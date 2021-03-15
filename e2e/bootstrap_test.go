package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func prepareBooptstrap() {
	It("should create moco-controller", func() {
		if doUpgrade {
			By("applying moco-controller (latest)")
			applyControllerManifests("./manifests/controller_latest")
		} else {
			By("applying moco-controller (develop)")
			applyControllerManifests("./manifests/controller_develop")
		}

		By("waiting moco-controller")
		waitController()
	})

	It("should register MySQLCluster", func() {
		_, _, _ = kubectl("create", "ns", "e2e-test") // ignore error
		stdout, stderr, err := kubectl("apply", "-n", "e2e-test", "-f", "manifests/mysql_cluster.yaml")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		By("waiting MySQLCluster")
		waitCluster()
	})

	if doUpgrade {
		It("should upgrade moco-controller to develop", func() {
			By("applying moco-controller (develop)")
			applyControllerManifests("./manifests/controller_develop")

			By("waiting moco-controller")
			waitController()

			// Add upgrade logic here.
			// NOTE: Please renew this upgrade test, when MOCO v1 is released.
			// The purpose of this test is to check the impact on the existing MySQLCluster during the development period (MOCO v0.X).
			// After MOCO v1 and later, the upgrade test needs to be changed. Please see the design document (TBD).

			By("waiting MySQLCluster")
			waitCluster()
		})
	}
}

func applyControllerManifests(path string) {
	stdout, stderr, err := kustomize(path)
	Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)
	stdout, stderr, err = kubectlWithInput(stdout, "apply", "-f", "-")
	Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)
}

func waitController() {
	Eventually(func() error {
		st := time.Now()
		for {
			// Wait for 15 seconds. After the upgrade of controller, it will be temporarily down.
			if time.Since(st) > 15*time.Second {
				return nil
			}

			stdout, _, err := kubectl("-n", "moco-system", "get", "deployment/moco-controller-manager", "-o=json")
			if err != nil {
				return err
			}
			deployment := new(appsv1.Deployment)
			err = json.Unmarshal(stdout, deployment)
			if err != nil {
				return err
			}

			if int(deployment.Status.AvailableReplicas) != 1 {
				return fmt.Errorf("AvailableReplicas is not 1: %d", int(deployment.Status.AvailableReplicas))
			}
			time.Sleep(1 * time.Second)
		}
	}, 3*time.Minute).Should(Succeed())
}

func waitCluster() {
	Eventually(func() error {
		st := time.Now()
		for {
			if time.Since(st) > 35*time.Second {
				return nil
			}

			cluster, err := getMySQLCluster()
			if err != nil {
				return err
			}
			if cluster.Status.CurrentPrimaryIndex == nil {
				return errors.New("CurrentPrimaryIndex should be set")
			}
			if cluster.Status.Ready != corev1.ConditionTrue {
				return errors.New("Status.Ready should be true")
			}
			available := findCondition(cluster.Status.Conditions, v1alpha1.ConditionAvailable)
			if available == nil || available.Status != corev1.ConditionTrue {
				return errors.New("Conditions.Available should be true")
			}
			healthy := findCondition(cluster.Status.Conditions, v1alpha1.ConditionHealthy)
			if healthy == nil || healthy.Status != corev1.ConditionTrue {
				return errors.New("Conditions.Healthy should be true")
			}
			initialized := findCondition(cluster.Status.Conditions, v1alpha1.ConditionInitialized)
			if initialized == nil || initialized.Status != corev1.ConditionTrue {
				return errors.New("Conditions.Initialized should be true")
			}
		}
	}, 3*time.Minute).Should(Succeed())
}

func testBootstrap() {
	It("should create cluster", func() {
		By("getting Secret which contains root password")
		Eventually(func() error {
			cluster, err := getMySQLCluster()
			if err != nil {
				return err
			}
			_, err = getPasswordSecret(cluster)
			if err != nil {
				return err
			}
			return nil
		}).Should(Succeed())

		By("getting StatefulSet")
		Eventually(func() error {
			cluster, err := getMySQLCluster()
			if err != nil {
				return err
			}

			stdout, stderr, err := kubectl("get", "-n", "e2e-test", "statefulsets", moco.UniqueName(cluster), "-o", "json")
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
		}).Should(Succeed())
	})

	It("should record events", func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())
		stdout, stderr, err := kubectl("get", "-n", "e2e-test", "events", "--field-selector", "involvedObject.name="+cluster.Name, "-o", "json")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		var events corev1.EventList
		err = json.Unmarshal(stdout, &events)
		Expect(err).ShouldNot(HaveOccurred())

		initialized := false
		completed := false
		for _, e := range events.Items {
			if equalEvent(e, moco.EventInitializationSucceeded) {
				initialized = true
			}
			if equalEvent(e, moco.EventClusteringCompletedSynced) {
				completed = true
			}
		}
		Expect(initialized).Should(BeTrue())
		Expect(completed).Should(BeTrue())
	})

	It("should replicate data", func() {
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

		err = insertData(primaryDB, lineCount)
		Expect(err).ShouldNot(HaveOccurred())

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
	})
}

func equalEvent(actual corev1.Event, expected moco.MOCOEvent) bool {
	return actual.Reason == expected.Reason && actual.Type == expected.Type && actual.Message == expected.Message
}
