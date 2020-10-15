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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	systemNamespace = "test-moco-system"
	namespace       = "controllers-test"

	clusterName = "mysqlcluster"
)

func mysqlClusterResource() *mocov1alpha1.MySQLCluster {
	cluster := &mocov1alpha1.MySQLCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MySQLCluster",
			APIVersion: mocov1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 3,
			PodTemplate: mocov1alpha1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mysqld",
							Image: "mysql:dev",
						},
					},
				},
			},
			DataVolumeClaimTemplateSpec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						"storage": *resource.NewQuantity(1<<10, resource.BinarySI),
					},
				},
			},
		},
	}
	return cluster
}

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

		cluster = mysqlClusterResource()
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
				err = k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, &actual)
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
			isUpdated, err := reconciler.createOrUpdateServices(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			createdPrimaryService := &corev1.Service{}
			createdReplicaService := &corev1.Service{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(cluster)), Namespace: namespace}, createdPrimaryService)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(cluster)), Namespace: namespace}, createdReplicaService)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(createdPrimaryService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdPrimaryService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))

			Expect(createdReplicaService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdPrimaryService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			isUpdated, err = reconciler.createOrUpdateServices(ctx, reconciler.Log, cluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})

		It("should use serviceTemplate", func() {
			newCluster := &mocov1alpha1.MySQLCluster{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			newCluster.Spec.ServiceTemplate = &corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{
					{
						Name:       "mysql",
						Protocol:   corev1.ProtocolTCP,
						Port:       8888,
						TargetPort: intstr.FromInt(8888),
					},
				},
			}
			err = k8sClient.Update(ctx, newCluster)
			Expect(err).ShouldNot(HaveOccurred())

			isUpdated, err := reconciler.createOrUpdateServices(ctx, reconciler.Log, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeTrue())

			createdPrimaryService := &corev1.Service{}
			createdReplicaService := &corev1.Service{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-primary", moco.UniqueName(newCluster)), Namespace: namespace}, createdPrimaryService)
			Expect(err).ShouldNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: fmt.Sprintf("%s-replica", moco.UniqueName(newCluster)), Namespace: namespace}, createdReplicaService)
			Expect(err).ShouldNot(HaveOccurred())

			Expect(createdPrimaryService.Spec.Type).Should(Equal(corev1.ServiceTypeLoadBalancer))
			Expect(createdReplicaService.Spec.Type).Should(Equal(corev1.ServiceTypeLoadBalancer))

			Expect(createdPrimaryService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[0].Name).Should(Equal("mysql"))
			Expect(createdPrimaryService.Spec.Ports[0].Port).Should(BeNumerically("==", moco.MySQLPort))

			Expect(createdReplicaService.Spec.Ports).Should(HaveLen(2))
			Expect(createdPrimaryService.Spec.Ports[1].Name).Should(Equal("mysqlx"))
			Expect(createdPrimaryService.Spec.Ports[1].Port).Should(BeNumerically("==", moco.MySQLXPort))

			isUpdated, err = reconciler.createOrUpdateServices(ctx, reconciler.Log, newCluster)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(isUpdated).Should(BeFalse())
		})
	})
})
