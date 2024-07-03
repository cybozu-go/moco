package e2e

import (
	_ "embed"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/switchover.yaml
var switchoverYAML string

var _ = Context("switchover", func() {
	if doUpgrade {
		return
	}

	It("should construct a 3-instance cluster", func() {
		kubectlSafe(fillTemplate(switchoverYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("switchover", "test")
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

		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "switchover",
			"-u", "moco-writable",
			"--", "-e", "CREATE DATABASE test;")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "switchover",
			"-u", "moco-admin",
			"--", "-e", "CREATE USER 'user'@'%' IDENTIFIED BY 'abc';")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "switchover",
			"-u", "moco-admin",
			"--", "-e", "GRANT ALL PRIVILEGES ON test.* TO 'user'@'%';")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "switchover",
			"-u", "moco-writable",
			"--", "-e", "CREATE TABLE test.t1 (foo int);")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "switchover",
			"-u", "moco-writable",
			"--", "-e", "INSERT INTO test.t1 (foo) VALUES (1), (2), (3), (4), (5), (6), (7), (8), (9), (10); COMMIT;")
	})

	It("should switch the primary if requested, even when a huge transaction is running", func() {
		go func() {
			runInPod("mysql", "-u", "user", "-pabc",
				"-h", "moco-test-primary.switchover.svc.cluster.local", "test",
				"-e", "INSERT INTO test.t1 (foo) SELECT a.foo a FROM test.t1 a, test.t1 b, test.t1 c, test.t1 d, test.t1 e, test.t1 f, test.t1 g, test.t1 h")
		}()
		kubectlSafe(nil, "moco", "-n", "switchover", "switchover", "test")
		Eventually(func() int {
			cluster, err := getCluster("switchover", "test")
			if err != nil {
				return 0
			}
			return cluster.Status.CurrentPrimaryIndex
		}).ShouldNot(Equal(0))

		Eventually(func() error {
			cluster, err := getCluster("switchover", "test")
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

	It("should delete clusters", func() {
		kubectlSafe(nil, "delete", "-n", "switchover", "mysqlclusters", "--all")
		verifyAllPodsDeleted("switchover")
	})
})
