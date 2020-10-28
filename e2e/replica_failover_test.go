package e2e

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

func testReplicaFailOver() {
	It("should do failover when a replica becomes unavailable", func() {
		By("connecting to primary")
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())
		connector := newMySQLConnector(cluster)
		err = connector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())
		defer connector.stopPortForward()

		var primaryDB *sqlx.DB
		Eventually(func() error {
			primaryDB, err = connector.connectToPrimary()
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())
		targetReplica, err := replica0(cluster)
		Expect(err).ShouldNot(HaveOccurred())

		By("purging binlogs")
		_, err = primaryDB.Exec("FLUSH BINARY LOGS")
		Expect(err).ShouldNot(HaveOccurred())

		binlogs := make([]string, 0)
		binlogRows, err := primaryDB.Unsafe().Queryx("SHOW BINARY LOGS")
		Expect(err).ShouldNot(HaveOccurred())
		for binlogRows.Next() {
			results := struct {
				LogName string `db:"Log_name"`
			}{}
			err = binlogRows.StructScan(&results)
			Expect(err).ShouldNot(HaveOccurred())
			binlogs = append(binlogs, results.LogName)
		}
		Expect(binlogs).ShouldNot(BeEmpty())
		sort.Strings(binlogs)
		lastBinLog := binlogs[len(binlogs)-1]
		_, err = primaryDB.Exec("PURGE MASTER LOGS TO ?", lastBinLog)
		Expect(err).ShouldNot(HaveOccurred())

		By("deleting PVC of the target replica")
		podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), targetReplica)
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

		By("deleting Pod of the target replica")
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

		By("connecting to recovered replica")
		connector.stopPortForward()
		err = connector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())

		var replicaDB *sqlx.DB
		Eventually(func() error {
			replicaDB, err = connector.connect(targetReplica)
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
			replicatedCount := 0
			for selectRows.Next() {
				err = selectRows.Scan(&replicatedCount)
				if err != nil {
					return err
				}
			}
			if replicatedCount != 100000 {
				return fmt.Errorf("repcalited: %d", replicatedCount)
			}
			return nil
		}).Should(Succeed())
	})
}
