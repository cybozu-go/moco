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
//   1. Rewrite PVC object requested volume size.
//   2. Delete StatefulSet object for "--cascade=orphan" option.
//   3. The StatefulSet will be re-created in the reconcileV1StatefulSet().
//
// It is preferable to execute this function before reconcileV1StatefulSet(), since the deleted StatefulSet will be immediately re-created.
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

	if err := r.resizePVCs(ctx, cluster, &sts, resizeTarget); err != nil {
		metrics.VolumeResizedErrorTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}

	metrics.VolumeResizedTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()

	return nil
}

func (r *MySQLClusterReconciler) resizePVCs(ctx context.Context, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet, resizeTarget map[string]corev1.PersistentVolumeClaim) error {
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
		return fmt.Errorf("failed to parse selector: %w", err)
	}

	var pvcs corev1.PersistentVolumeClaimList
	if err := r.Client.List(ctx, &pvcs, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return fmt.Errorf("failed to list PVCs: %w", err)
	}

	for _, pvc := range pvcs.Items {
		newSize, ok := pvcsToKeep[pvc.Name]
		if !ok {
			continue
		}

		supported, err := r.isVolumeExpansionSupported(ctx, &pvc)
		if err != nil {
			return fmt.Errorf("failed to check if volume expansion is supported: %w", err)
		}
		if !supported {
			log.Info("StorageClass used by PVC does not support volume expansion, skipped", "storageClassName", *pvc.Spec.StorageClassName, "pvcName", pvc.Name)
			continue
		}

		switch i := pvc.Spec.Resources.Requests.Storage().Cmp(*newSize); {
		case i == 0: // volume size is equal
			continue
		case i == 1: // current volume size is greater than new size
			return fmt.Errorf("failed to resize pvc %q, want size: %s, deployed size: %s: %w", pvc.Name, newSize.String(), pvc.Spec.Resources.Requests.Storage().String(), ErrReduceVolumeSize)
		case i == -1: // current volume size is smaller than new size
			pvc.Spec.Resources.Requests[corev1.ResourceStorage] = *newSize

			if err := r.Client.Update(ctx, &pvc); err != nil {
				return fmt.Errorf("failed to update PVC: %w", err)
			}
		}
	}

	return nil
}

func (*MySQLClusterReconciler) needResizePVC(cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet) (map[string]corev1.PersistentVolumeClaim, bool, error) {
	if len(sts.Spec.VolumeClaimTemplates) == 0 {
		return nil, false, nil
	}

	pvcSet := make(map[string]corev1.PersistentVolumeClaim, len(sts.Spec.VolumeClaimTemplates))
	for _, pvc := range sts.Spec.VolumeClaimTemplates {
		pvcSet[pvc.Name] = pvc
	}

	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		if _, ok := pvcSet[pvc.Name]; !ok {
			delete(pvcSet, pvc.Name)
			continue
		}

		current := pvcSet[pvc.Name]

		deployedSize := current.Spec.Resources.Requests.Storage()
		wantSize := pvc.Spec.Resources.Requests.Storage()

		switch i := deployedSize.Cmp(wantSize.DeepCopy()); {
		case i == 0: // volume size is equal
			delete(pvcSet, pvc.Name)
			continue
		case i == 1: // volume size is greater
			return nil, false, fmt.Errorf("failed to resize pvc %q, want size: %s, deployed size: %s: %w", pvc.Name, wantSize, deployedSize, ErrReduceVolumeSize)
		case i == -1: // volume size is smaller
			continue
		}
	}

	if len(pvcSet) == 0 {
		return nil, false, nil
	}

	return pvcSet, true, nil
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

func (*MySQLClusterReconciler) isUpdatingStatefulSet(sts *appsv1.StatefulSet) bool {
	if sts.Status.ObservedGeneration == 0 {
		return false
	}

	if sts.Status.CurrentRevision != sts.Status.UpdateRevision {
		return true
	}

	if sts.Generation > sts.Status.ObservedGeneration && *sts.Spec.Replicas == sts.Status.Replicas {
		return true
	}

	return false
}
