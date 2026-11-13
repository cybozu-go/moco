package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"text/template"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func kubectl(input []byte, args ...string) ([]byte, error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd := exec.Command(kubectlCmd, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	return nil, fmt.Errorf("kubectl failed with %s: stderr=%s", err, stderr)
}

func kubectlSafe(input []byte, args ...string) []byte {
	GinkgoHelper()
	out, err := kubectl(input, args...)
	Expect(err).NotTo(HaveOccurred())
	return out
}

func runInPod(args ...string) ([]byte, error) {
	a := append([]string{"exec", "client", "--"}, args...)
	return kubectl(nil, a...)
}

func getCluster(ns, name string) (*mocov1beta2.MySQLCluster, error) {
	out, err := kubectl(nil, "get", "-n", ns, "mysqlcluster", name, "-o", "json")
	if err != nil {
		return nil, err
	}
	cluster := &mocov1beta2.MySQLCluster{}
	err = json.Unmarshal(out, cluster)
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

func getClusterCondition(cluster *mocov1beta2.MySQLCluster, condType string) (metav1.Condition, error) {
	for _, cond := range cluster.Status.Conditions {
		if cond.Type != condType {
			continue
		}
		return cond, nil
	}
	return metav1.Condition{}, fmt.Errorf("no %s condition", condType)
}

func getJobCondition(job *batchv1.Job, condType batchv1.JobConditionType) (batchv1.JobCondition, error) {
	for _, cond := range job.Status.Conditions {
		if cond.Type != condType {
			continue
		}
		return cond, nil
	}
	return batchv1.JobCondition{}, fmt.Errorf("no %s condition", condType)
}

func fillTemplate(tmpl string) []byte {
	return fillTemplateWithVersion(tmpl, mysqlVersion)
}

func fillTemplateWithVersion(tmpl, version string) []byte {
	return fillTemplateWithParams(tmpl, version)
}

func fillTemplateWithParams(tmpl string, params any) []byte {
	t := template.Must(template.New("").Parse(tmpl))
	buf := new(bytes.Buffer)
	err := t.Execute(buf, params)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// checkClusterCondition returns nil if the condition of the MySQLCluster has the given status.
func checkClusterCondition(ns, name, condType string, status metav1.ConditionStatus) error {
	cluster, err := getCluster(ns, name)
	if err != nil {
		return err
	}
	cond, err := getClusterCondition(cluster, condType)
	if err != nil {
		return err
	}
	if cond.Status != status {
		return fmt.Errorf("%s condition of %s/%s is %s, not %s", condType, ns, name, cond.Status, status)
	}
	return nil
}

// waitForClusterCondition waits until the condition of the MySQLCluster has the given status.
func waitForClusterCondition(ns, name, condType string, status metav1.ConditionStatus) {
	GinkgoHelper()
	Eventually(func() error {
		return checkClusterCondition(ns, name, condType, status)
	}).Should(Succeed())
}

// recreateInstance deletes the PVC and the Pod of the instance so that the instance loses its data.
// It returns after a new Pod is scheduled.
func recreateInstance(ns, name string, index int) {
	GinkgoHelper()
	podName := fmt.Sprintf("moco-%s-%d", name, index)
	out := kubectlSafe(nil, "-n", ns, "get", "pod", podName, "-o", "json")
	oldPod := &corev1.Pod{}
	Expect(json.Unmarshal(out, oldPod)).To(Succeed())

	kubectlSafe(nil, "-n", ns, "delete", "--wait=false", "pvc", fmt.Sprintf("mysql-data-%s", podName))
	kubectlSafe(nil, "-n", ns, "delete", "--grace-period=1", "pod", podName)

	Eventually(func() error {
		out, err := kubectl(nil, "-n", ns, "get", "pod", podName, "-o", "json")
		if err != nil {
			return err
		}
		pod := &corev1.Pod{}
		if err := json.Unmarshal(out, pod); err != nil {
			return err
		}
		if pod.UID == oldPod.UID {
			return fmt.Errorf("pod %s is not recreated yet", podName)
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type != corev1.PodScheduled {
				continue
			}
			// The Pod can be unschedulable if it is created before the old PVC is deleted.
			if cond.Reason == corev1.PodReasonUnschedulable {
				if _, err := kubectl(nil, "-n", ns, "delete", "--grace-period=1", "pod", podName); err != nil {
					return fmt.Errorf("failed to delete pod %s: %w", podName, err)
				}
				return fmt.Errorf("pod %s is unschedulable", podName)
			}
			if cond.Status == corev1.ConditionTrue {
				return nil
			}
		}
		return fmt.Errorf("pod %s is not scheduled yet", podName)
	}).Should(Succeed())
}

// deleteNamespace deletes all MySQLClusters in the namespace and then the namespace itself.
func deleteNamespace(ns string) {
	GinkgoHelper()
	out, err := kubectl(nil, "get", "namespace", ns, "--ignore-not-found", "-o", "name")
	Expect(err).NotTo(HaveOccurred())
	if len(bytes.TrimSpace(out)) == 0 {
		return
	}
	kubectlSafe(nil, "delete", "-n", ns, "mysqlclusters", "--all")
	verifyAllPodsDeleted(ns)
	kubectlSafe(nil, "delete", "namespace", ns)
}

func findMetric(mf *dto.MetricFamily, labels map[string]string) *dto.Metric {
OUTER:
	for _, m := range mf.Metric {
		having := make(map[string]string)
		for _, p := range m.Label {
			having[*p.Name] = *p.Value
		}
		for k, v := range labels {
			if having[k] != v {
				continue OUTER
			}
		}
		return m
	}
	return nil
}

// verifyPVCTemplates verifies the labels and storage size of PVC templates in a specific cluster.
// It builds two maps of anticipated PVC labels and sizes based on the cluster specifications,
// Retrieves a StatefulSet using Kubernetes API and compares its PVC templates' labels and sizes
// with the anticipated ones.
func verifyPVCTemplates(ns string, clusterName string) {
	GinkgoHelper()
	cluster, err := getCluster(ns, clusterName)
	Expect(err).NotTo(HaveOccurred())

	wantLabels := make(map[string]map[string]string)
	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		wantLabels[pvc.Name] = pvc.Labels
	}

	wantSizes := make(map[string]*resource.Quantity)
	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		wantSizes[pvc.Name] = pvc.Spec.Resources.Requests.Storage()
	}

	Eventually(func(g Gomega) {
		out, err := kubectl(nil,
			"get", "sts",
			"-n", ns,
			fmt.Sprintf("moco-%s", clusterName),
			"-o", "json",
		)
		g.Expect(err).NotTo(HaveOccurred())

		var sts appsv1.StatefulSet
		g.Expect(json.Unmarshal(out, &sts)).To(Succeed())

		for _, pvc := range sts.Spec.VolumeClaimTemplates {
			g.Expect(wantLabels).To(HaveKey(pvc.Name), "pvc %s is not expected", pvc.Name)
			labels := wantLabels[pvc.Name]
			g.Expect(pvc.ObjectMeta.Labels).To(Equal(labels), "pvc %s labels are not expected", pvc.Name)

			g.Expect(wantSizes).To(HaveKey(pvc.Name), "pvc %s is not expected", pvc.Name)
			want := wantSizes[pvc.Name]
			size := pvc.Spec.Resources.Requests.Storage()
			g.Expect(size.Cmp(*want)).To(BeZero(), "pvc %s: expected size %s, got %s", pvc.Name, want, size)
		}
	}).Should(Succeed())
}

// verifyPVCSize validates the storage size of Persistent Volume Claims (PVC) in a specific cluster.
// It builds a map of anticipated PVC sizes, retrieves the current state of PVCs from the Kubernetes API,
// and assesses if the actual PVC sizes coincide with the predicted sizes.
func verifyPVCSize(ns string, clusterName string) {
	GinkgoHelper()
	cluster, err := getCluster(ns, clusterName)
	Expect(err).NotTo(HaveOccurred())

	wantSizes := make(map[string]*resource.Quantity)
	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		for i := int32(0); i < cluster.Spec.Replicas; i++ {
			name := fmt.Sprintf("%s-moco-%s-%d", pvc.Name, clusterName, i)
			wantSizes[name] = pvc.Spec.Resources.Requests.Storage()
		}
	}

	Eventually(func(g Gomega) {
		out, err := kubectl(nil,
			"get", "pvc",
			"-n", ns,
			"-l", fmt.Sprintf("app.kubernetes.io/instance=%s", clusterName),
			"-o", "json",
		)
		g.Expect(err).NotTo(HaveOccurred())

		var pvcList corev1.PersistentVolumeClaimList
		g.Expect(json.Unmarshal(out, &pvcList)).To(Succeed())
		g.Expect(pvcList.Items).NotTo(BeEmpty(), "no PVCs found")
		g.Expect(pvcList.Items).To(HaveLen(len(wantSizes)), "pvc count is not expected")

		for _, pvc := range pvcList.Items {
			g.Expect(wantSizes).To(HaveKey(pvc.Name), "pvc %s is not expected", pvc.Name)
			want := wantSizes[pvc.Name]
			size := pvc.Spec.Resources.Requests.Storage()
			g.Expect(size.Cmp(*want)).To(BeZero(), "pvc %s: expected size %s, got %s", pvc.Name, want, size)
		}
	}).Should(Succeed())
}

// verifyAllPodsDeleted validates that the namespace has no pods.
func verifyAllPodsDeleted(namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		out, err := kubectl(nil, "get", "-n", namespace, "pod", "-o", "json")
		g.Expect(err).NotTo(HaveOccurred())
		pods := &corev1.PodList{}
		g.Expect(json.Unmarshal(out, pods)).To(Succeed())
		g.Expect(pods.Items).To(BeEmpty(), "namespace %s still has pods", namespace)
	}).Should(Succeed())
}
