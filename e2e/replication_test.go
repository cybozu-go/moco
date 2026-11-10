package e2e

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/donor.yaml
var donorYAML string

//go:embed testdata/replication.yaml
var replYAML string

const (
	donorClusterName          = "single"
	replClusterName           = "test"
	sharedReplicationReplicas = 3
)

// The namespaces are sharded by the parallel process, so that the specs can run in parallel.
// Within a process, specs run one by one, so the per-process clusters are never used concurrently.
func donorNamespace() string {
	return fmt.Sprintf("donor-p%d", GinkgoParallelProcess())
}

func sharedReplicationNamespace() string {
	return fmt.Sprintf("repl-p%d", GinkgoParallelProcess())
}

func dedicatedReplicationNamespace() string {
	return fmt.Sprintf("repl-new-p%d", GinkgoParallelProcess())
}

// The shared clusters are created on first use, and deleted when the process
// moves on to other specs or in AfterSuite. Specs using them must restore the
// states checked by checkDonorReusable and checkSharedReplicationReusable.
var (
	sharedDonorCreated       bool
	sharedReplicationCreated bool
)

const replicationLabel = "replication"

var _ = Describe("MySQLCluster replicating from an external donor", Label(replicationLabel), func() {
	if doUpgrade {
		return
	}

	var replNS string

	BeforeEach(func() {
		ensureSharedDonor()
		DeferCleanup(func() error {
			return checkDonorReusable()
		})
	})

	Context("using the shared 3-instance cluster", SpecPriority(priorityReplicationShared), func() {
		BeforeEach(func() {
			replNS = ensureSharedReplicationCluster()
			// Registered first, so this runs after the cleanups registered by each spec.
			DeferCleanup(func() {
				waitForSharedReplicationReusable()
			})
		})

		It("clones the donor data and creates the MOCO users", func() {
			By("checking all instances have the rows in the donor")
			for i := range sharedReplicationReplicas {
				Expect(countRows(replNS, i, "t")).To(Equal(3), "instance %d", i)
			}

			By("checking the MOCO users dropped in the donor can log in")
			for _, user := range []string{"moco-readonly", "moco-writable"} {
				_, err := kubectl(nil, "moco", "-n", replNS, "mysql", "-u", user, replClusterName, "--", "-e", "SELECT 1")
				Expect(err).NotTo(HaveOccurred(), "user %s", user)
			}
		})

		When("a transaction is written to the donor", func() {
			var table string

			BeforeEach(func() {
				table = createDonorTable()
				insertIntoDonor(table, "ddd")
			})

			It("replicates the transaction to all instances", func() {
				for i := range sharedReplicationReplicas {
					waitForRows(replNS, i, table, 1)
				}
			})
		})

		When("a switchover is requested", func() {
			var expectedPrimary int

			BeforeEach(func() {
				expectedPrimary = replicaIndices(replNS)[0]
				kubectlSafe(nil, "moco", "-n", replNS, "switchover", replClusterName)
			})

			It("moves the primary to the lowest-ordinal replica and becomes Healthy", func() {
				waitForPrimary(replNS, expectedPrimary)
			})
		})

		// Issue #731
		When("a replica is lagging and another replica has been re-initialized by CLONE", func() {
			var (
				reinitializedReplica, laggingReplica int
				table                                string
			)

			// Expected state after setup:
			// - The current primary (the replicas' intermediate primary) has applied "eee" and has
			//   its corresponding binary log entry.
			// - reinitializedReplica (replicas[0]) has the latest data from CLONE, but its binary logs
			//   do not include "eee" because CLONE copied the data, not the binary log history.
			// - laggingReplica (replicas[1]) has received "eee" in its relay log, but has not applied
			//   it to its tablespace because its SQL thread is stopped.
			BeforeEach(func() {
				// The replica with the lowest ordinal becomes the target of the next switchover.
				replicas := replicaIndices(replNS)
				reinitializedReplica, laggingReplica = replicas[0], replicas[1]

				table = createDonorTable()
				for i := range sharedReplicationReplicas {
					waitForRows(replNS, i, table, 0)
				}

				DeferCleanup(func() {
					startReplicaSQLThread(replNS, laggingReplica)
				})
				stopReplicaSQLThread(replNS, laggingReplica)
				insertIntoDonor(table, "eee")

				recreateInstance(replNS, replClusterName, reinitializedReplica)
				waitForRows(replNS, reinitializedReplica, table, 1)
				// The lagging replica is not ready while its SQL thread is stopped,
				// so the cluster becomes Degraded rather than Healthy.
				primaryHost := podHostname(replNS, currentPrimary(replNS))
				Eventually(func() (string, error) {
					return replicationSourceHost(replNS, reinitializedReplica)
				}).Should(Equal(primaryHost))
				waitForPodReady(replNS, reinitializedReplica)
				waitForClusterCondition(replNS, replClusterName, mocov1beta2.ConditionAvailable, metav1.ConditionTrue)
				Expect(countRows(replNS, laggingReplica, table)).To(Equal(0), "the lagging replica must not apply the transaction yet")
			})

			// Merged into one spec to avoid re-initializing an instance twice.
			It("switches over to the re-initialized replica without losing the transactions of the lagging replica", func() {
				By("[Act] requesting a switchover")
				kubectlSafe(nil, "moco", "-n", replNS, "switchover", replClusterName)
				Eventually(func(g Gomega) {
					cluster, err := getCluster(replNS, replClusterName)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(cluster.Status.CurrentPrimaryIndex).To(Equal(reinitializedReplica))
				}).Should(Succeed())

				// The new primary (reinitializedReplica) does not have the binary logs of the transactions that it got by CLONE.
				// If the source of the laggingReplica were changed now, the replica would lose
				// the retrieved transactions in its relay log and could not get them again.
				By("[Assert] the source of the lagging replica is not changed until it applies the retrieved transactions")
				newPrimaryHost := podHostname(replNS, reinitializedReplica)
				Consistently(func() (string, error) {
					return replicationSourceHost(replNS, laggingReplica)
				}).WithTimeout(30 * time.Second).WithPolling(2 * time.Second).ShouldNot(Equal(newPrimaryHost))

				By("[Act] resuming the SQL thread of the lagging replica")
				startReplicaSQLThread(replNS, laggingReplica)

				By("[Assert] the lagging replica catches up and replicates from the new primary")
				waitForRows(replNS, laggingReplica, table, 1)
				Eventually(func() (string, error) {
					return replicationSourceHost(replNS, laggingReplica)
				}).Should(Equal(newPrimaryHost))
				waitForPrimary(replNS, reinitializedReplica)
			})
		})

		// Merged into one spec to avoid re-initializing the errant replica twice.
		It("reports a replica with errant transactions and clears the status after the replica is re-initialized", func() {
			errant := replicaIndices(replNS)[0]
			DeferCleanup(func() {
				reinitializeErrantReplica(replNS, errant)
			})

			By("[Act] writing a transaction directly to a replica")
			writeErrantTransaction(replNS, errant)

			By("[Assert] the replica is reported as errant")
			Eventually(func(g Gomega) {
				cluster, err := getCluster(replNS, replClusterName)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cluster.Status.ErrantReplicas).To(Equal(1))
				g.Expect(cluster.Status.ErrantReplicaList).To(Equal([]int{errant}))
				cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			}).Should(Succeed())

			By("[Act] re-initializing the errant replica")
			recreateInstance(replNS, replClusterName, errant)

			By("[Assert] the errant status is cleared and the cluster becomes Healthy")
			waitForNoErrantReplicaAndHealthy(replNS)
		})

		When("the primary loses its data", func() {
			var oldPrimary int

			BeforeEach(func() {
				// The Pod that lost its data is cloned from the new primary and becomes a replica,
				// which the cleanup registered above waits for.
				oldPrimary = currentPrimary(replNS)
				recreateInstance(replNS, replClusterName, oldPrimary)
			})

			It("fails over to another replica", func() {
				Eventually(func(g Gomega) {
					cluster, err := getCluster(replNS, replClusterName)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(cluster.Status.CurrentPrimaryIndex).NotTo(Equal(oldPrimary))
					g.Expect(checkClusterCondition(replNS, replClusterName, mocov1beta2.ConditionAvailable, metav1.ConditionTrue)).To(Succeed())
				}).Should(Succeed())
			})
		})
	})

	// The following operations cannot be undone, so each spec creates its own cluster.
	// - The webhook rejects decreasing replicas.
	// - The webhook rejects setting replicationSourceSecretName again.
	Context("using a dedicated 3-instance cluster", SpecPriority(priorityReplicationDedicated), func() {
		BeforeEach(func() {
			replNS = createDedicatedReplicationCluster(sharedReplicationReplicas)
		})

		When("the cluster is scaled out to 5 instances", func() {
			BeforeEach(func() {
				kubectlSafe(nil, "-n", replNS, "patch", "mysqlcluster", replClusterName, "--type=merge", "-p", `{"spec":{"replicas":5}}`)
			})

			It("clones data to the new instances and becomes Healthy", func() {
				Eventually(func(g Gomega) {
					cluster, err := getCluster(replNS, replClusterName)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(cluster.Status.SyncedReplicas).To(Equal(5))
					cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				}).Should(Succeed())

				for _, i := range []int{3, 4} {
					Expect(countRows(replNS, i, "t")).To(Equal(3), "instance %d", i)
				}
			})
		})

		When("replicationSourceSecretName is removed", func() {
			BeforeEach(func() {
				kubectlSafe(nil, "-n", replNS, "patch", "mysqlcluster", replClusterName, "--type=merge", "-p", `{"spec":{"replicationSourceSecretName":null}}`)
			})

			It("accepts writes through the primary Service and serves them through the replica Service", func() {
				By("inserting a row with moco-writable through the primary Service")
				writablePasswd := userPassword(replNS, replClusterName, "moco-writable")
				Eventually(func() error {
					_, err := runInPod("mysql", "-u", "moco-writable", "-p"+writablePasswd,
						"-h", fmt.Sprintf("moco-%s-primary.%s.svc", replClusterName, replNS),
						"-D", "test", "-e", "INSERT INTO t (data) VALUES ('fff')")
					return err
				}).Should(Succeed())

				By("reading the row with moco-readonly through the replica Service")
				readonlyPasswd := userPassword(replNS, replClusterName, "moco-readonly")
				Eventually(func() (int, error) {
					return queryIntInPod(replicaServiceHost(replNS), "moco-readonly", readonlyPasswd, "SELECT COUNT(*) FROM t")
				}).Should(Equal(4))
			})
		})
	})

	// Failover requires more than half of all instances to be healthy, non-errant replicas.
	// With 3 instances, an errant replica leaves too few candidates after the primary loses its data.
	Context("using a dedicated 5-instance cluster with an errant replica", SpecPriority(priorityReplicationDedicated), func() {
		var errant int

		BeforeEach(func() {
			replNS = createDedicatedReplicationCluster(5)
			errant = replicaIndices(replNS)[0]
			writeErrantTransaction(replNS, errant)
			waitForErrantReplica(replNS, errant)
		})

		When("the primary loses its data", func() {
			var oldPrimary int

			BeforeEach(func() {
				oldPrimary = currentPrimary(replNS)
				recreateInstance(replNS, replClusterName, oldPrimary)
			})

			It("fails over to a replica that is not errant", func() {
				Eventually(func(g Gomega) {
					cluster, err := getCluster(replNS, replClusterName)
					g.Expect(err).NotTo(HaveOccurred())
					primary := cluster.Status.CurrentPrimaryIndex
					g.Expect(primary).NotTo(Equal(oldPrimary))
					g.Expect(primary).NotTo(Equal(errant))
					g.Expect(checkClusterCondition(replNS, replClusterName, mocov1beta2.ConditionAvailable, metav1.ConditionTrue)).To(Succeed())
				}).Should(Succeed())
			})
		})
	})
})

type replicationTemplateParams struct {
	Namespace    string
	Replicas     int
	MySQLVersion string
}

// ensureSharedDonor creates the donor on first use and checks that it is reusable before each reuse.
func ensureSharedDonor() {
	GinkgoHelper()
	ns := donorNamespace()
	if sharedDonorCreated {
		Eventually(func() error {
			return checkDonorReusable()
		}).Should(Succeed())
		return
	}

	// Delete the leftover of an interrupted run.
	deleteNamespace(ns)
	sharedDonorCreated = true
	kubectlSafe(fillTemplateWithParams(donorYAML, replicationTemplateParams{
		Namespace:    ns,
		MySQLVersion: mysqlVersion,
	}), "apply", "-f", "-")
	waitForClusterCondition(ns, donorClusterName, mocov1beta2.ConditionHealthy, metav1.ConditionTrue)
	for _, query := range []string{
		"CREATE DATABASE test",
		"CREATE TABLE test.t (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL) ENGINE=InnoDB",
		"INSERT INTO test.t (data) VALUES ('aaa'), ('bbb'), ('ccc')",
		"CREATE USER donor IDENTIFIED BY 'abc'",
		"GRANT BACKUP_ADMIN, REPLICATION SLAVE ON *.* TO 'donor'@'%'",
		// MOCO must create these users in the clusters cloned from the donor.
		"DROP USER 'moco-readonly'@'%'",
		"DROP USER 'moco-writable'@'%'",
	} {
		execOnDonor(query)
	}
	Eventually(func() error {
		return checkDonorReusable()
	}).Should(Succeed())
}

func checkDonorReusable() error {
	ns := donorNamespace()
	if err := checkClusterCondition(ns, donorClusterName, mocov1beta2.ConditionHealthy, metav1.ConditionTrue); err != nil {
		return err
	}
	count, err := queryInt(ns, donorClusterName, 0, "SELECT COUNT(*) FROM test.t")
	if err != nil {
		return err
	}
	if count != 3 {
		return fmt.Errorf("test.t in the donor has %d rows", count)
	}
	count, err = queryInt(ns, donorClusterName, 0, "SELECT COUNT(*) FROM mysql.user WHERE user IN ('moco-readonly', 'moco-writable')")
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("the donor has MOCO users")
	}
	return nil
}

// ensureSharedReplicationCluster creates the cluster on first use and recreates it if it is no longer reusable.
func ensureSharedReplicationCluster() string {
	GinkgoHelper()
	ns := sharedReplicationNamespace()
	if sharedReplicationCreated {
		err := checkSharedReplicationReusable()
		if err == nil {
			return ns
		}
		AddReportEntry("Shared replication cluster recovery", fmt.Sprintf("not reusable: %v", err))
	}

	deleteNamespace(ns)
	sharedReplicationCreated = true
	createReplicationCluster(ns, sharedReplicationReplicas)
	waitForSharedReplicationReusable()
	return ns
}

func waitForSharedReplicationReusable() {
	GinkgoHelper()
	Eventually(func() error {
		return checkSharedReplicationReusable()
	}).WithPolling(2 * time.Second).Should(Succeed())
}

// checkSharedReplicationReusable checks the state every spec using the shared cluster can assume.
// The index of the primary is intentionally not part of the reusable state.
func checkSharedReplicationReusable() error {
	ns := sharedReplicationNamespace()
	cluster, err := getCluster(ns, replClusterName)
	if err != nil {
		return err
	}
	cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
	if err != nil {
		return err
	}
	if cond.Status != metav1.ConditionTrue {
		return fmt.Errorf("cluster is not healthy: %s", cond.Status)
	}
	if cluster.Spec.Replicas != sharedReplicationReplicas || cluster.Status.SyncedReplicas != sharedReplicationReplicas {
		return fmt.Errorf("unexpected replicas: spec=%d synced=%d", cluster.Spec.Replicas, cluster.Status.SyncedReplicas)
	}
	if cluster.Status.ErrantReplicas != 0 {
		return fmt.Errorf("cluster has %d errant replicas", cluster.Status.ErrantReplicas)
	}
	if cluster.Spec.ReplicationSourceSecretName == nil {
		return errors.New("replicationSourceSecretName is not set")
	}

	for i := range sharedReplicationReplicas {
		out, err := execAsAdmin(ns, replClusterName, i, "-N", "-e",
			"SELECT CONCAT(c.SERVICE_STATE, ',', a.SERVICE_STATE) FROM performance_schema.replication_connection_status c "+
				"JOIN performance_schema.replication_applier_status a USING (CHANNEL_NAME)")
		if err != nil {
			return err
		}
		if state := strings.TrimSpace(string(out)); state != "ON,ON" {
			return fmt.Errorf("replication threads of instance %d are not running: %q", i, state)
		}
		count, err := countRows(ns, i, "t")
		if err != nil {
			return err
		}
		if count != 3 {
			return fmt.Errorf("test.t in instance %d has %d rows", i, count)
		}
	}
	return nil
}

// createDedicatedReplicationCluster creates a replication cluster that is deleted at the end of the spec.
func createDedicatedReplicationCluster(replicas int) string {
	GinkgoHelper()
	ns := dedicatedReplicationNamespace()
	deleteNamespace(ns)
	DeferCleanup(func() {
		deleteNamespace(ns)
	})
	createReplicationCluster(ns, replicas)
	return ns
}

func createReplicationCluster(ns string, replicas int) {
	GinkgoHelper()
	kubectlSafe(fillTemplateWithParams(replYAML, replicationTemplateParams{
		Namespace:    ns,
		Replicas:     replicas,
		MySQLVersion: mysqlVersion,
	}), "apply", "-f", "-")

	donorNS := donorNamespace()
	adminPasswd := userPassword(donorNS, donorClusterName, "moco-admin")
	kubectlSafe(nil, "-n", ns, "create", "secret", "generic", "donor",
		fmt.Sprintf("--from-literal=HOST=moco-%s-primary.%s.svc", donorClusterName, donorNS),
		"--from-literal=PORT=3306",
		"--from-literal=USER=donor",
		"--from-literal=PASSWORD=abc",
		"--from-literal=INIT_USER=moco-admin",
		"--from-literal=INIT_PASSWORD="+adminPasswd,
	)

	waitForClusterCondition(ns, replClusterName, mocov1beta2.ConditionHealthy, metav1.ConditionTrue)
}

// cleanupSharedClusters deletes the shared clusters created for this process.
func cleanupSharedClusters() {
	GinkgoHelper()
	if sharedReplicationCreated {
		deleteNamespace(sharedReplicationNamespace())
		sharedReplicationCreated = false
	}
	if sharedDonorCreated {
		deleteNamespace(donorNamespace())
		sharedDonorCreated = false
	}
}

// execAsAdmin runs mysql as moco-admin in an instance of a MySQLCluster.
func execAsAdmin(ns, name string, index int, args ...string) ([]byte, error) {
	a := []string{"moco", "-n", ns, "mysql", "-u", "moco-admin", "--index", strconv.Itoa(index), name, "--"}
	return kubectl(nil, append(a, args...)...)
}

func queryInt(ns, name string, index int, query string) (int, error) {
	out, err := execAsAdmin(ns, name, index, "-N", "-e", query)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func countRows(ns string, index int, table string) (int, error) {
	return queryInt(ns, replClusterName, index, "SELECT COUNT(*) FROM test."+table)
}

func waitForRows(ns string, index int, table string, rows int) {
	GinkgoHelper()
	Eventually(func() (int, error) {
		return countRows(ns, index, table)
	}).Should(Equal(rows), "instance %d, table %s", index, table)
}

func queryIntInPod(host, user, passwd, query string) (int, error) {
	out, err := runInPod("mysql", "-u", user, "-p"+passwd, "-h", host, "-N", "-D", "test", "-e", query)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func replicaServiceHost(ns string) string {
	return fmt.Sprintf("moco-%s-replica.%s.svc", replClusterName, ns)
}

func userPassword(ns, name, user string) string {
	GinkgoHelper()
	out := kubectlSafe(nil, "moco", "-n", ns, "credential", "-u", user, name)
	return strings.TrimSpace(string(out))
}

func execOnDonor(query string) {
	GinkgoHelper()
	_, err := execAsAdmin(donorNamespace(), donorClusterName, 0, "--init_command=SET autocommit=1", "-e", query)
	Expect(err).NotTo(HaveOccurred(), "query: %s", query)
}

// createDonorTable creates a table used only by the current spec, and drops it at the end of the spec.
func createDonorTable() string {
	GinkgoHelper()
	table := fmt.Sprintf("t%d", time.Now().UnixNano())
	execOnDonor(fmt.Sprintf("CREATE TABLE test.%s (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL) ENGINE=InnoDB", table))
	DeferCleanup(func() {
		execOnDonor(fmt.Sprintf("DROP TABLE IF EXISTS test.%s", table))
	})
	return table
}

func insertIntoDonor(table, data string) {
	GinkgoHelper()
	execOnDonor(fmt.Sprintf("INSERT INTO test.%s (data) VALUES ('%s')", table, data))
}

func currentPrimary(ns string) int {
	GinkgoHelper()
	cluster, err := getCluster(ns, replClusterName)
	Expect(err).NotTo(HaveOccurred())
	return cluster.Status.CurrentPrimaryIndex
}

// replicaIndices returns the indices of the replicas in ascending order.
func replicaIndices(ns string) []int {
	GinkgoHelper()
	cluster, err := getCluster(ns, replClusterName)
	Expect(err).NotTo(HaveOccurred())
	var indices []int
	for i := range int(cluster.Spec.Replicas) {
		if i != cluster.Status.CurrentPrimaryIndex {
			indices = append(indices, i)
		}
	}
	return indices
}

// waitForPrimary waits until the instance becomes the primary and the cluster becomes Healthy.
// It also checks the role label because the status can be stale just after a switchover.
func waitForPrimary(ns string, index int) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		cluster, err := getCluster(ns, replClusterName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cluster.Status.CurrentPrimaryIndex).To(Equal(index))
		cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))

		out, err := kubectl(nil, "-n", ns, "get", "pod", cluster.PodName(index), "-o", "json")
		g.Expect(err).NotTo(HaveOccurred())
		pod := &corev1.Pod{}
		g.Expect(json.Unmarshal(out, pod)).To(Succeed())
		g.Expect(pod.Labels).To(HaveKeyWithValue(constants.LabelMocoRole, constants.RolePrimary))
	}).Should(Succeed())
}

func podHostname(ns string, index int) string {
	GinkgoHelper()
	cluster, err := getCluster(ns, replClusterName)
	Expect(err).NotTo(HaveOccurred())
	return cluster.PodHostname(index)
}

func waitForPodReady(ns string, index int) {
	GinkgoHelper()
	podName := fmt.Sprintf("moco-%s-%d", replClusterName, index)
	Eventually(func(g Gomega) {
		out, err := kubectl(nil, "-n", ns, "get", "pod", podName, "-o", "json")
		g.Expect(err).NotTo(HaveOccurred())
		pod := &corev1.Pod{}
		g.Expect(json.Unmarshal(out, pod)).To(Succeed())
		g.Expect(pod.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", corev1.PodReady),
			HaveField("Status", corev1.ConditionTrue),
		)), "pod %s is not ready", podName)
	}).Should(Succeed())
}

func replicationSourceHost(ns string, index int) (string, error) {
	out, err := execAsAdmin(ns, replClusterName, index, "-N", "-e", "SELECT HOST FROM performance_schema.replication_connection_configuration")
	return strings.TrimSpace(string(out)), err
}

func stopReplicaSQLThread(ns string, index int) {
	GinkgoHelper()
	_, err := execAsAdmin(ns, replClusterName, index, "-e", "STOP REPLICA SQL_THREAD")
	Expect(err).NotTo(HaveOccurred())
}

func startReplicaSQLThread(ns string, index int) {
	GinkgoHelper()
	_, err := execAsAdmin(ns, replClusterName, index, "-e", "START REPLICA SQL_THREAD")
	Expect(err).NotTo(HaveOccurred())
}

func writeErrantTransaction(ns string, index int) {
	GinkgoHelper()
	// MOCO makes the replica read-only again, so retry until the transaction is written.
	Eventually(func() error {
		_, err := execAsAdmin(ns, replClusterName, index, "-e", "SET GLOBAL read_only=0; CREATE DATABASE IF NOT EXISTS errant")
		return err
	}).Should(Succeed())
}

func waitForErrantReplica(ns string, index int) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		cluster, err := getCluster(ns, replClusterName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cluster.Status.ErrantReplicas).To(Equal(1))
		g.Expect(cluster.Status.ErrantReplicaList).To(Equal([]int{index}))
	}).Should(Succeed())
}

func waitForNoErrantReplicaAndHealthy(ns string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		cluster, err := getCluster(ns, replClusterName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cluster.Status.ErrantReplicas).To(BeZero())
		g.Expect(cluster.Status.ErrantReplicaList).To(BeEmpty())
		cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	}).Should(Succeed())
}

// reinitializeErrantReplica re-initializes the errant replica unless it is already restored.
func reinitializeErrantReplica(ns string, index int) {
	GinkgoHelper()
	cluster, err := getCluster(ns, replClusterName)
	Expect(err).NotTo(HaveOccurred())
	// Check the data too, because MOCO may not have detected the errant transaction yet.
	errantDBs, err := queryInt(ns, replClusterName, index, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = 'errant'")
	if err == nil && errantDBs == 0 && cluster.Status.ErrantReplicas == 0 {
		return
	}
	recreateInstance(ns, replClusterName, index)
	waitForNoErrantReplicaAndHealthy(ns)
}
