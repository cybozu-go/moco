package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/metrics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	prometheusutil "github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/time/rate"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func testNewStatefulSet(cluster *mocov1beta2.MySQLCluster) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.PrefixedName(),
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				constants.LabelAppName:      constants.AppNameMySQL,
				constants.LabelAppInstance:  cluster.Name,
				constants.LabelAppCreatedBy: constants.AppCreator,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(cluster, mocov1beta2.GroupVersion.WithKind("MySQLCluster")),
			},
			Generation: 1,
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
				MatchLabels: map[string]string{
					constants.LabelAppName:      constants.AppNameMySQL,
					constants.LabelAppInstance:  cluster.Name,
					constants.LabelAppCreatedBy: constants.AppCreator,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						constants.LabelAppName:      constants.AppNameMySQL,
						constants.LabelAppInstance:  cluster.Name,
						constants.LabelAppCreatedBy: constants.AppCreator,
					},
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
		podLabels := sts.Spec.Template.DeepCopy().Labels
		podLabels[appsv1.ControllerRevisionHashLabelKey] = "rev1"
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:            fmt.Sprintf("%s-%d", sts.Name, i),
				Namespace:       sts.Namespace,
				Labels:          podLabels,
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

func rolloutPods(ctx context.Context, sts *appsv1.StatefulSet, rev1 int, rev2 int) {
	pods := &corev1.PodList{}
	err := k8sClient.List(ctx, pods, client.InNamespace("partition"), client.MatchingLabels(sts.Spec.Template.Labels))
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

		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			newSts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: sts.Namespace, Name: sts.Name}, newSts)
			if err != nil {
				return err
			}
			if rev1 == 0 {
				newSts.Status = appsv1.StatefulSetStatus{
					CurrentRevision: "rev2",
					UpdateRevision:  "rev2",
					Replicas:        int32(rev1) + int32(rev2),
					UpdatedReplicas: int32(rev2),
				}
			} else if rev2 == 0 {
				newSts.Status = appsv1.StatefulSetStatus{
					CurrentRevision: "rev1",
					UpdateRevision:  "rev1",
					Replicas:        int32(rev1) + int32(rev2),
					UpdatedReplicas: int32(rev1),
				}
			} else {
				newSts.Status = appsv1.StatefulSetStatus{
					CurrentRevision: "rev1",
					UpdateRevision:  "rev2",
					Replicas:        int32(rev1) + int32(rev2),
					UpdatedReplicas: int32(rev2),
				}
			}
			newSts.Status.ObservedGeneration = newSts.Generation
			return k8sClient.Status().Update(ctx, newSts)
		})
		Expect(err).NotTo(HaveOccurred())
	}
}

func setupNewManager(ctx context.Context, updateInterval time.Duration) context.CancelFunc {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		Controller: config.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})
	Expect(err).ToNot(HaveOccurred())

	r := &StatefulSetPartitionReconciler{
		Client:         mgr.GetClient(),
		Recorder:       mgr.GetEventRecorderFor("moco-controller"),
		UpdateInterval: updateInterval,
		RateLimiter:    rate.NewLimiter(rate.Every(updateInterval), 1),
	}
	err = r.SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		err := mgr.Start(ctx)
		if err != nil {
			panic(err)
		}
	}()
	return cancel
}

func testUpdatePartition(ctx context.Context, updateInterval time.Duration) {
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
	cluster.Status.ReconcileInfo.Generation = 1
	err = k8sClient.Status().Update(ctx, cluster)
	Expect(err).NotTo(HaveOccurred())

	sts := testNewStatefulSet(cluster)
	err = k8sClient.Create(ctx, sts)
	Expect(err).NotTo(HaveOccurred())
	sts.Status = appsv1.StatefulSetStatus{
		ObservedGeneration: 1,
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
			rolloutPods(ctx, sts, 2, 1)
		case 2:
			rolloutPods(ctx, sts, 1, 2)
		case 1:
			rolloutPods(ctx, sts, 0, 3)
		case 0:
			return nil
		}

		return fmt.Errorf("unexpected partition: %d", *sts.Spec.UpdateStrategy.RollingUpdate.Partition)
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

	if updateInterval > 0 {
		for i := 1; i < len(events.Items); i++ {
			interval := events.Items[i].CreationTimestamp.Sub(events.Items[i-1].CreationTimestamp.Time)
			Expect(interval).To(BeNumerically(">=", updateInterval))
		}
		retryCount := prometheusutil.CollectAndCount(metrics.PartitionUpdateRetriesTotalVec)
		Expect(retryCount).To(BeNumerically(">", 0))
	}
}

var _ = Describe("StatefulSet reconciler", func() {
	ctx := context.Background()
	var stopFunc func()

	BeforeEach(func() {
		cs := &mocov1beta2.MySQLClusterList{}
		err := k8sClient.List(ctx, cs, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())
		for _, cluster := range cs.Items {
			cluster.Finalizers = nil
			err := k8sClient.Update(ctx, &cluster)
			Expect(err).NotTo(HaveOccurred())
		}

		err = k8sClient.DeleteAllOf(ctx, &corev1.Event{}, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &mocov1beta2.MySQLCluster{}, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &appsv1.StatefulSet{}, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace("partition"))
		Expect(err).NotTo(HaveOccurred())

		time.Sleep(100 * time.Millisecond)
	})

	AfterEach(func() {
		if stopFunc != nil {
			stopFunc()
		}
		time.Sleep(100 * time.Millisecond)
	})

	Context("with different update intervals", func() {
		DescribeTable("should partition to 0",
			func(updateInterval time.Duration) {
				stopFunc = setupNewManager(ctx, updateInterval)
				testUpdatePartition(ctx, updateInterval)
			},
			Entry("without interval", 0*time.Millisecond),
			Entry("with 1000ms interval", 1000*time.Millisecond),
		)
	})
})

var _ = Describe("StatefulSetPartitionReconciler predicates", func() {
	DescribeTable("should filter events",
		func(obj client.Object, expect bool) {
			prct := partitionControllerPredicate()
			Expect(prct.Update(event.UpdateEvent{ObjectNew: obj})).To(Equal(expect))
			Expect(prct.Create(event.CreateEvent{Object: obj})).To(Equal(expect))
			Expect(prct.Delete(event.DeleteEvent{Object: obj})).To(Equal(expect))
			Expect(prct.Generic(event.GenericEvent{Object: obj})).To(Equal(expect))
		},
		Entry("statefulset with moco labels", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
			Name: "moco-test",
			Labels: map[string]string{
				constants.LabelAppName:      constants.AppNameMySQL,
				constants.LabelAppCreatedBy: constants.AppCreator,
			},
		}}, true),
		Entry("pod with moco labels", &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "moco-test-0",
			Labels: map[string]string{
				constants.LabelAppName:      constants.AppNameMySQL,
				constants.LabelAppCreatedBy: constants.AppCreator,
			},
		}}, true),
		Entry("statefulset without prefix", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			Labels: map[string]string{
				constants.LabelAppName:      constants.AppNameMySQL,
				constants.LabelAppCreatedBy: constants.AppCreator,
			},
		}}, false),
		Entry("statefulset without app label", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
			Name: "moco-test",
			Labels: map[string]string{
				constants.LabelAppCreatedBy: constants.AppCreator,
			},
		}}, false),
		Entry("statefulset without creator label", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
			Name: "moco-test",
			Labels: map[string]string{
				constants.LabelAppName: constants.AppNameMySQL,
			},
		}}, false),
		Entry("statefulset with wrong labels", &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
			Name: "moco-test",
			Labels: map[string]string{
				constants.LabelAppName:      constants.AppNameMySQL,
				constants.LabelAppCreatedBy: "other",
			},
		}}, false),
	)
})
