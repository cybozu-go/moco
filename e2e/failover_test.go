package e2e

import (
	_ "embed"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

//go:embed testdata/failover.yaml
var failoverYAML string

var _ = Context("failure", func() {
	if doUpgrade {
		return
	}

	It("should construct a 3-instance cluster", func() {
		kubectlSafe(fillTemplate(failoverYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("failover", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())

		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "failover",
			"-u", "moco-writable",
			"--", "-e", "CREATE DATABASE test;")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "failover",
			"-u", "moco-writable",
			"--", "-e", "CREATE TABLE test.t1 (foo int);")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "failover",
			"-u", "moco-writable",
			"--", "-e", "INSERT INTO test.t1 (foo) VALUES (1); COMMIT;")
	})

	It("should successful failover ", func() {
		By("kill primary instance")
		cluster, err := getCluster("failover", "test")
		Expect(err).NotTo(HaveOccurred())

		currentPrimaryIndex := cluster.Status.CurrentPrimaryIndex
		kubectlSafe(nil, "exec",
			fmt.Sprintf("moco-test-%d", currentPrimaryIndex),
			"-n", "failover",
			"-c", "mysqld",
			"--", "kill", "1")

		By("switch primary instance")
		Eventually(func() error {
			cluster, err := getCluster("failover", "test")
			if err != nil {
				return err
			}
			if currentPrimaryIndex == cluster.Status.CurrentPrimaryIndex {
				return errors.New("failover not yet executed")
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())

		// Immediately after replication,
		// 'Retrieved_Gtid_Set' will be empty if there are no new transactions.
		//
		//
		// SHOW REPLICA STATUS\G
		//*************************** 1. row ***************************
		//...
		//           Retrieved_Gtid_Set: <empty>
		//            Executed_Gtid_Set: 09a2bf6a-960c-11ec-bf89-9ee9543e8238:1,
		//09c0848a-960c-11ec-90e9-0a55db88221e:1-4
		//...
		//
		// Verify that failover succeeds even if 'Retrieved_Gtid_Set' is empty.
		// https://github.com/cybozu-go/moco/issues/370
		By("kill primary instance one more")
		cluster, err = getCluster("failover", "test")
		Expect(err).NotTo(HaveOccurred())

		kubectlSafe(nil, "exec",
			fmt.Sprintf("moco-test-%d", cluster.Status.CurrentPrimaryIndex),
			"-n", "failover",
			"-c", "mysqld",
			"--", "kill", "1")

		By("switch primary instance")
		Eventually(func() error {
			cluster, err := getCluster("failover", "test")
			if err != nil {
				return err
			}
			if currentPrimaryIndex == cluster.Status.CurrentPrimaryIndex {
				return errors.New("failover not yet executed")
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("should delete clusters", func() {
		kubectlSafe(nil, "delete", "-n", "failover", "mysqlclusters", "--all")
	})
})
