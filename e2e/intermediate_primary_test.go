package e2e

import (
	"errors"
	"fmt"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

const (
	nsExternal = "e2e-test-external"
)

func testIntermediatePrimary() {
	It("should create intermediate cluster", func() {
		By("creating namespace")
		_, _, _ = kubectl("create", "ns", nsExternal) // ignore error

		By("creating replication source secret")
		donorCluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())
		passwordSecret, err := getPasswordSecret(donorCluster)

		secret := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: replication-source-secret
type: Opaque
stringData:
  PRIMARY_HOST: %s.e2e-test.svc
  PRIMARY_PORT: "%s"
  PRIMARY_USER: %s
  PRIMARY_PASSWORD: %s
  CLONE_USER: %s
  CLONE_PASSWORD: %s
  INIT_AFTER_CLONE_USER: %s
  INIT_AFTER_CLONE_PASSWORD: %s
`, fmt.Sprintf("%s-replica", moco.UniqueName(donorCluster)), "3306",
			moco.ReplicationUser, string(passwordSecret.Data[moco.ReplicationPasswordKey]),
			moco.CloneDonorUser, string(passwordSecret.Data[moco.CloneDonorPasswordKey]),
			"moco-admin", string(passwordSecret.Data[moco.AdminPasswordKey]))
		stdout, stderr, err := kubectlWithInput([]byte(secret), "apply", "-n"+nsExternal, "-f", "-")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s, secret=%v", stdout, stderr, secret)

		By("creating intermediate cluster")
		stdout, stderr, err = kubectl("apply", "-n", nsExternal, "-f", "manifests/mysql_cluster_external.yaml")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		By("getting intermediate cluster status")
		Eventually(func() error {
			cluster, err := getMySQLClusterWithNamespace(nsExternal)
			Expect(err).ShouldNot(HaveOccurred())
			if err != nil {
				return err
			}
			if cluster.Status.CurrentPrimaryIndex == nil {
				return errors.New("CurrentPrimaryIndex should be set")
			}
			if cluster.Status.Ready != corev1.ConditionTrue {
				return errors.New("Status.Ready should be true")
			}
			healthy := findCondition(cluster.Status.Conditions, v1alpha1.ConditionHealthy)
			if healthy == nil || healthy.Status != corev1.ConditionTrue {
				return errors.New("Conditions.Healthy should be true")
			}
			return nil
		}, 10*time.Minute).Should(Succeed())

		By("checking data from donor")
		cluster, err := getMySQLClusterWithNamespace(nsExternal)
		Expect(err).ShouldNot(HaveOccurred())
		connector := newMySQLConnector(cluster)
		err = connector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())
		defer connector.stopPortForward()

		replica, err := minIndexReplica(cluster)
		Expect(err).ShouldNot(HaveOccurred())
		var replicaDB *sqlx.DB
		Eventually(func() error {
			replicaDB, err = connector.connect(replica)
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		Eventually(func() error {
			rows, err := replicaDB.Query("SELECT count(*) FROM moco_e2e.replication_test")
			if err != nil {
				return err
			}
			replicatedCount := 0
			for rows.Next() {
				err = rows.Scan(&replicatedCount)
				if err != nil {
					return err
				}
			}
			if replicatedCount != lineCount {
				return fmt.Errorf("replicated: %d", replicatedCount)
			}
			return nil
		}).Should(Succeed())

		var primaryDB *sqlx.DB
		Eventually(func() error {
			primaryDB, err = connector.connectToPrimary()
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		_, err = primaryDB.Exec("CREATE DATABASE moco_e2e_external")
		fmt.Printf("cannot write intermediate primary: err=%v", err)
		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).Should(ContainSubstring("Error 1290: The MySQL server is running with the --super-read-only option so it cannot execute this statement"))
		connector.stopPortForward()

		By("propagating query when writing new data to external donor")
		cluster, err = getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())
		donorConnector := newMySQLConnector(cluster)
		err = donorConnector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())
		defer donorConnector.stopPortForward()

		var donorDB *sqlx.DB
		Eventually(func() error {
			donorDB, err = donorConnector.connectToPrimary()
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())
		_, err = donorDB.Exec("CREATE DATABASE moco_e2e_from_donor")
		Expect(err).ShouldNot(HaveOccurred())
		donorConnector.stopPortForward()

		err = connector.startPortForward()
		Expect(err).ShouldNot(HaveOccurred())
		Eventually(func() error {
			ret, err := primaryDB.Query("SHOW DATABASES LIKE 'moco_e2e_from_donor'")
			if err != nil {
				return err
			}

			if !ret.Next() {
				return errors.New("doesn't propagating query from donor")
			}
			return nil
		}).Should(Succeed())
	})

	It("should stop intermediate primary behavior", func() {
		By("applying manifest without replicationSourceSecretName")
		stdout, stderr, err := execAtLocal("sed", nil, "/replicationSourceSecretName: replication-source-secret/d", "./manifests/mysql_cluster_external.yaml")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=", string(stdout), "stderr=", string(stderr))

		stdout, stderr, err = kubectlWithInput(stdout, "apply", "-n"+nsExternal, "-f", "-")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=", string(stdout), "stderr=", string(stderr))

		By("creating a database on the e2e-test-external MySQL cluster directly")
		cluster, err := getMySQLClusterWithNamespace(nsExternal)
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

		Eventually(func() error {
			_, err = primaryDB.Exec("CREATE DATABASE moco_e2e_external")
			return err
		}, 2*time.Minute).Should(Succeed())

		Eventually(func() error {
			ret, err := primaryDB.Query("SHOW DATABASES LIKE 'moco_e2e_external'")
			if err != nil {
				return err
			}

			if !ret.Next() {
				return errors.New("cannot create a database")
			}
			return nil
		}).Should(Succeed())
	})
}
