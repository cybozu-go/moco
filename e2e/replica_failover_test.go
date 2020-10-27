package e2e

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cybozu-go/moco"
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

		By("preparing data")
		count := 100000
		err = insertData(primaryDB, count)
		Expect(err).ShouldNot(HaveOccurred())
		selectRows, err := primaryDB.Query("SELECT count(*) FROM moco_e2e.replication_test")
		Expect(err).ShouldNot(HaveOccurred())
		primaryCount := 0
		for selectRows.Next() {
			err = selectRows.Scan(&primaryCount)
			Expect(err).ShouldNot(HaveOccurred())
		}
		Expect(primaryCount).Should(BeNumerically(">", count))

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

		By("deleting Pod and PVC/PV of the target replica")
		podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), targetReplica)
		pvcName := fmt.Sprintf("mysql-data-%s", podName)
		wg := &sync.WaitGroup{}
		wg.Add(1)

		var stdout2, stderr2 []byte
		var err2 error
		go func() {
			stdout2, stderr2, err2 = kubectl("-n", "e2e-test", "delete", "pvc", pvcName)
			wg.Done()
		}()
		stdout, stderr, err := kubectl("-n", "e2e-test", "patch", "pvc", pvcName, "-p", `{"metadata": {"finalizers" : null}}`)
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)
		wg.Wait()
		Expect(err2).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout2, stderr2)

		stdout, stderr, err = kubectl("-n", "e2e-test", "delete", "pod", podName)
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		Eventually(func() error {
			cluster, err = getMySQLCluster()
			if cluster.Status.Ready != corev1.ConditionFalse {
				return errors.New("status should be in error once")
			}
			return nil
		}).Should(Succeed())

		Eventually(func() error {
			cluster, err = getMySQLCluster()
			if cluster.Status.Ready != corev1.ConditionTrue {
				return errors.New("cluster should recover")
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
			if replicatedCount != primaryCount {
				return fmt.Errorf("repcalited: %d", replicatedCount)
			}
			return nil
		}).Should(Succeed())
	})
}
