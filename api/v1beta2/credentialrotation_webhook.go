package v1beta2

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (r *CredentialRotation) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(&credentialRotationAdmission{client: mgr.GetAPIReader()}).
		Complete()
}

type credentialRotationAdmission struct {
	client client.Reader
}

//+kubebuilder:webhook:path=/validate-moco-cybozu-com-v1beta2-credentialrotation,mutating=false,failurePolicy=fail,sideEffects=None,matchPolicy=Equivalent,groups=moco.cybozu.com,resources=credentialrotations,verbs=create;update;delete,versions=v1beta2,name=vcredentialrotation.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*CredentialRotation] = &credentialRotationAdmission{}

func (a *credentialRotationAdmission) ValidateCreate(ctx context.Context, cr *CredentialRotation) (admission.Warnings, error) {
	var errs field.ErrorList

	// MySQLCluster with the same name must exist
	cluster := &MySQLCluster{}
	if err := a.client.Get(ctx, types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      cr.Name,
	}, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), cr.Name,
				"MySQLCluster with the same name must exist in the same namespace"))
		} else {
			errs = append(errs, field.InternalError(field.NewPath("metadata", "name"), err))
		}
	} else {
		// Replicas must be > 0
		if cluster.Spec.Replicas <= 0 {
			errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), cr.Name,
				"target MySQLCluster must have replicas > 0"))
		}
	}

	// rotationGeneration must be > 0
	if cr.Spec.RotationGeneration <= 0 {
		errs = append(errs, field.Invalid(field.NewPath("spec", "rotationGeneration"),
			cr.Spec.RotationGeneration, "must be > 0"))
	}

	// discardGeneration must be in [0, rotationGeneration]
	if cr.Spec.DiscardGeneration < 0 {
		errs = append(errs, field.Invalid(field.NewPath("spec", "discardGeneration"),
			cr.Spec.DiscardGeneration, "must be >= 0"))
	}
	if cr.Spec.DiscardGeneration > cr.Spec.RotationGeneration {
		errs = append(errs, field.Invalid(field.NewPath("spec", "discardGeneration"),
			cr.Spec.DiscardGeneration, "must be <= rotationGeneration"))
	}

	if len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "CredentialRotation"}, cr.Name, errs)
	}
	return nil, nil
}

func (a *credentialRotationAdmission) ValidateUpdate(ctx context.Context, oldCR, newCR *CredentialRotation) (admission.Warnings, error) {
	var errs field.ErrorList

	// rotationGeneration must be monotonically increasing
	if newCR.Spec.RotationGeneration < oldCR.Spec.RotationGeneration {
		errs = append(errs, field.Invalid(field.NewPath("spec", "rotationGeneration"),
			newCR.Spec.RotationGeneration, "must be >= previous value (monotonically increasing)"))
	}
	// discardGeneration must be monotonically increasing
	if newCR.Spec.DiscardGeneration < oldCR.Spec.DiscardGeneration {
		errs = append(errs, field.Invalid(field.NewPath("spec", "discardGeneration"),
			newCR.Spec.DiscardGeneration, "must be >= previous value (monotonically increasing)"))
	}
	// discardGeneration must not exceed rotationGeneration
	if newCR.Spec.DiscardGeneration > newCR.Spec.RotationGeneration {
		errs = append(errs, field.Invalid(field.NewPath("spec", "discardGeneration"),
			newCR.Spec.DiscardGeneration, "must be <= rotationGeneration"))
	}

	rotationIncreased := newCR.Spec.RotationGeneration > oldCR.Spec.RotationGeneration
	discardIncreased := newCR.Spec.DiscardGeneration > oldCR.Spec.DiscardGeneration

	if rotationIncreased {
		// rotationGeneration can only increase when Phase is "" or Completed
		phase := oldCR.Status.Phase
		if phase != "" && phase != RotationPhaseCompleted {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "rotationGeneration"),
				"can only increment rotationGeneration when phase is empty or Completed"))
		}

		// Check MySQLCluster replicas > 0
		cluster := &MySQLCluster{}
		if err := a.client.Get(ctx, types.NamespacedName{
			Namespace: newCR.Namespace,
			Name:      newCR.Name,
		}, cluster); err != nil {
			if apierrors.IsNotFound(err) {
				errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), newCR.Name,
					"MySQLCluster with the same name must exist"))
			} else {
				errs = append(errs, field.InternalError(field.NewPath("metadata", "name"), err))
			}
		} else if cluster.Spec.Replicas <= 0 {
			errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), newCR.Name,
				"target MySQLCluster must have replicas > 0"))
		}
	}

	if discardIncreased && !rotationIncreased {
		// discardGeneration can only increase when Phase is Rotated
		// (during the same rotation cycle in which discard is being requested).
		if oldCR.Status.Phase != RotationPhaseRotated {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "discardGeneration"),
				"can only increment discardGeneration when phase is Rotated"))
		}
	}

	if len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "CredentialRotation"}, newCR.Name, errs)
	}
	return nil, nil
}

// hasStaleClusterOwnerRef reports whether cr carries a MySQLCluster owner
// reference that points at a UID different from cluster.UID, with no matching
// reference. That signals the CR is left over from a deleted cluster that has
// since been recreated under the same name.
func hasStaleClusterOwnerRef(cr *CredentialRotation, cluster *MySQLCluster) bool {
	hasStale := false
	for _, ref := range cr.OwnerReferences {
		if ref.Kind != "MySQLCluster" {
			continue
		}
		if ref.UID == cluster.UID {
			return false
		}
		hasStale = true
	}
	return hasStale
}

func (a *credentialRotationAdmission) ValidateDelete(ctx context.Context, cr *CredentialRotation) (admission.Warnings, error) {
	// Always allow deletion when the rotation is idle/completed.
	switch cr.Status.Phase {
	case "", RotationPhaseCompleted:
		return nil, nil
	}

	// Allow garbage-collection deletes: if the owning MySQLCluster is gone,
	// the CR has nothing to act on and must be reclaimable. Blocking GC here
	// would orphan the CR and poison a future cluster recreated with the
	// same name (it would inherit a stale in-progress rotation).
	cluster := &MySQLCluster{}
	err := a.client.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}, cluster)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	// The cluster is being torn down. Owner references use blockOwnerDeletion,
	// so Kubernetes GC must be allowed to remove this CR; otherwise the
	// MySQLCluster would be stuck in Terminating until the rotation finishes.
	if cluster.DeletionTimestamp != nil {
		return nil, nil
	}
	// The cluster lookup matches by name only; if the live cluster has a
	// different UID than the CR's owner, the original cluster has been deleted
	// and replaced. The CR is stale and must be deletable so it does not
	// poison the new cluster with leftover rotation state.
	if hasStaleClusterOwnerRef(cr, cluster) {
		return nil, nil
	}

	// Otherwise forbid: a deletion mid-rotation abandons the workflow,
	// leaving pending/dual passwords on instances with no automatic recovery.
	errs := field.ErrorList{
		field.Forbidden(field.NewPath("status", "phase"),
			fmt.Sprintf("cannot delete CredentialRotation while phase is %q; wait for the rotation to reach Completed", cr.Status.Phase)),
	}
	return nil, apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "CredentialRotation"}, cr.Name, errs)
}
