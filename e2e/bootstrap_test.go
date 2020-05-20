package e2e

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
)

func testBootstrap() {
	It("should create StatefulSet", func() {
		By("registering MySQLCluster")
		stdout, stderr, err := kubectl("apply", "-f", "manifests/mysql_cluster.yaml")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		By("getting StatefulSet")
		Eventually(func() error {
			stdout, stderr, err := kubectl("get", "statefulsets/mysqlcluster", "-o", "json")
			if err != nil {
				return fmt.Errorf("failed to get StatefulSet. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}

			var sts appsv1.StatefulSet
			err = json.Unmarshal(stdout, &sts)
			if err != nil {
				return fmt.Errorf("failed to unmarshal StatefulSet. stdout: %s, err: %v", stdout, err)
			}

			if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
				return fmt.Errorf("replicas should be 1: %v", sts.Spec.Replicas)
			}

			if len(sts.Spec.Template.Spec.InitContainers) != 1 {
				return fmt.Errorf("number of initContainers should be 1: %d", len(sts.Spec.Template.Spec.InitContainers))
			}

			initContainerName := "init-0"
			if sts.Spec.Template.Spec.InitContainers[0].Name != initContainerName {
				return fmt.Errorf(
					"name of first initContainer should be  %s: %s",
					initContainerName,
					sts.Spec.Template.Spec.InitContainers[0].Name,
				)
			}

			if sts.Status.ReadyReplicas != 1 {
				return fmt.Errorf("readyReplicas should be 1: %v", sts.Status.ReadyReplicas)
			}
			return nil
		}).Should(Succeed())
	})
}
