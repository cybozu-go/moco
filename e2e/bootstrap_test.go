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
		var stsJSON []byte
		Eventually(func() error {
			stdout, stderr, err := kubectl("get", "statefulsets/mysqlcluster", "-o", "json")
			if err != nil {
				return fmt.Errorf("failed to get StatefulSet. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			stsJSON = stdout
			return nil
		}).Should(Succeed())

		var sts appsv1.StatefulSet
		err = json.Unmarshal(stsJSON, &sts)
		Expect(err).ShouldNot(HaveOccurred(), "stsJSON=%s", stsJSON)

		Expect(sts.Spec.Replicas).Should(Equal(1))
		Expect(sts.Spec.Template.Spec.InitContainers).Should(HaveLen(1))
		Expect(sts.Spec.Template.Spec.InitContainers[0].Name).Should(Equal("init-0"))
	})
}
