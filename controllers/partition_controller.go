package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubectl/pkg/util/podutils"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/metrics"
)

var _ reconcile.Reconciler = &StatefulSetPartitionReconciler{}

// StatefulSetPartitionReconciler reconciles a StatefulSet object
type StatefulSetPartitionReconciler struct {
	client.Client
	Recorder                record.EventRecorder
	MaxConcurrentReconciles int
}

//+kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods/status,verbs=get
//+kubebuilder:rbac:groups="",resources=events,verbs=create;update;patch

// Reconcile implements Reconciler interface.
// See https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile#Reconciler
func (r *StatefulSetPartitionReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := crlog.FromContext(ctx)

	sts := &appsv1.StatefulSet{}
	err := r.Get(ctx, req.NamespacedName, sts)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		log.Error(err, "unable to fetch StatefulSet")
		return reconcile.Result{}, err
	}

	cluster, err := r.getMySQLCluster(ctx, sts)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get MySQLCluster: %w", err)
	}

	metrics.CurrentReplicasVec.WithLabelValues(cluster.Name, cluster.Namespace).Set(float64(sts.Status.CurrentReplicas))
	metrics.UpdatedReplicasVec.WithLabelValues(cluster.Name, cluster.Namespace).Set(float64(sts.Status.UpdatedReplicas))

	if !r.needPartitionUpdate(sts) {
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if r.isStatefulSetRolloutComplete(sts) {
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}

	ready, err := r.isRolloutReady(ctx, cluster, sts)
	if err != nil {
		log.Error(err, "failed to check if rollout is ready")
		return reconcile.Result{}, err
	}
	if !ready {
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if err := r.patchNewPartition(ctx, sts); err != nil {
		log.Error(err, "failed to apply new partition")
		return reconcile.Result{}, err
	}

	log.Info("partition is updated")
	metrics.LastPartitionUpdatedVec.WithLabelValues(cluster.Name, cluster.Namespace).SetToCurrentTime()

	return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *StatefulSetPartitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapFn := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []ctrl.Request {
			return []ctrl.Request{
				{
					NamespacedName: client.ObjectKey{
						Name:      obj.GetName(),
						Namespace: obj.GetNamespace(),
					},
				},
			}
		})

	p := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			old := e.ObjectOld.(*mocov1beta2.MySQLCluster)
			new := e.ObjectNew.(*mocov1beta2.MySQLCluster)
			return old.ResourceVersion != new.ResourceVersion
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.StatefulSet{}).
		Owns(&corev1.Pod{}).
		Watches(
			&mocov1beta2.MySQLCluster{},
			mapFn,
			builder.WithPredicates(p),
		).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}

// isRolloutReady returns true if the StatefulSet is ready for rolling update.
func (r *StatefulSetPartitionReconciler) isRolloutReady(ctx context.Context, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet) (bool, error) {
	log := crlog.FromContext(ctx)

	if !r.isMySQLClusterHealthy(cluster) {
		log.Info("MySQLCluster is not healthy", "name", cluster.Name, "namespace", cluster.Namespace)
		return false, nil
	}

	ready, err := r.areAllChildPodsRolloutReady(ctx, sts)
	if err != nil {
		return false, fmt.Errorf("failed to check if all child pods are ready: %w", err)
	}

	return ready, nil
}

func (r *StatefulSetPartitionReconciler) areAllChildPodsRolloutReady(ctx context.Context, sts *appsv1.StatefulSet) (bool, error) {
	log := crlog.FromContext(ctx)

	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(sts.Namespace),
		client.MatchingLabels(sts.Spec.Selector.MatchLabels),
	}

	err := r.Client.List(ctx, podList, listOpts...)
	if err != nil {
		return false, err
	}

	var replicas int32
	if sts.Spec.Replicas != nil {
		replicas = *sts.Spec.Replicas
	} else {
		replicas = 1
	}

	if replicas != int32(len(podList.Items)) {
		log.Info("replicas is different from the expected number of Pods", "expected", replicas, "actual", len(podList.Items))
		return false, nil
	}

	sort.Slice(podList.Items, func(i, j int) bool {
		return podList.Items[i].Name < podList.Items[j].Name
	})

	lastIndex := len(podList.Items) - 1
	latestRevision := podList.Items[lastIndex].Labels[appsv1.ControllerRevisionHashLabelKey]
	revisionCounts := make(map[string]int)

	for _, pod := range podList.Items {
		if !podutils.IsPodAvailable(&pod, 5, metav1.Now()) {
			log.Info("Pod is not ready", "name", pod.Name, "namespace", pod.Namespace)
			return false, nil
		}
		revision := pod.Labels[appsv1.ControllerRevisionHashLabelKey]
		revisionCounts[revision]++
	}

	partition := *sts.Spec.UpdateStrategy.RollingUpdate.Partition
	expectedPodsCount := int(*sts.Spec.Replicas) - int(partition)

	// If there is only one revision and expectedPodsCount is 0 or less, it means there are no Pods in the middle of a rollout.
	if expectedPodsCount <= 0 && len(revisionCounts) == 1 {
		return true, nil
	}

	if revisionCounts[latestRevision] != expectedPodsCount {
		log.Info("Pod count is different from the expected number", "revision", latestRevision, "expected", expectedPodsCount, "actual", revisionCounts[latestRevision])
		return false, nil
	}

	return true, nil
}

// isMySQLClusterHealthy checks the health status of a given MySQLCluster.
func (r *StatefulSetPartitionReconciler) isMySQLClusterHealthy(cluster *mocov1beta2.MySQLCluster) bool {
	return meta.IsStatusConditionTrue(cluster.Status.Conditions, mocov1beta2.ConditionHealthy)
}

// getMySQLCluster retrieves the MySQLCluster release that owns a given StatefulSet.
func (r *StatefulSetPartitionReconciler) getMySQLCluster(ctx context.Context, sts *appsv1.StatefulSet) (*mocov1beta2.MySQLCluster, error) {
	for _, ownerRef := range sts.GetOwnerReferences() {
		if ownerRef.Kind != "MySQLCluster" {
			continue
		}

		cluster := &mocov1beta2.MySQLCluster{}
		if err := r.Get(ctx, types.NamespacedName{Name: ownerRef.Name, Namespace: sts.Namespace}, cluster); err != nil {
			return nil, err
		}

		return cluster, nil
	}

	return nil, fmt.Errorf("StatefulSet %s/%s has no owner reference to MySQLCluster", sts.Namespace, sts.Name)
}

// isStatefulSetRolloutComplete returns true if the StatefulSet is update completed.
func (r *StatefulSetPartitionReconciler) isStatefulSetRolloutComplete(sts *appsv1.StatefulSet) bool {
	if sts.Spec.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType {
		return false
	}

	if sts.Status.ObservedGeneration == 0 || sts.Generation > sts.Status.ObservedGeneration {
		return false
	}

	if sts.Spec.Replicas != nil && sts.Status.ReadyReplicas < *sts.Spec.Replicas {
		return false
	}

	if sts.Spec.UpdateStrategy.RollingUpdate != nil {
		if sts.Spec.Replicas != nil && sts.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
			if sts.Status.UpdatedReplicas < (*sts.Spec.Replicas - *sts.Spec.UpdateStrategy.RollingUpdate.Partition) {
				return false
			}
		}
	}

	if sts.Status.UpdateRevision != sts.Status.CurrentRevision {
		return false
	}

	return true
}

// needPartitionUpdate returns true if the StatefulSet needs to update partition.
func (r *StatefulSetPartitionReconciler) needPartitionUpdate(sts *appsv1.StatefulSet) bool {
	if sts.Annotations[constants.AnnForceRollingUpdate] == "true" {
		return false
	}
	if sts.Spec.UpdateStrategy.RollingUpdate == nil || sts.Spec.UpdateStrategy.RollingUpdate.Partition == nil {
		return false
	}

	return *sts.Spec.UpdateStrategy.RollingUpdate.Partition > 0
}

// patchNewPartition patches the new partition of a StatefulSet.
func (r *StatefulSetPartitionReconciler) patchNewPartition(ctx context.Context, sts *appsv1.StatefulSet) error {
	oldPartition := *sts.Spec.UpdateStrategy.RollingUpdate.Partition
	newPartition := oldPartition - 1

	if oldPartition == 0 {
		return nil
	}

	patch := client.MergeFrom(sts.DeepCopy())
	sts.Spec.UpdateStrategy.RollingUpdate.Partition = &newPartition

	if err := r.Client.Patch(ctx, sts, patch); err != nil {
		return fmt.Errorf("failed to patch new partition to StatefulSet %s/%s: %w", sts.Namespace, sts.Name, err)
	}

	r.Recorder.Eventf(sts, corev1.EventTypeNormal, "PartitionUpdate", "Updated partition from %d to %d", oldPartition, newPartition)

	return nil
}
