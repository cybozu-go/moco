package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var _ = Describe("MySQLCluster controller", func() {
	Context("when creating MySQLCluster resource", func() {
		It("should create resources", func() {
			ctx := context.Background()

			manifest := `apiVersion: moco.cybozu.com/v1alpha1
kind: MySQLCluster
metadata:
  name: mysqlcluster
  namespace: default
spec:
  replicas: 3
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: mysql:dev
  dataVolumeClaimTemplateSpec:
    accessModes: [ "ReadWriteOnce" ]
    resources:
      requests:
        storage: 1Gi
`
			cluster := &mocov1alpha1.MySQLCluster{}
			err := yaml.Unmarshal([]byte(manifest), cluster)
			Expect(err).ShouldNot(HaveOccurred())

			err = k8sClient.Create(ctx, cluster)
			Expect(err).ShouldNot(HaveOccurred())

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "moco-system",
				},
			}

			err = k8sClient.Create(ctx, ns)
			Expect(err).ShouldNot(HaveOccurred())

			err = os.Setenv("POD_NAMESPACE", "moco-system")
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err := reconciler.reconcileInitialize(ctx, reconciler.Log, cluster)
			Expect(isUpdated).Should(BeTrue())
			Expect(err).ShouldNot(HaveOccurred())

			Eventually(func() error {
				var actual mocov1alpha1.MySQLCluster
				err = k8sClient.Get(ctx, client.ObjectKey{Name: "mysqlcluster", Namespace: "default"}, &actual)
				if err != nil {
					return err
				}

				if actual.Status.ServerIDBase == nil {
					return errors.New("status.ServerIDBase is not yet assigned")
				}

				return nil
			}, 30*time.Second).Should(Succeed())

			createdPrimaryService := &corev1.Service{}
			createdReplicaService := &corev1.Service{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(cluster)), Namespace: "default"}, createdPrimaryService)
				if err != nil {
					return err
				}

				err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(cluster)), Namespace: "default"}, createdReplicaService)
				if err != nil {
					return err
				}

				return nil
			}, 30*time.Second).Should(Succeed())

			expectedPorts := []corev1.ServicePort{
				{
					Name:       "mysql",
					Protocol:   corev1.ProtocolTCP,
					Port:       moco.MySQLPort,
					TargetPort: intstr.FromInt(moco.MySQLPort),
				},
				{
					Name:       "mysqlx",
					Protocol:   corev1.ProtocolTCP,
					Port:       moco.MySQLXPort,
					TargetPort: intstr.FromInt(moco.MySQLXPort),
				},
			}

			Expect(createdPrimaryService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports).Should(ConsistOf(expectedPorts))

			Expect(createdReplicaService.Spec.Ports).Should(HaveLen(2))
			Expect(createdReplicaService.Spec.Ports).Should(ConsistOf(expectedPorts))
		})
	})
})
