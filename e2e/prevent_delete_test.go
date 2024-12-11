package e2e

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/prevent_delete.yaml
var preventDeleteYAML string

func initializeMySQL() {
	kubectlSafe(fillTemplate(preventDeleteYAML), "apply", "-f", "-")
	Eventually(func() error {
		cluster, err := getCluster("prevent-delete", "test")
		Expect(err).NotTo(HaveOccurred())
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
	time.Sleep(30 * time.Second)

	// wait for primary to be 1
	Eventually(func() int {
		cluster, err := getCluster("prevent-delete", "test")
		Expect(err).NotTo(HaveOccurred())
		if cluster.Status.CurrentPrimaryIndex != 1 {
			kubectlSafe(nil, "moco", "-n", "prevent-delete", "switchover", "test")
			time.Sleep(10 * time.Second)
		}
		return cluster.Status.CurrentPrimaryIndex
	}).Should(Equal(1))

	kubectlSafe(nil, "moco", "mysql", "-n", "prevent-delete", "-u", "moco-writable", "test", "--",
		"-e", "CREATE DATABASE test")
	kubectlSafe(nil, "moco", "mysql", "-n", "prevent-delete", "-u", "moco-writable", "test", "--",
		"-e", "CREATE TABLE test.t (i INT)")
}

func cleanupMySQL() {
	cluster, err := getCluster("prevent-delete", "test")
	Expect(err).NotTo(HaveOccurred())
	primary := cluster.Status.CurrentPrimaryIndex
	for i := 0; i < 3; i++ {
		if i == primary {
			continue
		}
		setSourceDelay(i, 0)
	}
	time.Sleep(10 * time.Second)

	kubectlSafe(nil, "delete", "mysqlclusters", "-n", "prevent-delete", "--all")
	verifyAllPodsDeleted("prevent-delete")
}

func setSourceDelay(index, delay int) {
	kubectlSafe(nil, "moco", "mysql", "-n", "prevent-delete", "-u", "moco-admin", "--index", strconv.Itoa(index), "test", "--", "-e", "STOP REPLICA SQL_THREAD")
	kubectlSafe(nil, "moco", "mysql", "-n", "prevent-delete", "-u", "moco-admin", "--index", strconv.Itoa(index), "test", "--", "-e", fmt.Sprintf("CHANGE REPLICATION SOURCE TO SOURCE_DELAY=%d", delay))
	kubectlSafe(nil, "moco", "mysql", "-n", "prevent-delete", "-u", "moco-admin", "--index", strconv.Itoa(index), "test", "--", "-e", "START REPLICA")
	if delay != 0 {
		kubectlSafe(nil, "moco", "mysql", "-n", "prevent-delete", "-u", "moco-writable", "test", "--", "-e", "INSERT INTO test.t VALUES (1); COMMIT;")
	}
}

var _ = Context("PreventDelete", Serial, func() {
	if doUpgrade {
		return
	}

	BeforeEach(func() {
		initializeMySQL()
	})

	AfterEach(func() {
		cleanupMySQL()
	})

	It("should add or remove prevent-delete annotation by replication delay", func() {
		cluster, err := getCluster("prevent-delete", "test")
		Expect(err).NotTo(HaveOccurred())
		primary := cluster.Status.CurrentPrimaryIndex

		// add prevent-delete annotation and wait for it to be removed
		for i := 0; i < 3; i++ {
			kubectlSafe(nil, "annotate", "pod", "-n", "prevent-delete", cluster.PodName(i), "moco.cybozu.com/prevent-delete=true")
			Eventually(func() error {
				out, err := kubectl(nil, "get", "pod", "-n", "prevent-delete", cluster.PodName(i), "-o", "json")
				Expect(err).NotTo(HaveOccurred())
				pod := &corev1.Pod{}
				err = json.Unmarshal(out, pod)
				Expect(err).NotTo(HaveOccurred())
				if _, exists := pod.Annotations[constants.AnnPreventDelete]; exists {
					return errors.New("annotation is not removed")
				}
				return nil
			}).Should(Succeed())
		}

		// set huge replication delay
		setSourceDelay(0, 10000)

		// wait for prevent-delete annotation to be added
		Eventually(func() error {
			out, err := kubectl(nil, "get", "pod", "-n", "prevent-delete", cluster.PodName(primary), "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			pod := &corev1.Pod{}
			err = json.Unmarshal(out, pod)
			Expect(err).NotTo(HaveOccurred())
			if val, exists := pod.Annotations[constants.AnnPreventDelete]; !exists {
				return errors.New("annotation is not added")
			} else if val != "true" {
				return fmt.Errorf("annotation value is not true: %s", val)
			}
			return nil
		}).Should(Succeed())

		// fail to delete pod with prevent-delete annotation
		_, err = kubectl(nil, "delete", "pod", "-n", "prevent-delete", cluster.PodName(primary))
		Expect(err.Error()).To(ContainSubstring("%s is protected from deletion", cluster.PodName(primary)))

		// resolve replication delay
		setSourceDelay(0, 0)

		// wait for prevent-delete annotation to be removed
		Eventually(func() error {
			out, err := kubectl(nil, "get", "pod", "-n", "prevent-delete", cluster.PodName(primary), "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			pod := &corev1.Pod{}
			err = json.Unmarshal(out, pod)
			Expect(err).NotTo(HaveOccurred())
			if _, exists := pod.Annotations[constants.AnnPreventDelete]; exists {
				return errors.New("annotation is not removed")
			}
			return nil
		}).Should(Succeed())
	})

	It("should not finish rollout restart if replication delay occurs", func() {
		cluster, err := getCluster("prevent-delete", "test")
		Expect(err).NotTo(HaveOccurred())
		primary := cluster.Status.CurrentPrimaryIndex

		// set huge replication delay
		setSourceDelay(0, 10000)

		// wait for prevent-delete annotation to be added
		Eventually(func() error {
			out, err := kubectl(nil, "get", "pod", "-n", "prevent-delete", cluster.PodName(primary), "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			pod := &corev1.Pod{}
			err = json.Unmarshal(out, pod)
			Expect(err).NotTo(HaveOccurred())
			if val, exists := pod.Annotations[constants.AnnPreventDelete]; !exists {
				return errors.New("annotation is not added")
			} else if val != "true" {
				return fmt.Errorf("annotation value is not true: %s", val)
			}
			return nil
		}).Should(Succeed())

		// never finish rollout restart
		kubectlSafe(nil, "rollout", "restart", "sts", "-n", "prevent-delete", "moco-test")
		Consistently(func() error {
			out, err := kubectl(nil, "get", "sts", "-n", "prevent-delete", "moco-test", "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			sts := &appsv1.StatefulSet{}
			err = json.Unmarshal(out, sts)
			Expect(err).NotTo(HaveOccurred())
			if sts.Status.UpdatedReplicas != sts.Status.Replicas {
				return errors.New("rollout restart is not finished")
			}
			return nil
		}, 3*time.Minute).ShouldNot(Succeed())

		// resolve replication delay
		setSourceDelay(0, 0)

		// wait for rollout restart to be finished
		Eventually(func() error {
			out, err := kubectl(nil, "get", "sts", "-n", "prevent-delete", "moco-test", "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			sts := &appsv1.StatefulSet{}
			err = json.Unmarshal(out, sts)
			Expect(err).NotTo(HaveOccurred())
			if sts.Status.UpdatedReplicas != sts.Status.Replicas {
				return errors.New("rollout restart is not finished")
			}
			return nil
		}).Should(Succeed())

		// wait for cluster to be healthy
		Eventually(func() error {
			cluster, err := getCluster("prevent-delete", "test")
			Expect(err).NotTo(HaveOccurred())
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
		time.Sleep(30 * time.Second)
	})

	It("should not finish switchover if replication delay occurs", func() {
		cluster, err := getCluster("prevent-delete", "test")
		Expect(err).NotTo(HaveOccurred())
		primary := cluster.Status.CurrentPrimaryIndex

		// set huge replication delay
		setSourceDelay(0, 10000)

		// wait for prevent-delete annotation to be added
		Eventually(func() error {
			out, err := kubectl(nil, "get", "pod", "-n", "prevent-delete", cluster.PodName(primary), "-o", "json")
			Expect(err).NotTo(HaveOccurred())
			pod := &corev1.Pod{}
			err = json.Unmarshal(out, pod)
			Expect(err).NotTo(HaveOccurred())
			if val, exists := pod.Annotations[constants.AnnPreventDelete]; !exists {
				return errors.New("annotation is not added")
			} else if val != "true" {
				return fmt.Errorf("annotation value is not true: %s", val)
			}
			return nil
		}).Should(Succeed())

		// never finish switchover
		kubectlSafe(nil, "moco", "switchover", "-n", "prevent-delete", "test")
		Consistently(func() error {
			cluster, err := getCluster("prevent-delete", "test")
			Expect(err).NotTo(HaveOccurred())
			if cluster.Status.CurrentPrimaryIndex == primary {
				return errors.New("switchover is not finished")
			}
			return nil
		}, 1*time.Minute).ShouldNot(Succeed())

		// resolve replication delay
		setSourceDelay(0, 0)

		// wait for switchover to be finished
		Eventually(func() error {
			cluster, err := getCluster("prevent-delete", "test")
			Expect(err).NotTo(HaveOccurred())
			if cluster.Status.CurrentPrimaryIndex == primary {
				return errors.New("switchover is not finished")
			}
			return nil
		}).Should(Succeed())

		// wait for cluster to be healthy
		Eventually(func() error {
			cluster, err := getCluster("prevent-delete", "test")
			Expect(err).NotTo(HaveOccurred())
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
		time.Sleep(30 * time.Second)
	})
})
