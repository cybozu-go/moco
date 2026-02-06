package controllers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubectl/pkg/util/podutils"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
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
	UpdateInterval          time.Duration
	RateLimiter             *rate.Limiter
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

	if !r.RateLimiter.Allow() {
		metrics.PartitionUpdateRetriesTotalVec.WithLabelValues(req.Namespace).Inc()
		return reconcile.Result{RequeueAfter: r.UpdateInterval}, nil
	}

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

	// In this case, the reconciliation of MySQLClusterReconciler has not been completed.
	// Wait until completion.
	if cluster.Generation != cluster.Status.ReconcileInfo.Generation || sts.Generation != sts.Status.ObservedGeneration {
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if !r.needPartitionUpdate(sts) {
		return reconcile.Result{}, nil
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

	metrics.LastPartitionUpdatedVec.WithLabelValues(cluster.Name, cluster.Namespace).SetToCurrentTime()

	return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *StatefulSetPartitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	stsPrct := partitionControllerPredicate()
	podPrct := partitionControllerPredicate()

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.StatefulSet{}, builder.WithPredicates(stsPrct)).
		Owns(&corev1.Pod{}, builder.WithPredicates(podPrct)).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}

// partitionControllerPredicate filters StatefulSets and Pods managed by MOCO.
func partitionControllerPredicate() predicate.Funcs {
	// Predicate function for StatefulSets and Pods. They have a prefixed name and specific labels.
	prctFunc := func(o client.Object) bool {
		if !strings.HasPrefix(o.GetName(), "moco-") {
			return false
		}

		labels := o.GetLabels()
		if labels[constants.LabelAppName] != constants.AppNameMySQL {
			return false
		}
		if labels[constants.LabelAppCreatedBy] != constants.AppCreator {
			return false
		}
		return true
	}

	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool { return prctFunc(e.ObjectNew) },
		CreateFunc: func(e event.CreateEvent) bool { return prctFunc(e.Object) },
	}
}

// isRolloutReady returns true if the StatefulSet is ready for rolling update.
func (r *StatefulSetPartitionReconciler) isRolloutReady(ctx context.Context, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet) (bool, error) {
	log := crlog.FromContext(ctx)

	if ptr.Deref(sts.Spec.Replicas, 1) == sts.Status.UpdatedReplicas {
		// In this case, a rolling update has been completed.
		return true, nil
	}

	podList, err := r.getSortedPodList(ctx, sts)
	if err != nil {
		return false, fmt.Errorf("failed to get pod list: %w", err)
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

	nextRolloutTarget := r.nextRolloutTargetIndex(sts)
	if nextRolloutTarget < 0 {
		return false, nil
	}

	if podList.Items[nextRolloutTarget].Labels[appsv1.ControllerRevisionHashLabelKey] == sts.Status.UpdateRevision {
		return true, nil
	}

	// If not all Pods are ready, the MySQLCluster becomes Unhealthy.
	// Even if the MySQLCluster is not healthy, the rollout continues if the rollout target Pod is not ready.
	// This is because there is an expectation that restarting the Not Ready Pod might improve its state.
	if podutils.IsPodReady(&podList.Items[nextRolloutTarget]) && !r.isMySQLClusterHealthy(cluster) {
		log.Info("MySQLCluster is not healthy", "name", cluster.Name, "namespace", cluster.Namespace)
		return false, nil
	}

	ready := r.areAllChildPodsRolloutReady(ctx, sts, podList)

	return ready, nil
}

// getSortedPodList returns a sorted child pod list.
// The list is sorted by pod name with ascending order.
func (r *StatefulSetPartitionReconciler) getSortedPodList(ctx context.Context, sts *appsv1.StatefulSet) (*corev1.PodList, error) {
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(sts.Namespace),
		client.MatchingLabels(sts.Spec.Selector.MatchLabels),
	}

	err := r.Client.List(ctx, podList, listOpts...)
	if err != nil {
		return nil, err
	}

	sort.Slice(podList.Items, func(i, j int) bool {
		return podList.Items[i].Name < podList.Items[j].Name
	})

	return podList, nil
}

func (r *StatefulSetPartitionReconciler) areAllChildPodsRolloutReady(ctx context.Context, sts *appsv1.StatefulSet, sortedPodList *corev1.PodList) bool {
	log := crlog.FromContext(ctx)

	nextRolloutTarget := r.nextRolloutTargetIndex(sts)

	// Proceed with the rollout for the next Pod to be rolled out, even if it is not Ready.
	// Expect that the Pod state will improve by being updated through the rollout.
	// All other Pods must be Ready.
	for i, pod := range sortedPodList.Items {
		if i == nextRolloutTarget {
			continue
		}
		if pod.DeletionTimestamp != nil {
			log.Info("Pod is in the process of being terminated", "pod", pod.Name)
			return false
		}
		if !podutils.IsPodAvailable(&pod, 5, metav1.Now()) {
			log.Info("Pod is not ready", "pod", pod.Name)
			return false
		}
		for _, c := range pod.Status.InitContainerStatuses {
			log.Info("Container is not ready", "pod", pod.Name, "container", c.Name)
			if !c.Ready {
				return false
			}
		}
		for _, c := range pod.Status.ContainerStatuses {
			log.Info("Container is not ready", "pod", pod.Name, "container", c.Name)
			if !c.Ready {
				return false
			}
		}
	}

	return true
}

// isMySQLClusterHealthy checks the health status of a given MySQLCluster.
func (r *StatefulSetPartitionReconciler) isMySQLClusterHealthy(cluster *mocov1beta2.MySQLCluster) bool {
	return meta.IsStatusConditionTrue(cluster.Status.Conditions, mocov1beta2.ConditionHealthy)
}

// nextRolloutTargetIndex returns the index of the next rollout target Pod.
// The index is calculated by subtracting 1 from the current partition.
// If there is no rollout target, it returns -1.
func (r *StatefulSetPartitionReconciler) nextRolloutTargetIndex(sts *appsv1.StatefulSet) int {
	if sts.Spec.UpdateStrategy.RollingUpdate == nil || sts.Spec.UpdateStrategy.RollingUpdate.Partition == nil {
		return -1
	}

	return int(*sts.Spec.UpdateStrategy.RollingUpdate.Partition) - 1
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
	log := crlog.FromContext(ctx)

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

	log.Info("Updated partition", "newPartition", newPartition, "oldPartition", oldPartition)
	r.Recorder.Eventf(sts, corev1.EventTypeNormal, "PartitionUpdate", "Updated partition from %d to %d", oldPartition, newPartition)

	return nil
}
