package controllers

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	if isUpdatingStatefulSet(&sts) {
		return nil
	}

	resizeTarget, ok := r.needResizePVC(cluster, &sts)
	if !ok {
		return nil
	}

	log.Info("Starting PVC resize")

	if err := r.findAndResizePVC(ctx, cluster, &sts, resizeTarget); err != nil {
		return fmt.Errorf("failed to resize PVC: %w", err)
	}

	if err := r.deleteStatefulSet(ctx, &sts); err != nil {
		return fmt.Errorf("failed to delete StatefulSet: %w", err)
	}

	return nil
}

func (r *MySQLClusterReconciler) findAndResizePVC(ctx context.Context, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet, resizeTarget map[string]corev1.PersistentVolumeClaim) error {
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

		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = *newSize

		if err := r.Client.Update(ctx, &pvc); err != nil {
			return fmt.Errorf("failed to update PVC %s/%s: %w", pvc.Namespace, pvc.Name, err)
		}

		log.Info("Resized PVC", "pvcName", pvc.Name)
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

func isUpdatingStatefulSet(sts *appsv1.StatefulSet) bool {
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
