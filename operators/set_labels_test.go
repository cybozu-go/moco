package operators

import (
	"context"

	"github.com/cybozu-go/moco"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Set labels", func() {

	ctx := context.Background()

	BeforeEach(func() {
		ns := corev1.Namespace{}
		ns.Name = namespace
		_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		pod0 := corev1.Pod{}
		pod0.Namespace = namespace
		pod0.Name = "pod-0"
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &pod0, func() error {
			pod0.Labels = map[string]string{
				moco.ClusterKey: "test-test-uid",
			}
			pod0.Spec.Containers = []corev1.Container{
				{
					Name:  "ubuntu",
					Image: "ubuntu:20.04",
				},
			}
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		pod1 := corev1.Pod{}
		pod1.Namespace = namespace
		pod1.Name = "pod-1"
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &pod1, func() error {
			pod1.Labels = map[string]string{
				moco.ClusterKey: "test-test-uid",
			}
			pod1.Spec.Containers = []corev1.Container{
				{
					Name:  "ubuntu",
					Image: "ubuntu:20.04",
				},
			}
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
	})

	It("should configure replication", func() {
		_, infra, cluster := getAccessorInfraCluster()

		op := setRoleLabelsOp{}

		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		pod0 := corev1.Pod{}
		err = infra.GetClient().Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: "pod-0"}, &pod0)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(pod0.Labels[moco.RoleKey]).Should(Equal(moco.PrimaryRole))

		pod1 := corev1.Pod{}
		err = infra.GetClient().Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: "pod-1"}, &pod1)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(pod1.Labels[moco.RoleKey]).Should(Equal(moco.ReplicaRole))
	})
})
