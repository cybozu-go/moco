package controllers

import (
	"context"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func testNewSts(ns string) *appsv1.StatefulSet {
	sts := &appsv1.StatefulSet{}
	sts.Namespace = ns
	sts.Name = "moco-test"
	sts.Spec.Replicas = pointer.Int32(3)
	sts.Spec.ServiceName = "moco-test"
	sts.Spec.Selector = &v1.LabelSelector{
		MatchLabels: map[string]string{"foo": "bar"},
	}
	sts.Spec.Template.Labels = map[string]string{"foo": "bar"}
	sts.Spec.Template.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "moco-mysql:latest"}}
	return sts
}

func testNewPod(ns string, name string) *corev1.Pod {
	pod := &corev1.Pod{}
	pod.Namespace = ns
	pod.Name = name
	pod.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "moco-mysql:latest"}}
	return pod
}

var _ = Describe("PodWatcher", func() {
	ctx := context.Background()
	var stopFunc func()
	var mockMgr *mockManager

	BeforeEach(func() {
		err := k8sClient.DeleteAllOf(ctx, &mocov1beta2.MySQLCluster{}, client.InNamespace("default"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &appsv1.StatefulSet{}, client.InNamespace("default"))
		Expect(err).NotTo(HaveOccurred())

		pods := &corev1.PodList{}
		err = k8sClient.List(ctx, pods, client.InNamespace("default"))
		Expect(err).NotTo(HaveOccurred())
		for i := range pods.Items {
			pod := &pods.Items[i]
			pod.Finalizers = nil
			err = k8sClient.Update(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
		}
		err = k8sClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace("default"))
		Expect(err).NotTo(HaveOccurred())

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:             scheme,
			LeaderElection:     false,
			MetricsBindAddress: "0",
		})
		Expect(err).ToNot(HaveOccurred())

		mockMgr = &mockManager{
			clusters: make(map[string]struct{}),
		}
		podwatcher := &PodWatcher{
			Client:         mgr.GetClient(),
			ClusterManager: mockMgr,
		}
		err = podwatcher.SetupWithManager(mgr)
		Expect(err).ToNot(HaveOccurred())

		ctx, cancel := context.WithCancel(ctx)
		stopFunc = cancel
		go func() {
			err := mgr.Start(ctx)
			if err != nil {
				panic(err)
			}
		}()
		time.Sleep(100 * time.Millisecond)
	})

	AfterEach(func() {
		stopFunc()
		time.Sleep(100 * time.Millisecond)
	})

	It("should notify cluster manager", func() {
		cluster := testNewMySQLCluster("default")
		cluster.Finalizers = nil
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		sts := testNewSts("default")
		err = ctrl.SetControllerReference(cluster, sts, scheme)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Create(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		pod := testNewPod("default", "pod-1")
		pod.Finalizers = []string{"moco.cybozu.com/pod"}
		err = ctrl.SetControllerReference(sts, pod, scheme)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Create(ctx, pod)
		Expect(err).NotTo(HaveOccurred())

		time.Sleep(100 * time.Millisecond)
		Expect(mockMgr.isUpdated(types.NamespacedName{Namespace: "default", Name: "test"})).To(BeFalse())

		pod = &corev1.Pod{}
		pod.Namespace = "default"
		pod.Name = "pod-1"
		err = k8sClient.Delete(ctx, pod)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			return mockMgr.isUpdated(types.NamespacedName{Namespace: "default", Name: "test"})
		}).Should(BeTrue())
	})
})
