package controllers

import (
	"context"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
		log.Error(err, "unable to fetch StatefulSet", "name", req.NamespacedName.Name, "namespace", req.NamespacedName.Namespace)
		return reconcile.Result{}, err
	}

	if !r.needPartitionUpdate(sts) {
		return reconcile.Result{}, nil
	}

	if r.isStatefulSetRolloutComplete(sts) {
		return reconcile.Result{}, nil
	}

	ready, err := r.isRolloutReady(ctx, sts)
	if err != nil {
		log.Error(err, "failed to check if rollout is ready", "name", req.NamespacedName.Name, "namespace", req.NamespacedName.Namespace)
		return reconcile.Result{}, err
	}
	if !ready {
		log.Info("rollout is not ready", "name", req.NamespacedName.Name, "namespace", req.NamespacedName.Namespace)
	}

	if err := r.patchNewPartition(ctx, sts); err != nil {
		log.Error(err, "failed to apply new partition", "name", req.NamespacedName.Name, "namespace", req.NamespacedName.Namespace)
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *StatefulSetPartitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.StatefulSet{}).
		Owns(&corev1.Pod{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}

// isRolloutReady returns true if the StatefulSet is ready for rolling update.
func (r *StatefulSetPartitionReconciler) isRolloutReady(ctx context.Context, sts *appsv1.StatefulSet) (bool, error) {
	cluster, err := r.getMySQLCluster(ctx, sts)
	if err != nil {
		return false, fmt.Errorf("failed to get MySQLCluster: %w", err)
	}

	if !r.isMySQLClusterHealthy(cluster) {
		return false, nil
	}

	return false, nil
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
	return sts.Status.CurrentRevision == sts.Status.UpdateRevision
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

// applyNewPartition applies a new partition to the StatefulSet,
// subtracting 1 from the current partition.
func (r *StatefulSetPartitionReconciler) applyNewPartition(ctx context.Context, sts *appsv1.StatefulSet) error {
	newPartition := *sts.Spec.UpdateStrategy.RollingUpdate.Partition - 1

	key := client.ObjectKey{
		Namespace: sts.Namespace,
		Name:      sts.Name,
	}

	stsApplyCfg := appsv1ac.StatefulSet(sts.Name, sts.Namespace).
		WithSpec(appsv1ac.StatefulSetSpec().
			WithUpdateStrategy(appsv1ac.StatefulSetUpdateStrategy().
				WithType(appsv1.RollingUpdateStatefulSetStrategyType).
				WithRollingUpdate(appsv1ac.RollingUpdateStatefulSetStrategy().WithPartition(newPartition)),
			),
		)

	if _, err := apply(ctx, r.Client, key, stsApplyCfg, appsv1ac.ExtractStatefulSet); err != nil {
		if errors.Is(err, ErrApplyConfigurationNotChanged) {
			return nil
		}
		return fmt.Errorf("failed to apply new partition to StatefulSet %s/%s: %w", sts.Namespace, sts.Name, err)
	}

	return nil
}

func (r *StatefulSetPartitionReconciler) patchNewPartition(ctx context.Context, sts *appsv1.StatefulSet) error {
	newPartition := *sts.Spec.UpdateStrategy.RollingUpdate.Partition - 1

	patch := client.MergeFrom(sts.DeepCopy())
	sts.Spec.UpdateStrategy.RollingUpdate.Partition = &newPartition

	if err := r.Client.Patch(ctx, sts, patch); err != nil {
		return fmt.Errorf("failed to patch new partition to StatefulSet %s/%s: %w", sts.Namespace, sts.Name, err)
	}

	return nil
}
