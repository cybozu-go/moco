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
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	systemNamespace = "test-moco-system"
	namespace       = "controllers-test"
)

var _ = Describe("MySQLCluster controller", func() {

	ctx := context.Background()
	cluster := &mocov1alpha1.MySQLCluster{}

	BeforeEach(func() {
		sysNs := corev1.Namespace{}
		sysNs.Name = systemNamespace
		_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &sysNs, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())
		ns := corev1.Namespace{}
		ns.Name = namespace
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		manifest := `apiVersion: moco.cybozu.com/v1alpha1
kind: MySQLCluster
metadata:
  name: mysqlcluster
  namespace: controllers-test
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
		err = yaml.Unmarshal([]byte(manifest), cluster)
		Expect(err).ShouldNot(HaveOccurred())

		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, cluster, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		err = os.Setenv("POD_NAMESPACE", systemNamespace)
		Expect(err).ShouldNot(HaveOccurred())
	})

	Context("ServerIDBase", func() {
		It("should set ServerIDBase", func() {
			isUpdated, err := reconciler.setServerIDBaseIfNotAssigned(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			Eventually(func() error {
				var actual mocov1alpha1.MySQLCluster
				err = k8sClient.Get(ctx, client.ObjectKey{Name: "mysqlcluster", Namespace: namespace}, &actual)
				if err != nil {
					return err
				}

				if actual.Status.ServerIDBase == nil {
					return errors.New("status.ServerIDBase is not yet assigned")
				}

				return nil
			}, 5*time.Second).Should(Succeed())

			isUpdated, err = reconciler.setServerIDBaseIfNotAssigned(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})

	Context("Services", func() {
		It("should create services", func() {
			isUpdated, err := reconciler.createOrUpdateService(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			createdPrimaryService := &corev1.Service{}
			createdReplicaService := &corev1.Service{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(cluster)), Namespace: namespace}, createdPrimaryService)
				if err != nil {
					return err
				}

				err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(cluster)), Namespace: namespace}, createdReplicaService)
				if err != nil {
					return err
				}

				return nil
			}, 5*time.Second).Should(Succeed())

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

			isUpdated, err = reconciler.createOrUpdateService(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})
})
