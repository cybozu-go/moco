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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/donor.yaml
var donorYAML string

//go:embed testdata/replication.yaml
var replYAML string

var _ = Context("replication", Ordered, func() {
	if doUpgrade {
		return
	}

	It("should prepare a donor instance", func() {
		kubectlSafe(fillTemplate(donorYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("donor", "single")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())

		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-writable", "single", "--",
			"-e", "CREATE DATABASE test")
		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-writable", "single", "--",
			"-D", "test", "-e", "CREATE TABLE t (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL) ENGINE=InnoDB")
		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-writable", "single", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('aaa'), ('bbb'), ('ccc')")
		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-admin", "single", "--",
			"-e", "CREATE USER donor IDENTIFIED BY 'abc'")
		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-admin", "single", "--",
			"-e", "GRANT BACKUP_ADMIN, REPLICATION SLAVE ON *.* TO 'donor'@'%'")
		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-admin", "single", "--",
			"-e", "DROP USER 'moco-readonly'@'%'")
		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-admin", "single", "--",
			"-e", "DROP USER 'moco-writable'@'%'")
	})

	It("should construct an intermediate primary and replicas", func() {
		kubectlSafe(fillTemplate(replYAML), "apply", "-f", "-")

		out := kubectlSafe(nil, "moco", "-n", "donor", "credential", "-u", "moco-admin", "single")
		adminPasswd := strings.TrimSpace(string(out))

		kubectlSafe(nil, "-n", "repl", "create", "secret", "generic", "donor",
			"--from-literal=HOST=moco-single-primary.donor.svc",
			"--from-literal=PORT=3306",
			"--from-literal=USER=donor",
			"--from-literal=PASSWORD=abc",
			"--from-literal=INIT_USER=moco-admin",
			"--from-literal=INIT_PASSWORD="+adminPasswd,
		)

		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("should replicate data from the donor", func() {
		out := kubectlSafe(nil, "moco", "-n", "repl", "mysql", "--index", "1", "test", "--",
			"-N", "-D", "test", "-e", "SELECT COUNT(*) FROM t")
		count, err := strconv.Atoi(strings.TrimSpace(string(out)))
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(3))

		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-admin", "single", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('ddd')")
		Eventually(func() int {
			out, err := kubectl(nil, "moco", "-n", "repl", "mysql", "--index", "2", "test", "--",
				"-N", "-D", "test", "-e", "SELECT COUNT(*) FROM t")
			if err != nil {
				return 0
			}
			count, _ := strconv.Atoi(strings.TrimSpace(string(out)))
			return count
		}).Should(Equal(4))
	})

	It("should switch the primary if requested", func() {
		kubectlSafe(nil, "moco", "-n", "repl", "switchover", "test")
		time.Sleep(10 * time.Second)
		Eventually(func() int {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return 0
			}
			return cluster.Status.CurrentPrimaryIndex
		}).ShouldNot(Equal(0))

		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("should switch the primary even with replication delays and new primary has initialized", func() {
		kubectlSafe(nil, "moco", "-n", "repl", "mysql", "-u", "moco-admin", "test", "--index", "2", "--",
			"-e", "STOP REPLICA SQL_THREAD")
		kubectlSafe(nil, "moco", "-n", "donor", "mysql", "-u", "moco-admin", "single", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('eee')")
		kubectlSafe(nil, "delete", "pvc", "-n", "repl", "--wait=false", "mysql-data-moco-test-0")
		kubectlSafe(nil, "delete", "pod", "-n", "repl", "--grace-period=1", "moco-test-0")
		time.Sleep(60 * time.Second)
		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionAvailable {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not available: %s", cond.Status)
			}
			return errors.New("no available condition")
		}).Should(Succeed())

		kubectlSafe(nil, "moco", "-n", "repl", "switchover", "test")
		time.Sleep(10 * time.Second)
		Eventually(func() int {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return 0
			}
			return cluster.Status.CurrentPrimaryIndex
		}).ShouldNot(Equal(1))
		time.Sleep(10 * time.Second)
		kubectlSafe(nil, "moco", "-n", "repl", "mysql", "-u", "moco-admin", "test", "--index", "2", "--",
			"-e", "START REPLICA SQL_THREAD")
		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("should be able to scale out the cluster", func() {
		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			cluster.Spec.Replicas = 5
			data, _ := json.Marshal(cluster)
			_, err = kubectl(data, "apply", "-f", "-")
			return err
		}).Should(Succeed())

		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			if cluster.Status.SyncedReplicas != 5 {
				return fmt.Errorf("synced replicas is not 5: %d", cluster.Status.SyncedReplicas)
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("should detect errant transactions", func() {
		Eventually(func() error {
			_, err := kubectl(nil, "moco", "-n", "repl", "mysql", "-u", "moco-admin", "--index", "2", "test", "--",
				"-e", "SET GLOBAL read_only=0")
			if err != nil {
				return err
			}
			_, err = kubectl(nil, "moco", "-n", "repl", "mysql", "-u", "moco-admin", "--index", "2", "test", "--",
				"-e", "CREATE DATABASE errant")
			return err
		}).Should(Succeed())

		Eventually(func() int {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return 0
			}
			return cluster.Status.ErrantReplicas
		}).Should(Equal(1))
	})

	It("should do a failover if the primary lost data", func() {
		cluster, err := getCluster("repl", "test")
		Expect(err).NotTo(HaveOccurred())
		primary := cluster.Status.CurrentPrimaryIndex

		kubectlSafe(nil, "delete", "-n", "repl", "--wait=false", "pvc", fmt.Sprintf("mysql-data-moco-test-%d", primary))
		kubectlSafe(nil, "delete", "-n", "repl", "--grace-period=1", "pod", cluster.PodName(primary))

		Eventually(func() error {
			out, err := kubectl(nil, "-n", "repl", "get", "pod", cluster.PodName(primary), "-o", "json")
			if err != nil {
				return err
			}
			pod := &corev1.Pod{}
			err = json.Unmarshal(out, pod)
			if err != nil {
				return err
			}
			for _, cond := range pod.Status.Conditions {
				if cond.Type != corev1.PodScheduled {
					continue
				}
				if cond.Reason == "Unschedulable" {
					fmt.Println("re-deleting pending pod...")
					_, err := kubectl(nil, "delete", "-n", "repl", "--grace-period=1", "pod", cluster.PodName(primary))
					if err != nil {
						return fmt.Errorf("failed to delete pod: %w", err)
					}
					return errors.New("pod is unschedulable")
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
			}
			return errors.New("no pod scheduled status")
		}).Should(Succeed())

		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			if cluster.Status.CurrentPrimaryIndex == primary {
				return fmt.Errorf("primary is not changed from %d", primary)
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionAvailable {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not available: %s", cond.Status)
			}
			return errors.New("no available condition")
		}).Should(Succeed())
	})

	It("should clear errant status after re-initializing the errant replica", func() {
		cluster, err := getCluster("repl", "test")
		Expect(err).NotTo(HaveOccurred())

		kubectlSafe(nil, "delete", "-n", "repl", "--wait=false", "pvc", "mysql-data-moco-test-2")
		kubectlSafe(nil, "delete", "-n", "repl", "--grace-period=1", "pod", cluster.PodName(2))

		Eventually(func() error {
			out, err := kubectl(nil, "-n", "repl", "get", "pod", cluster.PodName(2), "-o", "json")
			if err != nil {
				return err
			}
			pod := &corev1.Pod{}
			err = json.Unmarshal(out, pod)
			if err != nil {
				return err
			}
			for _, cond := range pod.Status.Conditions {
				if cond.Type != corev1.PodScheduled {
					continue
				}
				if cond.Reason == "Unschedulable" {
					fmt.Println("re-deleting pending pod...")
					_, err := kubectl(nil, "delete", "-n", "repl", "--grace-period=1", "pod", cluster.PodName(2))
					if err != nil {
						return fmt.Errorf("failed to delete pod: %w", err)
					}
					return errors.New("pod is unschedulable")
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
			}
			return errors.New("no pod scheduled status")
		}).Should(Succeed())

		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("should be able to stop replication from the donor", func() {
		Eventually(func() error {
			cluster, err := getCluster("repl", "test")
			if err != nil {
				return err
			}
			cluster.Spec.ReplicationSourceSecretName = nil
			data, _ := json.Marshal(cluster)
			_, err = kubectl(data, "apply", "-f", "-")
			return err
		}).Should(Succeed())

		Eventually(func() error {
			_, err := kubectl(nil, "moco", "-n", "repl", "mysql", "-u", "moco-writable", "test", "--",
				"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('fff')")
			return err
		}).Should(Succeed())
	})

	It("should allow reads via Service", func() {
		By("obtaining the credential")
		out := kubectlSafe(nil, "moco", "-n", "repl", "credential", "-u", "moco-readonly", "test")
		passwd := strings.TrimSpace(string(out))

		By("running mysql in a pod")
		Eventually(func() int {
			out, err := runInPod("mysql", "-u", "moco-readonly", "-p"+passwd,
				"-h", "moco-test-replica.repl.svc", "-N", "-D", "test", "-e", "SELECT COUNT(*) FROM t")
			if err != nil {
				return 0
			}
			count, _ := strconv.Atoi(strings.TrimSpace(string(out)))
			return count
		}).Should(Equal(6))
	})

	It("should delete clusters", func() {
		kubectlSafe(nil, "delete", "-n", "donor", "mysqlclusters", "--all")
		kubectlSafe(nil, "delete", "-n", "repl", "mysqlclusters", "--all")
		verifyAllPodsDeleted("donor")
		verifyAllPodsDeleted("repl")

	})
})
