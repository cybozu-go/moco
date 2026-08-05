package v1beta2

import (
	"context"

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

//+kubebuilder:webhook:path=/validate-moco-cybozu-com-v1beta2-credentialrotation,mutating=false,failurePolicy=fail,sideEffects=None,matchPolicy=Equivalent,groups=moco.cybozu.com,resources=credentialrotations,verbs=create;update,versions=v1beta2,name=vcredentialrotation.kb.io,admissionReviewVersions=v1

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

	// rotationGeneration must be exactly 1 at create time. The counter is
	// a CR-lifetime measure of completed cycles and must start at 1 so
	// rotationGeneration == observedRotationGeneration reliably reports
	// the number of rotations performed against this CR.
	if cr.Spec.RotationGeneration != 1 {
		errs = append(errs, field.Invalid(field.NewPath("spec", "rotationGeneration"),
			cr.Spec.RotationGeneration, "must be 1 at create time"))
	}

	// discardGeneration must be 0 at create time. A non-zero value would
	// short-circuit the cycle straight into ApplyingDiscard, skipping the
	// AwaitingRollout wait that gates DiscardReady=True. Discard must
	// always be requested via update once the CR reaches the
	// awaiting-discard steady state (see ValidateUpdate).
	if cr.Spec.DiscardGeneration != 0 {
		errs = append(errs, field.Invalid(field.NewPath("spec", "discardGeneration"),
			cr.Spec.DiscardGeneration, "must be 0 at create time"))
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
		// rotationGeneration can only increase when the CR is idle —
		// i.e. no rotation cycle is in flight and MySQL is not holding
		// dual passwords. Stuck states clear themselves first: Blocked
		// resumes on scale-up, and Stale recovers automatically after
		// the operator cleans the controller Secret (see the recovery
		// procedures in the design doc).
		if !oldCR.IsIdle() {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "rotationGeneration"),
				"can only increment rotationGeneration when the CR is idle (RotationReady=True, DiscardReady=False, DualPassword=False, or the previous request was Refused without mutations)"))
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

	if discardIncreased {
		// discardGeneration can only increase while the CR is in the
		// awaiting-discard steady state (rotation phase done, MySQL
		// still holding dual passwords, no discard in flight). This also
		// rejects increasing both generations in one update: the old CR
		// is idle then, and allowing it would derive Step=ApplyingDiscard
		// right after the rotation phase promotes
		// observedRotationGeneration, skipping the AwaitingRollout gate —
		// DISCARD could run while Pods still authenticate with the old
		// password.
		if !oldCR.IsAwaitingDiscard() {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "discardGeneration"),
				"can only increment discardGeneration when the CR is awaiting discard (RotationReady=False, DiscardReady=True, DualPassword=True; post-distribute rollout has settled)"))
		}
	}

	if len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "CredentialRotation"}, newCR.Name, errs)
	}
	return nil, nil
}

// ValidateDelete always allows deletion. The core invariant — the controller
// Secret's current passwords always authenticate on every instance — makes CR
// deletion non-destructive: per-namespace Secrets always hold the canonical
// current passwords, so deleting the CR at any step never breaks
// connectivity. Mid-cycle deletion can only leave residue (dual passwords in
// MySQL, stale bookkeeping keys in the controller Secret) that blocks the
// next rotation cycle until the documented recovery procedure cleans it up.
// The webhook is not registered for the delete verb, so this method exists
// only to satisfy the admission.Validator interface.
func (a *credentialRotationAdmission) ValidateDelete(_ context.Context, _ *CredentialRotation) (admission.Warnings, error) {
	return nil, nil
}
