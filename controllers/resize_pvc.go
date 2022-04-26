package controllers

import (
	"context"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
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

	resizeTarget, ok := r.needResizePVC(cluster, &sts)
	if !ok {
		return nil
	}

	log.Info("Starting PVC resize")

	patches, err := r.findPVCs(ctx, cluster, &sts, resizeTarget)
	if err != nil {
		return fmt.Errorf("failed to resize PVC: %w", err)
	}

	if err := r.resizePVCs(ctx, patches); err != nil {
		return fmt.Errorf("failed to resize PVCs: %w", err)
	}

	if err := r.deleteStatefulSet(ctx, &sts); err != nil {
		return fmt.Errorf("failed to delete StatefulSet: %w", err)
	}

	return nil
}

func (r *MySQLClusterReconciler) findPVCs(ctx context.Context, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet, resizeTarget map[string]corev1.PersistentVolumeClaim) (map[types.NamespacedName]*unstructured.Unstructured, error) {
	log := crlog.FromContext(ctx)

	newSizes := make(map[string]*resource.Quantity)
	for _, pvc := range cluster.Spec.VolumeClaimTemplates {
		newSize := pvc.Spec.Resources.Requests.Storage()
		if newSize == nil {
			continue
		}
		newSizes[pvc.Name] = newSize
	}

	pvcsToKeep := make(map[string]*resource.Quantity, int(*sts.Spec.Replicas)*len(resizeTarget))
	for _, pvc := range resizeTarget {
		for i := int32(0); i < *sts.Spec.Replicas; i++ {
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

	patches := make(map[types.NamespacedName]*unstructured.Unstructured)

	for _, pvc := range pvcs.Items {
		newSize, ok := pvcsToKeep[pvc.Name]
		if !ok {
			continue
		}

		supported, err := r.isVolumeExpansionSupported(ctx, &pvc)
		if err != nil {
			return nil, fmt.Errorf("failed to check if volume expansion is supported: %w", err)
		}
		if !supported {
			log.Info("StorageClass used by PVC does not support volume expansion, skipped", "storageClassName", *pvc.Spec.StorageClassName, "pvcName", pvc.Name)
			continue
		}

		pvcac := corev1ac.PersistentVolumeClaim(pvc.Name, pvc.Namespace).
			WithSpec(corev1ac.PersistentVolumeClaimSpec().
				WithResources(corev1ac.ResourceRequirements().
					WithRequests(corev1.ResourceList{corev1.ResourceStorage: *newSize}),
				),
			)

		obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pvcac)
		if err != nil {
			return nil, fmt.Errorf("failed to convert PVC %s/%s to unstructured: %w", pvc.Namespace, pvc.Name, err)
		}

		patches[types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}] = &unstructured.Unstructured{Object: obj}
	}

	if len(patches) == 0 {
		return nil, errors.New("could not find resizable PVCs")
	}

	return patches, nil
}

func (r *MySQLClusterReconciler) resizePVCs(ctx context.Context, patches map[types.NamespacedName]*unstructured.Unstructured) error {
	log := crlog.FromContext(ctx)

	for key, patch := range patches {
		err := r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
			FieldManager: fieldManager,
			Force:        pointer.Bool(true),
		})
		if err != nil {
			return fmt.Errorf("failed to patch PVC %s/%s: %w", key.Namespace, key.Name, err)
		}

		log.Info("Resized PVC", "pvcName", key.Name)
	}

	return nil
}

func (r *MySQLClusterReconciler) deleteStatefulSet(ctx context.Context, sts *appsv1.StatefulSet) error {
	log := crlog.FromContext(ctx)

	orphan := metav1.DeletePropagationOrphan
	if err := r.Client.Delete(ctx, sts, &client.DeleteOptions{PropagationPolicy: &orphan}); err != nil {
		return fmt.Errorf("failed to delete StatefulSet %s/%s: %w", sts.Namespace, sts.Name, err)
	}

	log.Info("Deleted StatefulSet. It will be re-created in the next reconcile loop.", "statefulSetName", sts.Name)

	return nil
}

func (r *MySQLClusterReconciler) needResizePVC(cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet) (map[string]corev1.PersistentVolumeClaim, bool) {
	if len(sts.Spec.VolumeClaimTemplates) == 0 {
		return nil, false
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

		if deployedSize.Equal(wantSize.DeepCopy()) {
			delete(pvcSet, pvc.Name)
			continue
		}
	}

	if len(pvcSet) == 0 {
		return nil, false
	}

	return pvcSet, true
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

func (_ MySQLClusterReconciler) isUpdatingStatefulSet(sts *appsv1.StatefulSet) bool {
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
