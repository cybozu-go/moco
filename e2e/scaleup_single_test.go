package e2e

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/scaleup_single.yaml
var scaleupSingleYAML string

// At replicas: 1 the primary never had semi-sync enabled (configurePrimary
// returns early), so scaling out turns it on for the first time.
var _ = Context("scale up from a single instance", Ordered, func() {
	if doUpgrade {
		return
	}

	BeforeAll(func() {
		GinkgoWriter.Println("construct a 1-instance cluster")
		kubectlSafe(fillTemplate(scaleupSingleYAML), "apply", "-f", "-")
		Eventually(func(g Gomega) {
			cluster, err := getCluster("scaleup-single", "single")
			g.Expect(err).NotTo(HaveOccurred())
			cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())

		DeferCleanup(func() {
			GinkgoWriter.Println("delete clusters")
			kubectlSafe(nil, "delete", "-n", "scaleup-single", "mysqlclusters", "--all")
			verifyAllPodsDeleted("scaleup-single")
		})
	})

	It("should prepare a donor instance", func() {
		kubectlSafe(nil, "moco", "-n", "scaleup-single", "mysql", "-u", "moco-writable", "single", "--",
			"-e", "CREATE DATABASE test")
		kubectlSafe(nil, "moco", "-n", "scaleup-single", "mysql", "-u", "moco-writable", "single", "--",
			"-D", "test", "-e", "CREATE TABLE t (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL) ENGINE=InnoDB")
		kubectlSafe(nil, "moco", "-n", "scaleup-single", "mysql", "-u", "moco-writable", "single", "--",
			"-D", "test", "-e", "CREATE TABLE probe (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL) ENGINE=InnoDB")
		kubectlSafe(nil, "moco", "-n", "scaleup-single", "mysql", "-u", "moco-writable", "single", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('aaa'), ('bbb'), ('ccc')")
	})

	It("should be able to scale out the cluster from 1 to 3", func() {
		// The stall only happens while a transaction is waiting on semi-sync ack,
		// so keep one commit in flight across the scale-out.
		stopWriter := make(chan struct{})
		defer close(stopWriter)
		go func() {
			for {
				select {
				case <-stopWriter:
					return
				case <-time.After(200 * time.Millisecond):
				}
				// no error check: this call blocks indefinitely while the bug is present
				kubectl(nil, "moco", "-n", "scaleup-single", "mysql", "-u", "moco-writable", "single", "--",
					"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO probe (data) VALUES ('x')")
			}
		}()

		Eventually(func() error {
			cluster, err := getCluster("scaleup-single", "single")
			if err != nil {
				return err
			}
			cluster.Spec.Replicas = 3
			data, _ := json.Marshal(cluster)
			_, err = kubectl(data, "apply", "-f", "-")
			return err
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			cluster, err := getCluster("scaleup-single", "single")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cluster.Status.SyncedReplicas).To(Equal(3))
			cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("should replicate the data to the new instances", func() {
		Eventually(func(g Gomega) {
			out, err := kubectl(nil, "moco", "-n", "scaleup-single", "mysql", "--index", "2", "single", "--",
				"-N", "-D", "test", "-e", "SELECT COUNT(*) FROM t")
			g.Expect(err).NotTo(HaveOccurred())
			count, err := strconv.Atoi(strings.TrimSpace(string(out)))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(count).To(Equal(3))
		}).Should(Succeed())
	})
})
