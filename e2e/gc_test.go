package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cybozu-go/moco"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

func testGarbageCollector() {
	It("should remove all resources", func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())

		stdout, stderr, err := kubectl("delete", "-n", "e2e-test", "-f", "manifests/mysql_cluster.yaml")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		for _, kind := range []string{"configmaps", "service", "cronjobs", "jobs", "statefulsets", "pods"} {
			Eventually(func() error {
				stdout, stderr, err := kubectl("get", "-n", "e2e-test", kind)
				if err != nil {
					return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
				}
				if !strings.Contains(string(stderr), "No resources found in e2e-test namespace.") {
					return fmt.Errorf("resources remain: %s, stdout: %s, stderr: %s", kind, stdout, stderr)
				}
				return nil
			}, 2*time.Minute).Should(Succeed())
		}

		for _, resource := range []string{
			"serviceaccount/mysqld-sa-" + moco.UniqueName(cluster),
			"secret/root-password-" + moco.UniqueName(cluster),
		} {
			Eventually(func() error {
				stdout, stderr, err := kubectl("get", "-n", "e2e-test", resource)
				if err == nil {
					return fmt.Errorf("%s should be removed. stdout: %s, stderr: %s", resource, stdout, stderr)
				}
				if !strings.Contains(string(stderr), "not found") {
					return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
				}
				return nil
			}).Should(Succeed())
		}

		Eventually(func() error {
			stdout, stderr, err := kubectl("get", "-n", "moco-system", "secret/e2e-test.mysqlcluster")
			if err == nil {
				return fmt.Errorf("secret/e2e-test.mysqlcluster should be removed. stdout: %s, stderr: %s", stdout, stderr)
			}
			if !strings.Contains(string(stderr), "not found") {
				return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			return nil
		}).Should(Succeed())

		var pvcNames []string
		for i := 0; i < int(cluster.Spec.Replicas); i++ {
			podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), i)
			pvcName := fmt.Sprintf("mysql-data-%s", podName)
			pvcNames = append(pvcNames, pvcName)
		}

		var pvNames []string
		Consistently(func() error {
			pvNames = make([]string, 0)
			for _, pvcName := range pvcNames {
				stdout, stderr, err := kubectl("get", "-n", "e2e-test", "pvc", pvcName, "-o", "json")
				if err != nil {
					return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
				}
				var pvc corev1.PersistentVolumeClaim
				err = json.Unmarshal(stdout, &pvc)
				if err != nil {
					return fmt.Errorf("failed to unmarshal PVC. stdout: %s, err: %v", stdout, err)
				}
				pvNames = append(pvNames, pvc.Spec.VolumeName)
			}
			return nil
		}).Should(Succeed())

		Consistently(func() error {
			for _, pvName := range pvNames {
				stdout, stderr, err := kubectl("get", "pv", pvName, "-o", "json")
				if err != nil {
					return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
				}
			}
			return nil
		}).Should(Succeed())
	})
}
