package e2e

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/api/v1alpha1"
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
		firstPrimary := *cluster.Status.CurrentPrimaryIndex

		By("deleting PVC of primary")
		podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), firstPrimary)
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

		Eventually(func() error {
			cluster, err = getMySQLCluster()
			healthy := findCondition(cluster.Status.Conditions, v1alpha1.ConditionHealthy)
			if healthy == nil || healthy.Status != corev1.ConditionTrue {
				return errors.New("should recover")
			}
			return nil
		}, 2*time.Minute).Should(Succeed())

		Expect(cluster.Status.CurrentPrimaryIndex).ShouldNot(BeNil())
		newPrimary := *cluster.Status.CurrentPrimaryIndex
		Expect(newPrimary).ShouldNot(Equal(firstPrimary))

		By("connecting to recovered instance")
		connector.stopPortForward()
		err = connector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())

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
			if count != 100000 {
				return fmt.Errorf("repcalited: %d", count)
			}
			return nil
		}).Should(Succeed())
	})
}
