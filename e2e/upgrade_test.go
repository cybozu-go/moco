package e2e

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/upgrade.yaml
var upgradeYAML string

const (
	mysqlVersionOld = "8.0.28"
	mysqlVersionNew = "8.4.8"
)

var _ = Context("upgrade", Ordered, func() {
	if !doUpgrade {
		return
	}

	BeforeAll(func() {
		GinkgoWriter.Println("creating a 5-instance cluster with MySQL " + mysqlVersionOld)
		kubectlSafe(fillTemplateWithVersion(upgradeYAML, mysqlVersionOld), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("upgrade", "test")
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

		DeferCleanup(func() {
			GinkgoWriter.Println("delete clusters")
			kubectlSafe(nil, "delete", "-n", "upgrade", "mysqlclusters", "--all")
			verifyAllPodsDeleted("upgrade")
		})
	})

	It("should upgrade MySQL successfully", func() {
		By("doing a failover")
		kubectlSafe(nil, "moco", "-n", "upgrade", "mysql", "-u", "moco-writable", "test", "--",
			"-e", "CREATE DATABASE test")
		kubectlSafe(nil, "moco", "-n", "upgrade", "mysql", "-u", "moco-writable", "test", "--",
			"-D", "test", "-e", "CREATE TABLE t (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL) ENGINE=InnoDB")
		kubectlSafe(nil, "moco", "-n", "upgrade", "mysql", "-u", "moco-writable", "test", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('aaa'), ('bbb'), ('ccc')")
		kubectlSafe(nil, "delete", "-n", "upgrade", "--wait=false", "--grace-period=1", "pod", "moco-test-0")
		var cluster *mocov1beta2.MySQLCluster
		Eventually(func() error {
			var err error
			cluster, err = getCluster("upgrade", "test")
			if err != nil {
				return err
			}
			if cluster.Status.CurrentPrimaryIndex == 0 {
				return errors.New("a failover has not happen")
			}
			return nil
		}).Should(Succeed())
		primary := cluster.Status.CurrentPrimaryIndex
		fmt.Printf("The current primary = %d\n", primary)

		By("doing a switchover")
		kubectlSafe(nil, "moco", "-n", "upgrade", "switchover", "test")
		Eventually(func() error {
			var err error
			cluster, err = getCluster("upgrade", "test")
			if err != nil {
				return err
			}
			if cluster.Status.CurrentPrimaryIndex == primary {
				return errors.New("a switchover has not happen")
			}
			return nil
		}).Should(Succeed())
		primary = cluster.Status.CurrentPrimaryIndex
		fmt.Printf("The current primary = %d\n", primary)
		Eventually(func() error {
			cluster, err := getCluster("upgrade", "test")
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

		By("upgrading the cluster to MySQL " + mysqlVersionNew)
		kubectlSafe(fillTemplateWithVersion(upgradeYAML, mysqlVersionNew), "apply", "-f", "-")
		Eventually(func() error {
			out, err := kubectl(nil, "-n", "upgrade", "get", "pods", "-o", "json")
			if err != nil {
				return err
			}

			pods := &corev1.PodList{}
			err = json.Unmarshal(out, pods)
			if err != nil {
				return err
			}

			for _, pod := range pods.Items {
				for _, c := range pod.Spec.Containers {
					if c.Name != "mysqld" {
						continue
					}
					if !strings.HasSuffix(c.Image, mysqlVersionNew) {
						return fmt.Errorf("pod %s is not updated yet", pod.Name)
					}
				}
			}
			return nil
		}, 1200).Should(Succeed())
		cluster, err := getCluster("upgrade", "test")
		Expect(err).NotTo(HaveOccurred())
		Expect(cluster.Status.CurrentPrimaryIndex).To(Equal(1))

		Eventually(func() error {
			cluster, err := getCluster("upgrade", "test")
			if err != nil {
				return err
			}
			if cluster.Generation != cluster.Status.ReconcileInfo.Generation {
				return fmt.Errorf("cluster is not reconciled yet")
			}
			conditionReconcileSuccess := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionReconcileSuccess)
			conditionStatefulSetReady := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionStatefulSetReady)
			conditionHealthy := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionHealthy)
			if conditionReconcileSuccess == nil {
				return fmt.Errorf("reconcile failed")
			}
			if conditionStatefulSetReady == nil {
				return fmt.Errorf("statefulset is not ready")
			}
			if conditionHealthy == nil {
				return fmt.Errorf("cluster is not healthy")
			}
			if conditionReconcileSuccess.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition ReconcileSuccess is not expected")
			}
			if conditionStatefulSetReady.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition StatefulSetReady is not expected")
			}
			if conditionHealthy.Status != metav1.ConditionTrue {
				return fmt.Errorf("condition Healthy is not expected")
			}
			return nil
		}).Should(Succeed())
	})
})
