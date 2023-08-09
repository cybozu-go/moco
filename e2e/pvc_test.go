package e2e

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/expfmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/pvc_test.yaml
var pvcTestYAML string

//go:embed testdata/pvc_test_changed.yaml
var pvcApplyYAML string

var _ = Context("pvc_test", func() {

	It("should construct a cluster", func() {
		kubectlSafe(fillTemplate(pvcTestYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("pvc", "cluster")
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
	})

	It("should pvc template change succeed", func() {
		// 500Mi -> 1Gi
		kubectlSafe(fillTemplate(pvcApplyYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("pvc", "cluster")
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
	})

	It("should statefulset re-created", func() {
		comparePVCTemplate("pvc", "cluster")
	})

	It("should pvc resized", func() {
		comparePVCSize("pvc", "cluster")
	})

	It("should pvc template storage size reduce succeed", func() {
		// 1Gi -> 500Mi
		kubectlSafe(fillTemplate(pvcTestYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("pvc", "cluster")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("should statefulset re-created", func() {
		comparePVCTemplate("pvc", "cluster")
	})

	It("should volume size reduce succeed", func() {
		out := kubectlSafe(nil, "get", "pods", "-n", "pvc", "-o", "json")
		pods := &corev1.PodList{}
		err := json.Unmarshal(out, pods)
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).To(HaveLen(3))

		for _, pod := range pods.Items {
			kubectlSafe(nil, "delete", "pvc", "-n", "pvc", "--wait=false", fmt.Sprintf("mysql-data-%s", pod.Name))
			kubectlSafe(nil, "delete", "pod", "-n", "pvc", "--grace-period=1", pod.Name)

			Eventually(func() error {
				cluster, err := getCluster("pvc", "cluster")
				if err != nil {
					return err
				}

				for _, cond := range cluster.Status.Conditions {
					if cond.Type != mocov1beta2.ConditionHealthy {
						continue
					}
					if cond.Status == corev1.ConditionTrue {
						return nil
					}
					return fmt.Errorf("cluster is not healthy: %s", cond.Status)
				}
				return errors.New("no health condition")
			}).Should(Succeed())
		}
	})

	It("should pvc resized", func() {
		comparePVCSize("pvc", "cluster")
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

		mfs, err := (&expfmt.TextParser{}).TextToMetricFamilies(bytes.NewReader(out))
		Expect(err).NotTo(HaveOccurred())

		volumeMf := mfs["moco_cluster_volume_resized_total"]
		Expect(volumeMf).NotTo(BeNil())
		volumeMetric := findMetric(volumeMf, map[string]string{"namespace": "pvc", "name": "cluster"})
		Expect(volumeMetric).NotTo(BeNil())
		Expect(volumeMetric.GetCounter().GetValue()).To(BeNumerically("==", 1))

		stsMf := mfs["moco_cluster_statefulset_recreate_total"]
		Expect(stsMf).NotTo(BeNil())
		stsMetric := findMetric(stsMf, map[string]string{"namespace": "pvc", "name": "cluster"})
		Expect(stsMetric).NotTo(BeNil())
		Expect(stsMetric.GetCounter().GetValue()).To(BeNumerically("==", 2))
	})

	It("should delete clusters", func() {
		kubectlSafe(nil, "delete", "-n", "pvc", "mysqlclusters", "--all")

		Eventually(func() error {
			out, err := kubectl(nil, "get", "-n", "pvc", "pod", "-o", "json")
			if err != nil {
				return err
			}
			pods := &corev1.PodList{}
			if err := json.Unmarshal(out, pods); err != nil {
				return err
			}
			if len(pods.Items) > 0 {
				return errors.New("wait until all Pods are deleted")
			}
			return nil
		}).Should(Succeed())
	})
})
