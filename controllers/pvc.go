package controllers

import (
	"context"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/metrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	ErrReduceVolumeSize = errors.New("cannot reduce volume size")
)

// reconcilePVC resizes the PVC as needed.
// Since the PVC template of the StatefulSet is unchangeable, the following steps are required to resize the PVC
//
//  1. Rewrite PVC object requested volume size.
//  2. Delete StatefulSet object for "--cascade=orphan" option.
//  3. The StatefulSet will be re-created.
//
// This function rewrites the PVC volume size.
// StatefulSet deletion and re-creation is done by reconcileV1StatefulSet().
// Therefore, this function should be called before reconcileV1StatefulSet().
func (r *MySQLClusterReconciler) reconcilePVC(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	var sts appsv1.StatefulSet
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.PrefixedName()}, &sts)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get StatefulSet %s/%s: %w", cluster.Namespace, cluster.PrefixedName(), err)
	} else if apierrors.IsNotFound(err) {
		return nil
	}

	if r.isUpdatingStatefulSet(&sts) {
		return nil
	}

	resizeTarget, ok, err := r.needResizePVC(cluster, &sts)
	if !ok {
		return nil
	}
	if err != nil {
		return err
	}

	log.Info("Starting PVC resize")

	resized, err := r.resizePVCs(ctx, cluster, &sts, resizeTarget)
	if err != nil {
		metrics.VolumeResizedErrorTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}

	if len(resized) == 0 {
		return nil
	}

	metrics.VolumeResizedTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()

	return nil
}

func (r *MySQLClusterReconciler) resizePVCs(ctx context.Context, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet, resizeTarget map[string]corev1.PersistentVolumeClaim) (map[string]corev1.PersistentVolumeClaim, error) {
	log := crlog.FromContext(ctx)

	newSizes := make(map[string]*resource.Quantity)
	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		newSize := pvc.Spec.Resources.Requests.Storage()
		if newSize == nil {
			continue
		}
		newSizes[pvc.Name] = newSize
	}

	var replicas int32
	if sts.Spec.Replicas == nil {
		replicas = 1
	} else {
		replicas = *sts.Spec.Replicas
	}
	pvcsToKeep := make(map[string]*resource.Quantity, replicas*int32(len(resizeTarget)))
	for _, pvc := range resizeTarget {
		for i := int32(0); i < replicas; i++ {
			name := fmt.Sprintf("%s-%s-%d", pvc.Name, sts.Name, i)
			newSize := newSizes[pvc.Name]
			pvcsToKeep[name] = newSize
		}
	}

	selector, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to parse selector: %w", err)
	}

	var pvcs corev1.PersistentVolumeClaimList
	if err := r.Client.List(ctx, &pvcs, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list PVCs: %w", err)
	}

	resizedPVC := make(map[string]corev1.PersistentVolumeClaim)

	for _, pvc := range pvcs.Items {
		newSize, ok := pvcsToKeep[pvc.Name]
		if !ok {
			continue
		}

		supported, err := r.isVolumeExpansionSupported(ctx, &pvc)
		if err != nil {
			return resizedPVC, fmt.Errorf("failed to check if volume expansion is supported: %w", err)
		}
		if !supported {
			log.Info("StorageClass used by PVC does not support volume expansion, skipped", "storageClassName", *pvc.Spec.StorageClassName, "pvcName", pvc.Name)
			continue
		}

		switch i := pvc.Spec.Resources.Requests.Storage().Cmp(*newSize); {
		case i == 0: // volume size is equal
			continue
		case i == 1: // current volume size is greater than new size
			// The size of the Persistent Volume Claims (PVC) cannot be reduced.
			// Although MOCO permits the reduction of PVC, an automatic resize is not performed.
			// An error arises if a PVC involving reduction is passed, as it's unexpected.
			return resizedPVC, fmt.Errorf("failed to resize pvc %q, want size: %s, deployed size: %s: %w", pvc.Name, newSize.String(), pvc.Spec.Resources.Requests.Storage().String(), ErrReduceVolumeSize)
		case i == -1: // current volume size is smaller than new size
			pvc.Spec.Resources.Requests[corev1.ResourceStorage] = *newSize

			if err := r.Client.Update(ctx, &pvc); err != nil {
				return resizedPVC, fmt.Errorf("failed to update PVC: %w", err)
			}

			log.Info("PVC resized", "pvcName", pvc.Name)

			resizedPVC[pvc.Name] = pvc
		}
	}

	return resizedPVC, nil
}

func (*MySQLClusterReconciler) needResizePVC(cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet) (map[string]corev1.PersistentVolumeClaim, bool, error) {
	if len(sts.Spec.VolumeClaimTemplates) == 0 {
		return nil, false, nil
	}

	pvcSet := make(map[string]corev1.PersistentVolumeClaim, len(sts.Spec.VolumeClaimTemplates))
	for _, pvc := range sts.Spec.VolumeClaimTemplates {
		pvcSet[pvc.Name] = pvc
	}

	resizeTarget := make(map[string]corev1.PersistentVolumeClaim)

	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		if _, ok := pvcSet[pvc.Name]; !ok {
			continue
		}

		current := pvcSet[pvc.Name]

		deployedSize := current.Spec.Resources.Requests.Storage()
		wantSize := pvc.Spec.Resources.Requests.Storage()

		switch i := deployedSize.Cmp(wantSize.DeepCopy()); {
		case i == 0: // volume size is equal
			continue
		case i == 1: // volume size is greater
			// Due to the lack of support for volume size reduction, resizing will not be executed if it implies a smaller size.
			// It's important to highlight that this does not induce an error.
			// Instead, the recreation of the StatefulSet will be managed in the reconcileV1StatefulSet() operation, which follows this one.
			// Hence, the execution flow remains uninterrupted.
			// ref: docs/designdoc/support_reduce_volume_size.md
			continue
		case i == -1: // volume size is smaller
			resizeTarget[pvc.Name] = pvcSet[pvc.Name]
			continue
		}
	}

	if len(resizeTarget) == 0 {
		return nil, false, nil
	}

	return resizeTarget, true, nil
}

func (r *MySQLClusterReconciler) isVolumeExpansionSupported(ctx context.Context, pvc *corev1.PersistentVolumeClaim) (bool, error) {
	if pvc.Spec.StorageClassName == nil {
		return false, nil
	}

	var storageClass storagev1.StorageClass
	if err := r.Client.Get(ctx, types.NamespacedName{Name: *pvc.Spec.StorageClassName}, &storageClass); err != nil {
		return false, fmt.Errorf("failed to get StorageClass %s: %w", *pvc.Spec.StorageClassName, err)
	}

	if storageClass.AllowVolumeExpansion == nil {
		return false, nil
	}

	return *storageClass.AllowVolumeExpansion, nil
}

// isUpdatingStatefulSet returns whether the StatefulSet is being updated or not.
// refs: https://github.com/kubernetes/kubectl/blob/v0.24.2/pkg/polymorphichelpers/rollout_status.go#L119-L152
func (*MySQLClusterReconciler) isUpdatingStatefulSet(sts *appsv1.StatefulSet) bool {
	// Waiting for StatefulSet to be deleting.
	if sts.DeletionTimestamp != nil {
		return true
	}

	// Waiting for StatefulSet spec update to be observed
	if sts.Status.ObservedGeneration == 0 || sts.Generation > sts.Status.ObservedGeneration {
		return true
	}
	// Waiting for Pods to be ready
	if sts.Spec.Replicas != nil && sts.Status.ReadyReplicas < *sts.Spec.Replicas {
		return true
	}
	// Waiting for partitioned rollout to finish
	if sts.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType && sts.Spec.UpdateStrategy.RollingUpdate != nil {
		if sts.Spec.Replicas != nil && sts.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
			if sts.Status.UpdatedReplicas < (*sts.Spec.Replicas - *sts.Spec.UpdateStrategy.RollingUpdate.Partition) {
				return true
			}
		}
	}
	// Waiting for StatefulSet rolling update to complete
	if sts.Status.UpdateRevision != sts.Status.CurrentRevision {
		return true
	}

	return false
}
