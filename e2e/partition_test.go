package e2e

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"text/template"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
)

//go:embed testdata/partition.yaml
var partitionTestYAML string

//go:embed testdata/partition_changed.yaml
var partitionApplyYAML string

//go:embed testdata/partition_force_rollingupdate.yaml
var forceRollingUpdateApplyYAML string

//go:embed testdata/partition_image_pull_backoff.yaml
var imagePullBackoffApplyYAML string

//go:embed testdata/partition_volume_template.yaml
var volumeTemplateApplyYAML string

// Each context below gets its own namespace and cluster so that the chains can
// run on different ginkgo processes.

func fillPartitionTemplate(tmpl, ns string) []byte {
	t := template.Must(template.New("").Parse(tmpl))
	buf := new(bytes.Buffer)
	err := t.Execute(buf, struct {
		Namespace    string
		MySQLVersion string
	}{Namespace: ns, MySQLVersion: mysqlVersion})
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func partitionClusterHealthy(ns string) error {
	cluster, err := getCluster(ns, "test")
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
}

func setupPartitionCluster(ns string) {
	GinkgoWriter.Println("should construct a cluster")
	kubectlSafe(fillPartitionTemplate(partitionTestYAML, ns), "apply", "-f", "-")
	Eventually(func() error {
		return partitionClusterHealthy(ns)
	}).Should(Succeed())

	kubectlSafe(nil, "moco", "-n", ns, "mysql", "-u", "moco-writable", "test", "--",
		"-e", "CREATE DATABASE test")

	DeferCleanup(func() {
		GinkgoWriter.Println("should delete clusters")
		kubectlSafe(nil, "delete", "-n", ns, "mysqlclusters", "--all")
		verifyAllPodsDeleted(ns)
	})
}

var _ = Context("partition_test: pod template change", Ordered, SpecPriority(100), func() {
	if doUpgrade {
		return
	}
	const ns = "partition-pod"

	BeforeAll(func() {
		setupPartitionCluster(ns)
	})

	It("should pod template change succeed", func() {
		// cpu request 1m -> 2m
		kubectlSafe(fillPartitionTemplate(partitionApplyYAML, ns), "apply", "-f", "-")
		Eventually(func() error {
			out, err := kubectl(nil, "get", "-n", ns, "pod", "-o", "json")
			if err != nil {
				return err
			}
			pods := &corev1.PodList{}
			if err := json.Unmarshal(out, pods); err != nil {
				return err
			}

			for _, pod := range pods.Items {
				for _, c := range pod.Spec.Containers {
					if c.Name != "mysqld" {
						continue
					}
					if c.Resources.Requests.Cpu().Cmp(resource.MustParse("2m")) != 0 {
						return fmt.Errorf("pod %s is not changed", pod.Name)
					}
				}
			}

			return partitionClusterHealthy(ns)
		}).WithTimeout(time.Minute * 10).Should(Succeed())
	})

	It("should partition changes succeed", func() {
		out, err := kubectl(nil, "get", "-n", ns, "event", "-o", "json")
		Expect(err).NotTo(HaveOccurred())
		events := &corev1.EventList{}
		err = json.Unmarshal(out, events)
		Expect(err).NotTo(HaveOccurred())

		partitionEvents := []corev1.Event{}
		for _, event := range events.Items {
			if event.Reason == "PartitionUpdate" {
				partitionEvents = append(partitionEvents, event)
			}
		}

		sort.Slice(partitionEvents, func(i, j int) bool {
			return partitionEvents[i].CreationTimestamp.Before(&partitionEvents[j].CreationTimestamp)
		})
		Expect(partitionEvents).To(HaveLen(3))
		Expect(partitionEvents[0].Message).To(Equal("Updated partition from 3 to 2"))
		Expect(partitionEvents[1].Message).To(Equal("Updated partition from 2 to 1"))
		Expect(partitionEvents[2].Message).To(Equal("Updated partition from 1 to 0"))
	})

	It("metrics", func() {
		out := kubectlSafe(nil, "-n", "moco-system", "get", "pods", "-l", "app.kubernetes.io/component=moco-controller", "-o", "json")
		pods := &corev1.PodList{}
		err := json.Unmarshal(out, pods)
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).To(HaveLen(1))
		addr := pods.Items[0].Status.PodIP
		out, err = runInPod("curl", "-sf", fmt.Sprintf("http://%s:8080/metrics", addr))
		Expect(err).NotTo(HaveOccurred())

		mfs, err := newTextParser().TextToMetricFamilies(bytes.NewReader(out))
		Expect(err).NotTo(HaveOccurred())

		stsOut, err := kubectl(nil, "get", "-n", ns, "statefulset", "moco-test", "-o", "json")
		Expect(err).NotTo(HaveOccurred())
		sts := &appsv1.StatefulSet{}
		err = json.Unmarshal(stsOut, sts)
		Expect(err).NotTo(HaveOccurred())

		curReplicasMf := mfs["moco_cluster_current_replicas"]
		Expect(curReplicasMf).NotTo(BeNil())
		curReplicasMetric := findMetric(curReplicasMf, map[string]string{"namespace": ns, "name": "test"})
		Expect(curReplicasMetric).NotTo(BeNil())
		Expect(curReplicasMetric.GetGauge().GetValue()).To(BeNumerically("==", float64(sts.Status.CurrentReplicas)))

		updatedReplicasMf := mfs["moco_cluster_updated_replicas"]
		Expect(updatedReplicasMf).NotTo(BeNil())
		updatedReplicasMetric := findMetric(updatedReplicasMf, map[string]string{"namespace": ns, "name": "test"})
		Expect(updatedReplicasMetric).NotTo(BeNil())
		Expect(updatedReplicasMetric.GetGauge().GetValue()).To(BeNumerically("==", float64(sts.Status.UpdatedReplicas)))

		partitionUpdatedMf := mfs["moco_cluster_last_partition_updated"]
		Expect(partitionUpdatedMf).NotTo(BeNil())
		partitionUpdatedMetric := findMetric(partitionUpdatedMf, map[string]string{"namespace": ns, "name": "test"})
		Expect(partitionUpdatedMetric).NotTo(BeNil())
		Expect(partitionUpdatedMetric.GetGauge().GetValue()).To(BeNumerically(">", 0))

		// This metric does not exist in this namespace because retries are not performed.
		retryPartitionUpdateMf := mfs["moco_cluster_partition_update_retries_total"]
		if retryPartitionUpdateMf != nil {
			Expect(findMetric(retryPartitionUpdateMf, map[string]string{"namespace": ns})).To(BeNil())
		}
	})
})

var _ = Context("partition_test: volume template change", Ordered, SpecPriority(100), func() {
	if doUpgrade {
		return
	}
	const ns = "partition-volume"

	BeforeAll(func() {
		setupPartitionCluster(ns)
	})

	It("should volume template change succeed", func() {
		// add labels to the volume template and pods.
		kubectlSafe(fillPartitionTemplate(volumeTemplateApplyYAML, ns), "apply", "-f", "-")
		Eventually(func() error {
			out, err := kubectl(nil, "get", "-n", ns, "pod", "-o", "json")
			if err != nil {
				return err
			}
			pods := &corev1.PodList{}
			if err := json.Unmarshal(out, pods); err != nil {
				return err
			}

			for _, pod := range pods.Items {
				_, ok := pod.Labels["foo"]
				if !ok {
					return fmt.Errorf("pod %s is not changed", pod.Name)
				}
			}

			return partitionClusterHealthy(ns)
		}).WithTimeout(time.Minute * 10).Should(Succeed())
	})

	It("should recreate statefulset", func() {
		out, err := kubectl(nil, "get", "-n", ns, "statefulset", "moco-test", "-o", "json")
		Expect(err).NotTo(HaveOccurred())
		sts := &appsv1.StatefulSet{}
		err = json.Unmarshal(out, sts)
		Expect(err).NotTo(HaveOccurred())
		Expect(sts.Spec.UpdateStrategy.RollingUpdate).NotTo(BeNil())
		Expect(*sts.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(int32(0)))
		Expect(sts.Spec.VolumeClaimTemplates[0].Labels["foo"]).To(Equal("bar"))
	})
})

var _ = Context("partition_test: image pull backoff", Ordered, SpecPriority(100), func() {
	if doUpgrade {
		return
	}
	const ns = "partition-backoff"

	BeforeAll(func() {
		setupPartitionCluster(ns)
	})

	It("should image pull backoff", func() {
		kubectlSafe(fillPartitionTemplate(imagePullBackoffApplyYAML, ns), "apply", "-f", "-")
		Eventually(func() error {
			out, err := kubectl(nil, "get", "-n", ns, "pod", "moco-test-2", "-o", "json")
			if err != nil {
				return err
			}
			pod := &corev1.Pod{}
			if err := json.Unmarshal(out, pod); err != nil {
				return err
			}

			status := make([]corev1.ContainerStatus, 0, len(pod.Status.ContainerStatuses)+len(pod.Status.InitContainerStatuses))
			status = append(status, pod.Status.ContainerStatuses...)
			status = append(status, pod.Status.InitContainerStatuses...)

			for _, s := range status {
				if s.Image != "ghcr.io/cybozu-go/moco/mysql:invalid-image" {
					continue
				}
				if s.State.Waiting != nil && s.State.Waiting.Reason == "ImagePullBackOff" {
					return nil
				}
			}

			return errors.New("image pull backoff Pod not found")
		}).Should(Succeed())
	})

	It("should partition updates have stopped", func() {
		out, err := kubectl(nil, "get", "-n", ns, "statefulset", "moco-test", "-o", "json")
		Expect(err).NotTo(HaveOccurred())
		sts := &appsv1.StatefulSet{}
		err = json.Unmarshal(out, sts)
		Expect(err).NotTo(HaveOccurred())
		Expect(sts.Spec.UpdateStrategy.RollingUpdate).NotTo(BeNil())
		Expect(sts.Spec.UpdateStrategy.RollingUpdate.Partition).NotTo(BeNil())
		Expect(*sts.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(int32(2)))
	})

	It("should rollback succeed", func() {
		kubectlSafe(fillPartitionTemplate(partitionApplyYAML, ns), "apply", "-f", "-")
		Eventually(func() error {
			return partitionClusterHealthy(ns)
		}).Should(Succeed())
	})

	It("should partition updates succeed", func() {
		Eventually(func() error {
			out, err := kubectl(nil, "get", "-n", ns, "statefulset", "moco-test", "-o", "json")
			if err != nil {
				return err
			}
			sts := &appsv1.StatefulSet{}
			if err := json.Unmarshal(out, sts); err != nil {
				return err
			}
			if sts.Spec.UpdateStrategy.RollingUpdate == nil || sts.Spec.UpdateStrategy.RollingUpdate.Partition == nil {
				return errors.New("partition is nil")
			}
			if *sts.Spec.UpdateStrategy.RollingUpdate.Partition == int32(0) {
				return nil
			}
			return errors.New("partition is not 0")
		}).Should(Succeed())
	})
})

var _ = Context("partition_test: force rolling update", Ordered, SpecPriority(100), func() {
	if doUpgrade {
		return
	}
	const ns = "partition-force"

	BeforeAll(func() {
		setupPartitionCluster(ns)
	})

	It("should not start rollout when mysql cluster is unhealthy", func() {
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, time.Minute*10)

		getPartition := func() (int32, error) {
			out, err := kubectl(nil, "get", "-n", ns, "statefulset", "moco-test", "-o", "json")
			if err != nil {
				return -1, err
			}
			sts := &appsv1.StatefulSet{}
			if err := json.Unmarshal(out, sts); err != nil {
				return -1, err
			}
			if sts.Spec.UpdateStrategy.RollingUpdate == nil || sts.Spec.UpdateStrategy.RollingUpdate.Partition == nil {
				return -1, errors.New("partition is nil")
			}
			return *sts.Spec.UpdateStrategy.RollingUpdate.Partition, nil
		}

		// The partition starts at the number of replicas on StatefulSet
		// creation. Wait for the initial rollout to settle at 0 before making
		// the cluster unhealthy: the partition controller stops updating the
		// partition while the cluster is unhealthy, and the partition of 3
		// asserted below must be the result of the template change, not a
		// leftover of the creation.
		Eventually(func() error {
			partition, err := getPartition()
			if err != nil {
				return err
			}
			if partition != 0 {
				return fmt.Errorf("partition is %d, not 0", partition)
			}
			return nil
		}).Should(Succeed())

		// Keep killing mysqld on pod 0 so that the cluster stays unhealthy
		// during this spec. Errors are ignored because the pod may be
		// restarting when kill is attempted.
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_ = wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
				_, _ = kubectl(nil, "exec", "-n", ns, "moco-test-0", "-c", "mysqld", "--", "kill", "1")
				return false, nil
			})
		}()
		// Stop the loop and wait for the last kill to finish, so that the
		// kills cannot leak into the next spec.
		defer func() {
			cancel()
			<-done
		}()

		Eventually(func() error {
			cluster, err := getCluster(ns, "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				// Confirming that the cluster becomes unhealthy
				if cond.Status == metav1.ConditionFalse {
					return nil
				}
				return fmt.Errorf("cluster is healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())

		// Change the pod template (cpu request 1m -> 2m) while the cluster is
		// unhealthy. The webhook resets the partition to the number of replicas
		// when the controller applies the new template.
		kubectlSafe(fillPartitionTemplate(partitionApplyYAML, ns), "apply", "-f", "-")
		Eventually(func() error {
			partition, err := getPartition()
			if err != nil {
				return err
			}
			if partition != 3 {
				return fmt.Errorf("partition is %d, not 3", partition)
			}
			return nil
		}).Should(Succeed())

		// The rollout must not proceed while the cluster is unhealthy.
		Consistently(func() error {
			partition, err := getPartition()
			if err != nil {
				return err
			}
			if partition != 3 {
				return fmt.Errorf("partition is %d, not 3", partition)
			}
			return nil
		}).WithTimeout(1 * time.Minute).WithPolling(time.Second).Should(Succeed())
	})

	It("should pod template change succeed with force rolling update", func() {
		kubectlSafe(fillPartitionTemplate(forceRollingUpdateApplyYAML, ns), "apply", "-f", "-")
		// The timeout is longer than the default because the cluster has to
		// recover from the repeated mysqld kills of the previous spec.
		Eventually(func() error {
			return partitionClusterHealthy(ns)
		}).WithTimeout(10 * time.Minute).Should(Succeed())
	})

	It("should partition removed", func() {
		out, err := kubectl(nil, "get", "-n", ns, "statefulset", "moco-test", "-o", "json")
		Expect(err).NotTo(HaveOccurred())
		sts := &appsv1.StatefulSet{}
		err = json.Unmarshal(out, sts)
		Expect(err).NotTo(HaveOccurred())
		Expect(sts.Spec.UpdateStrategy.RollingUpdate).To(BeNil())
	})
})
