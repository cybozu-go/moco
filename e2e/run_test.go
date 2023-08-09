package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"text/template"

	corev1 "k8s.io/api/core/v1"

	appsv1 "k8s.io/api/apps/v1"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/api/resource"
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
	out, err := kubectl(input, args...)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
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

func fillTemplate(tmpl string) []byte {
	return fillTemplateWithVersion(tmpl, mysqlVersion)
}

func fillTemplateWithVersion(tmpl, version string) []byte {
	t := template.Must(template.New("").Parse(tmpl))
	buf := new(bytes.Buffer)
	err := t.Execute(buf, version)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
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

func comparePVCTemplate(ns string, clusterName string) {
	cluster, err := getCluster(ns, clusterName)
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
			"-n", ns,
			fmt.Sprintf("moco-%s", clusterName),
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
}

func comparePVCSize(ns string, clusterName string) {
	cluster, err := getCluster(ns, clusterName)
	Expect(err).NotTo(HaveOccurred())

	wantSizes := make(map[string]*resource.Quantity)
	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		for i := int32(0); i < cluster.Spec.Replicas; i++ {
			name := fmt.Sprintf("%s-moco-%s-%d", pvc.Name, clusterName, i)
			wantSizes[name] = pvc.Spec.Resources.Requests.Storage()
		}
	}

	Eventually(func() error {
		out, err := kubectl(nil,
			"get", "pvc",
			"-n", ns,
			"-l", fmt.Sprintf("app.kubernetes.io/instance=%s", clusterName),
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
}
