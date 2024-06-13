package e2e

import (
	_ "embed"
	"errors"
	"fmt"
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/clustering"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strconv"
	"strings"
)

//go:embed testdata/offline_test.yaml
var offlineYAML string

//go:embed testdata/offline_test_changed.yaml
var offlineChangedYAML string

var _ = Context("offline", func() {
	if doUpgrade {
		return
	}

	It("should construct a cluster", func() {
		kubectlSafe(fillTemplate(offlineYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("offline", "test")
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

		kubectlSafe(nil, "moco", "-n", "offline", "mysql", "-u", "moco-writable", "test", "--",
			"-e", "CREATE DATABASE test")
		kubectlSafe(nil, "moco", "-n", "offline", "mysql", "-u", "moco-writable", "test", "--",
			"-D", "test", "-e", "CREATE TABLE t (id INT NOT NULL AUTO_INCREMENT, data VARCHAR(32) NOT NULL, PRIMARY KEY (id), KEY key1 (data), KEY key2 (data, id)) ENGINE=InnoDB")
		kubectlSafe(nil, "moco", "-n", "offline", "mysql", "-u", "moco-writable", "test", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('aaa')")
	})

	It("should offline change succeed", func() {
		kubectlSafe(fillTemplate(offlineChangedYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("offline", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionFalse && cond.Reason == clustering.StateOffline.String() {
					return nil
				}
				return fmt.Errorf("cluster is healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
		verifyAllPodsDeleted("offline")
	})

	It("should online change succeed", func() {
		kubectlSafe(fillTemplate(offlineYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("offline", "test")
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
		out := kubectlSafe(nil, "moco", "-n", "offline", "mysql", "test", "--",
			"-N", "-D", "test", "-e", "SELECT COUNT(*) FROM t")
		count, err := strconv.Atoi(strings.TrimSpace(string(out)))
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("should delete namespace", func() {
		kubectlSafe(nil, "delete", "-n", "offline", "mysqlclusters", "--all")
		verifyAllPodsDeleted("offline")
	})
})
