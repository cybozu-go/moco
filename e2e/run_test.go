package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"text/template"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
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

func getCluster(ns, name string) (*mocov1beta1.MySQLCluster, error) {
	out, err := kubectl(nil, "get", "-n", ns, "mysqlcluster", name, "-o", "json")
	if err != nil {
		return nil, err
	}
	cluster := &mocov1beta1.MySQLCluster{}
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
