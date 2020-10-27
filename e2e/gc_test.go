package e2e

import (
	"fmt"
	"strings"
	"time"

	"github.com/cybozu-go/moco"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
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
				if !strings.Contains(string(stdout), "No resources found in e2e-test namespace.") {
					return fmt.Errorf("resources remain: %s", stdout)
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
				if !strings.Contains(string(stdout), "not found") {
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
			if !strings.Contains(string(stdout), "not found") {
				return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			return nil
		}).Should(Succeed())

		Consistently(func() error {
			stdout, stderr, err := kubectl("get", "-n", "e2e-test", "pvc")
			if err != nil {
				return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			return nil
		}).Should(Succeed())

		Consistently(func() error {
			stdout, stderr, err := kubectl("get", "pv")
			if err != nil {
				return fmt.Errorf("failed to get resource. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			return nil
		}).Should(Succeed())
	})
}
