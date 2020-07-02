package e2e

import (
	"encoding/json"
	"fmt"

	"github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func testBootstrap() {
	It("should create StatefulSet", func() {
		By("registering MySQLCluster")
		kubectl("create", "ns", "e2e-test") // ignore error
		stdout, stderr, err := kubectl("apply", "-n", "e2e-test", "-f", "manifests/mysql_cluster.yaml")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		By("getting Secret which contains root password")
		Eventually(func() error {
			stdout, stderr, err := kubectl("get", "-n", "e2e-test", "mysqlcluster/mysqlcluster", "-o", "json")
			if err != nil {
				return fmt.Errorf("failed to get MySQLCluster. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}

			var mysqlCluster v1alpha1.MySQLCluster
			err = json.Unmarshal(stdout, &mysqlCluster)
			if err != nil {
				return fmt.Errorf("failed to unmarshal MySQLCluster. stdout: %s, err: %v", stdout, err)
			}
			stdout, stderr, err = kubectl("get", "-n", "e2e-test", "secret/root-password-mysqlcluster-"+string(mysqlCluster.GetUID()), "-o", "json")
			if err != nil {
				return fmt.Errorf("failed to get Secret. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}

			var secret corev1.Secret
			err = json.Unmarshal(stdout, &secret)
			if err != nil {
				return fmt.Errorf("failed to unmarshal Secret stdout: %s, err: %v", stdout, err)
			}

			return nil
		}).Should(Succeed())

		By("getting StatefulSet")
		Eventually(func() error {
			stdout, stderr, err := kubectl("get", "-n", "e2e-test", "mysqlcluster/mysqlcluster", "-o", "json")
			if err != nil {
				return fmt.Errorf("failed to get MySQLCluster. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}

			var mysqlCluster v1alpha1.MySQLCluster
			err = json.Unmarshal(stdout, &mysqlCluster)
			if err != nil {
				return fmt.Errorf("failed to unmarshal MySQLCluster. stdout: %s, err: %v", stdout, err)
			}

			stdout, stderr, err = kubectl("get", "-n", "e2e-test", "statefulsets/mysqlcluster-"+string(mysqlCluster.GetUID()), "-o", "json")
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

			if sts.Status.ReadyReplicas != 1 {
				return fmt.Errorf("readyReplicas should be 1: %v", sts.Status.ReadyReplicas)
			}
			return nil
		}).Should(Succeed())
	})
}
