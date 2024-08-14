package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func testNewStatefulSet(cluster *mocov1beta2.MySQLCluster) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.PrefixedName(),
			Namespace: cluster.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(cluster, mocov1beta2.GroupVersion.WithKind("MySQLCluster")),
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To[int32](cluster.Spec.Replicas),
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
					Partition: ptr.To[int32](cluster.Spec.Replicas),
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"foo": "bar"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"foo": "bar"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "moco-mysql:latest",
						},
					},
				},
			},
		},
	}
}

func testNewPods(sts *appsv1.StatefulSet) []*corev1.Pod {
	pods := make([]*corev1.Pod, 0, *sts.Spec.Replicas)

	for i := 0; i < int(*sts.Spec.Replicas); i++ {
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%d", sts.Name, i),
				Namespace: sts.Namespace,
				Labels: map[string]string{
					appsv1.ControllerRevisionHashLabelKey: "rev1",
					"foo":                                 "bar",
				},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sts, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test", Image: "moco-mysql:latest"},
				},
			},
		})
	}
	return pods
}

func rolloutPods(ctx context.Context, rev1 int, rev2 int) {
	pods := &corev1.PodList{}
	err := k8sClient.List(ctx, pods, client.InNamespace("partition"), client.MatchingLabels(map[string]string{"foo": "bar"}))
	Expect(err).NotTo(HaveOccurred())
	Expect(len(pods.Items)).To(Equal(rev1 + rev2))

	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].Name < pods.Items[j].Name
	})

	for _, pod := range pods.Items {
		if rev1 > 0 {
			pod.Labels[appsv1.ControllerRevisionHashLabelKey] = "rev1"
			rev1--
		} else if rev2 > 0 {
			pod.Labels[appsv1.ControllerRevisionHashLabelKey] = "rev2"
			rev2--
		} else {
			break
		}

		err = k8sClient.Update(ctx, &pod)
		Expect(err).NotTo(HaveOccurred())
	}
}

var _ = Describe("StatefulSet reconciler", func() {
	ctx := context.Background()
	var stopFunc func()

	BeforeEach(func() {
		err := k8sClient.DeleteAllOf(ctx, &mocov1beta2.MySQLCluster{}, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &appsv1.StatefulSet{}, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:         scheme,
			LeaderElection: false,
			Metrics: metricsserver.Options{
				BindAddress: "0",
			},
		})
		Expect(err).ToNot(HaveOccurred())

		r := &StatefulSetPartitionReconciler{
			Client:   mgr.GetClient(),
			Recorder: mgr.GetEventRecorderFor("moco-controller"),
		}
		err = r.SetupWithManager(mgr)
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

	It("should partition to 0", func() {
		cluster := testNewMySQLCluster("partition")
		err := k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		meta.SetStatusCondition(&cluster.Status.Conditions,
			metav1.Condition{
				Type:   mocov1beta2.ConditionHealthy,
				Status: metav1.ConditionTrue,
				Reason: "healthy",
			},
		)
		err = k8sClient.Status().Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		sts := testNewStatefulSet(cluster)
		err = k8sClient.Create(ctx, sts)
		Expect(err).NotTo(HaveOccurred())
		sts.Status = appsv1.StatefulSetStatus{
			ObservedGeneration: 2,
			CurrentRevision:    "rev1",
			UpdateRevision:     "rev1",
			Replicas:           3,
			UpdatedReplicas:    3,
		}
		err = k8sClient.Status().Update(ctx, sts)
		Expect(err).NotTo(HaveOccurred())

		for _, pod := range testNewPods(sts) {
			err = k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
			pod.Status = corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
						Reason: "PodReady",
						LastTransitionTime: metav1.Time{
							Time: time.Now().Add(-24 * time.Hour),
						},
					},
				},
			}
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
		}

		Eventually(func() error {
			sts := &appsv1.StatefulSet{}
			key := client.ObjectKey{Namespace: "partition", Name: "moco-test"}
			if err := k8sClient.Get(ctx, key, sts); err != nil {
				return err
			}
			if sts.Spec.UpdateStrategy.RollingUpdate == nil {
				return errors.New("partition is nil")
			}

			switch *sts.Spec.UpdateStrategy.RollingUpdate.Partition {
			case 3:
				rolloutPods(ctx, 2, 1)
			case 2:
				rolloutPods(ctx, 1, 2)
			case 1:
				rolloutPods(ctx, 0, 3)
			case 0:
				return nil
			}

			return errors.New("unexpected partition")
		}).Should(Succeed())

		events := &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())
		sort.Slice(events.Items, func(i, j int) bool {
			return events.Items[i].CreationTimestamp.Before(&events.Items[j].CreationTimestamp)
		})
		Expect(events.Items).To(HaveLen(3))
		Expect(events.Items[0].Message).To(Equal("Updated partition from 3 to 2"))
		Expect(events.Items[1].Message).To(Equal("Updated partition from 2 to 1"))
		Expect(events.Items[2].Message).To(Equal("Updated partition from 1 to 0"))
	})
})
