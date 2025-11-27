package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// reconcilePVC syncs the PVC and volumeClaimTemplate labels and annotations and resizes the PVC as needed.
// The synchronization of labels and annotations always happens, regardless of the StatefulSet's state.
// Subsequently, the PVC is resized, if necessary.
//
// As the PVC template of the StatefulSet is unchangeable, the resizing of the PVC requires the following steps:
//
//  1. Rewrite PVC object's requested volume size.
//  2. Delete StatefulSet object with "--cascade=orphan" option.
//  3. The StatefulSet is recreated.
//
// This function first syncs the PVC labels and annotations with those of volumeClaimTemplate and then resizes the PVC volume size.
// The deletion and recreation of the StatefulSet are managed by reconcileV1StatefulSet().
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

	if err := r.syncPVCLabelAndAnnotationValues(ctx, cluster, &sts); err != nil {
		return err
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
	listOpts := []client.ListOption{
		client.InNamespace(sts.Namespace),
		client.MatchingLabelsSelector{Selector: selector},
	}
	if err := r.Client.List(ctx, &pvcs, listOpts...); err != nil {
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

// syncPVCLabelAndAnnotationValues compares the label and annotation values set in the MySQLCluster's VolumeClaimTemplates with those of the PVC,
// and updates the PVC's label and annotation if there are differences.
// The keys to be synced are defined in MySQLClusterReconciler's PVCSyncLabelKeys and PVCSyncAnnotationKeys.
// Does not perform additions and deletions of labels and annotations.
func (r *MySQLClusterReconciler) syncPVCLabelAndAnnotationValues(ctx context.Context, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet) error {
	if len(r.PVCSyncAnnotationKeys) == 0 && len(r.PVCSyncLabelKeys) == 0 {
		return nil
	}

	selector, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err != nil {
		return fmt.Errorf("failed to parse selector: %w", err)
	}

	var deployedPVCs corev1.PersistentVolumeClaimList
	listOpts := []client.ListOption{
		client.InNamespace(sts.Namespace),
		client.MatchingLabelsSelector{Selector: selector},
	}
	if err := r.Client.List(ctx, &deployedPVCs, listOpts...); err != nil {
		return fmt.Errorf("failed to list PVCs: %w", err)
	}

	var templatePVCSet = make(map[string]mocov1beta2.PersistentVolumeClaim, len(cluster.Spec.VolumeClaimTemplates))
	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		name := fmt.Sprintf("%s-%s", pvc.Name, sts.Name)
		templatePVCSet[name] = pvc
	}

	for _, pvc := range deployedPVCs.Items {
		index := strings.LastIndex(pvc.Name, "-")
		if index == -1 {
			continue
		}

		name := pvc.Name[:index]
		template, ok := templatePVCSet[name]
		if !ok {
			continue
		}

		var isUpdate bool

		for _, key := range r.PVCSyncLabelKeys {
			if value, ok := template.Labels[key]; ok {
				if pvc.Labels[key] != value {
					pvc.Labels[key] = value
					isUpdate = true
				}
			}
		}

		for _, key := range r.PVCSyncAnnotationKeys {
			if value, ok := template.Annotations[key]; ok {
				if pvc.Annotations[key] != value {
					pvc.Annotations[key] = value
					isUpdate = true
				}
			}
		}

		if isUpdate {
			if err := r.Client.Update(ctx, &pvc); err != nil {
				return fmt.Errorf("failed to update PVC: %w", err)
			}
		}
	}

	return nil
}
