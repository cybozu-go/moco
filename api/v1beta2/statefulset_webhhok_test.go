package v1beta2_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cybozu-go/moco/pkg/constants"
)

func makeStatefulSet() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "moco.cybozu.com/v1beta2",
					Kind:       "MySQLCluster",
					Name:       "test",
					UID:        "uid",
				},
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To[int32](3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"foo": "bar"}},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"foo": "bar"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mysql",
							Image: "mysql:examle",
						},
					},
				},
			},
		},
	}
}

func deleteStatefulSet() error {
	r := &appsv1.StatefulSet{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test"}, r)
	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return err
	}

	r.Finalizers = nil
	if err := k8sClient.Update(ctx, r); err != nil {
		return err
	}

	if err := k8sClient.Delete(ctx, r); err != nil {
		return err
	}

	return nil
}

var _ = Describe("StatefulSet Webhook", func() {
	ctx := context.TODO()

	BeforeEach(func() {
		err := deleteStatefulSet()
		Expect(err).NotTo(HaveOccurred())
	})

	It("should set partition when creating StatefulSet", func() {
		r := makeStatefulSet()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(r.Spec.Replicas))
	})

	It("should set partition when updating StatefulSet", func() {
		r := makeStatefulSet()
		r.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{
			Partition: ptr.To[int32](2),
		}
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(r.Spec.Replicas))
	})

	It("should not set partition when forcing updating StatefulSet", func() {
		r := makeStatefulSet()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(r.Spec.Replicas))

		r.Annotations = map[string]string{constants.AnnForceRollingUpdate: "true"}
		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate).To(BeNil())
	})

	It("should set partition when forcing updating StatefulSet with invalid value", func() {
		r := makeStatefulSet()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		r.Annotations = map[string]string{constants.AnnForceRollingUpdate: "false"}
		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(r.Spec.Replicas))
	})

	It("should not update partition when updating StatefulSet with only partition changed", func() {
		r := makeStatefulSet()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(r.Spec.Replicas))

		r.Spec.UpdateStrategy.RollingUpdate.Partition = ptr.To[int32](2)
		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(ptr.To[int32](2)))
	})

	It("should update partition when updating StatefulSet with partition and same field changed", func() {
		r := makeStatefulSet()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(r.Spec.Replicas))

		r.Spec.Replicas = ptr.To[int32](5)
		r.Spec.UpdateStrategy.RollingUpdate.Partition = ptr.To[int32](2)
		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(ptr.To[int32](5)))
	})

	It("should update partition when updating StatefulSet with partition unchanged", func() {
		r := makeStatefulSet()
		err := k8sClient.Create(ctx, r)
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(r.Spec.Replicas))

		r.Spec.Replicas = ptr.To[int32](5)
		err = k8sClient.Update(ctx, r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Spec.UpdateStrategy.RollingUpdate.Partition).To(Equal(ptr.To[int32](5)))
	})
})
