package e2e

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/expfmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
		cluster, err := getCluster("pvc", "cluster")
		Expect(err).NotTo(HaveOccurred())

		wantLabels := make(map[string]map[string]string)
		for _, pvc := range cluster.Spec.VolumeClaimTemplates {
			wantLabels[pvc.Name] = pvc.ObjectMeta.Labels
		}

		wantSizes := make(map[string]*resource.Quantity)
		for _, pvc := range cluster.Spec.VolumeClaimTemplates {
			wantSizes[pvc.Name] = pvc.Spec.Resources.Requests.Storage()
		}

		Eventually(func() error {
			out, err := kubectl(nil,
				"get", "sts",
				"-n", "pvc",
				"moco-cluster",
				"-o", "json",
			)
			if err != nil {
				return err
			}

			var sts appsv1.StatefulSet
			if err := json.Unmarshal(out, &sts); err != nil {
				return err
			}

			for _, pvc := range sts.Spec.VolumeClaimTemplates {
				labels, ok := wantLabels[pvc.Name]
				if !ok {
					return fmt.Errorf("pvc %s is not expected", pvc.Name)
				}

				if !reflect.DeepEqual(pvc.ObjectMeta.Labels, labels) {
					return fmt.Errorf("pvc %s labels are not expected", pvc.Name)
				}

				want, ok := wantSizes[pvc.Name]
				if !ok {
					return fmt.Errorf("pvc %s is not expected", pvc.Name)
				}

				if pvc.Spec.Resources.Requests.Storage().Cmp(*want) != 0 {
					return fmt.Errorf("pvc %s is not expected size: %s", pvc.Name, pvc.Spec.Resources.Requests.Storage())
				}
			}

			return nil
		}).Should(Succeed())
	})

	It("should pvc resized", func() {
		cluster, err := getCluster("pvc", "cluster")
		Expect(err).NotTo(HaveOccurred())

		wantSizes := make(map[string]*resource.Quantity)
		for _, pvc := range cluster.Spec.VolumeClaimTemplates {
			for i := int32(0); i < cluster.Spec.Replicas; i++ {
				name := fmt.Sprintf("%s-%s-%d", pvc.Name, "moco-cluster", i)
				wantSizes[name] = pvc.Spec.Resources.Requests.Storage()
			}
		}

		Eventually(func() error {
			out, err := kubectl(nil,
				"get", "pvc",
				"-n", "pvc",
				"-l", "app.kubernetes.io/instance=cluster",
				"-o", "json",
			)
			if err != nil {
				return err
			}

			var pvcList corev1.PersistentVolumeClaimList
			if err := json.Unmarshal(out, &pvcList); err != nil {
				return err
			}
			if len(pvcList.Items) < 1 {
				return errors.New("not found pvcs")
			}

			if len(pvcList.Items) != len(wantSizes) {
				return fmt.Errorf("pvc count is not expected: %d", len(pvcList.Items))
			}

			for _, pvc := range pvcList.Items {
				want, ok := wantSizes[pvc.Name]
				if !ok {
					return fmt.Errorf("pvc %s is not expected", pvc.Name)
				}

				if pvc.Spec.Resources.Requests.Storage().Cmp(*want) != 0 {
					return fmt.Errorf("pvc %s is not expected size: %s", pvc.Name, pvc.Spec.Resources.Requests.Storage())
				}
			}

			return nil
		}).Should(Succeed())
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
		Expect(stsMetric.GetCounter().GetValue()).To(BeNumerically("==", 1))
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
