package e2e

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
)

func testBootstrap() {
	It("should create cluster", func() {
		By("registering MySQLCluster")
		_, _, _ = kubectl("create", "ns", "e2e-test") // ignore error
		stdout, stderr, err := kubectl("apply", "-n", "e2e-test", "-f", "manifests/mysql_cluster.yaml")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		By("getting Secret which contains root password")
		Eventually(func() error {
			mysqlCluster, err := getMySQLCluster()
			if err != nil {
				return err
			}
			_, err = getRootPassword(mysqlCluster)
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		By("getting StatefulSet")
		Eventually(func() error {
			mysqlCluster, err := getMySQLCluster()
			if err != nil {
				return err
			}

			stdout, stderr, err = kubectl("get", "-n", "e2e-test", "statefulsets", moco.UniqueName(mysqlCluster), "-o", "json")
			if err != nil {
				return fmt.Errorf("failed to get StatefulSet. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}

			var sts appsv1.StatefulSet
			err = json.Unmarshal(stdout, &sts)
			if err != nil {
				return fmt.Errorf("failed to unmarshal StatefulSet. stdout: %s, err: %v", stdout, err)
			}

			if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 3 {
				return fmt.Errorf("replicas should be 3: %v", sts.Spec.Replicas)
			}

			if sts.Status.ReadyReplicas != 3 {
				return fmt.Errorf("readyReplicas should be 3: %v", sts.Status.ReadyReplicas)
			}
			return nil
		}, 3*time.Minute).Should(Succeed())
	})

	It("should replicate data", func() {
		mysqlCluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())
		connector := newMySQLConnector(mysqlCluster)
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
		var replicaDB *sqlx.DB
		Eventually(func() error {
			replicaDB, err = connector.connect(1)
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())
		Expect(replicaDB).ShouldNot(BeNil())

		_, err = primaryDB.Exec("DROP DATABASE IF EXISTS moco_e2e")
		Expect(err).ShouldNot(HaveOccurred())

		_, err = primaryDB.Exec("CREATE DATABASE moco_e2e")
		Expect(err).ShouldNot(HaveOccurred())

		_, err = primaryDB.Exec(`CREATE TABLE moco_e2e.replication_test (
			num bigint unsigned NOT NULL AUTO_INCREMENT,
			val0 varchar(100) DEFAULT NULL,
			val1 varchar(100) DEFAULT NULL,
			val2 varchar(100) DEFAULT NULL,
			val3 varchar(100) DEFAULT NULL,
			val4 varchar(100) DEFAULT NULL,
			  UNIQUE KEY num (num)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
		`)
		Expect(err).ShouldNot(HaveOccurred())

		count := 100
		tx, err := primaryDB.Begin()
		Expect(err).ShouldNot(HaveOccurred())
		_, err = tx.Exec("SET SESSION cte_max_recursion_depth = ?", count)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = tx.Exec(`INSERT INTO moco_e2e.replication_test (val0, val1, val2, val3, val4) 
			WITH RECURSIVE t AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM t WHERE n < ?) 
			SELECT MD5(RAND()),MD5(RAND()),MD5(RAND()),MD5(RAND()),MD5(RAND()) FROM t
		`, count)
		Expect(err).ShouldNot(HaveOccurred())
		err = tx.Commit()
		Expect(err).ShouldNot(HaveOccurred())

		Eventually(func() error {
			rows, err := replicaDB.Query("SELECT count(*) FROM moco_e2e.replication_test")
			if err != nil {
				return err
			}
			replicatedCount := 0
			for rows.Next() {
				err = rows.Scan(&replicatedCount)
			}
			if replicatedCount != count {
				return fmt.Errorf("repcalited: %d", replicatedCount)
			}
			return nil
		}).Should(Succeed())
	})
}
