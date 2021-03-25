package e2e

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cybozu-go/moco/api/v1beta1"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

func testPrimaryFailOver() {
	It("should do failover when a primary becomes unavailable", func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())
		connector := newMySQLConnector(cluster)
		err = connector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())
		defer connector.stopPortForward()
		Expect(cluster.Status.CurrentPrimaryIndex).ShouldNot(BeNil())
		firstPrimary := cluster.Status.CurrentPrimaryIndex

		By("deleting PVC of primary")
		podName := fmt.Sprintf("%s-%d", cluster.PrefixedName(), firstPrimary)
		pvcName := fmt.Sprintf("mysql-data-%s", podName)

		stdout, stderr, err := kubectl("-n", "e2e-test", "delete", "pvc", pvcName, "--wait=false")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)
		stdout, stderr, err = kubectl("-n", "e2e-test", "patch", "pvc", pvcName, "-p", `{"metadata": {"finalizers" : null}}`)
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)
		Eventually(func() error {
			stdout, stderr, err := kubectl("get", "pvc", "-n", "e2e-test", pvcName)
			if err == nil {
				return fmt.Errorf("pvc() should be removed. stdout: %s, stderr: %s", stdout, stderr)
			}
			if !strings.Contains(string(stderr), "not found") {
				return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			return nil
		}).Should(Succeed())

		By("deleting Pod of primary")
		stdout, stderr, err = kubectl("-n", "e2e-test", "delete", "pod", podName)
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		By("checking cluster status")
		Eventually(func() error {
			/*
			 * The condition may not become unhealthy immediately after deleting pod.
			 * So, the following `findCondition` may observe healthy before primary switching.
			 * On the other hand, it is not guaranteed to observe the unhealthy condition.
			 *
			 * Consequently, `Eventually` block must contain both condition check and primary index check.
			 */
			cluster, err = getMySQLCluster()
			healthy := findCondition(cluster.Status.Conditions, v1beta1.ConditionHealthy)
			if healthy == nil || healthy.Status != corev1.ConditionTrue {
				return errors.New("should recover")
			}

			newPrimary := cluster.Status.CurrentPrimaryIndex
			if newPrimary == firstPrimary {
				return fmt.Errorf("current primary is still %d", firstPrimary)
			}

			return nil
		}, 2*time.Minute).Should(Succeed())

		By("connecting to recovered instance")
		connector.stopPortForward()
		err = connector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())

		var primaryDB *sqlx.DB
		Eventually(func() error {
			primaryDB, err = connector.connectToPrimary()
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())
		selectRows, err := primaryDB.Query("SELECT count(*) FROM moco_e2e.replication_test")
		Expect(err).ShouldNot(HaveOccurred())
		primaryCount := 0
		for selectRows.Next() {
			err = selectRows.Scan(&primaryCount)
			Expect(err).ShouldNot(HaveOccurred())
		}
		Expect(primaryCount).ShouldNot(Equal(0))

		var replicaDB *sqlx.DB
		Eventually(func() error {
			replicaDB, err = connector.connect(firstPrimary)
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		Eventually(func() error {
			selectRows, err := replicaDB.Query("SELECT count(*) FROM moco_e2e.replication_test")
			if err != nil {
				return err
			}
			count := 0
			for selectRows.Next() {
				err = selectRows.Scan(&count)
				if err != nil {
					return err
				}
			}
			if count != primaryCount {
				return fmt.Errorf("replicated: %d", count)
			}
			return nil
		}).Should(Succeed())
	})
}
