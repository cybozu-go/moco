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

	// The target MySQLCluster (same name, same namespace) must exist,
	// must not be terminating, and must have replicas > 0.
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
		if cluster.DeletionTimestamp != nil {
			errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), cr.Name,
				"target MySQLCluster is being deleted"))
		}
		if cluster.Spec.Replicas <= 0 {
			errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), cr.Name,
				"target MySQLCluster must have replicas > 0"))
		}
		if cluster.Spec.Offline {
			errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), cr.Name,
				"target MySQLCluster is offline; the rotation needs running mysqld instances"))
		}
	}

	// The discard must be requested via update after the verification
	// window opens; true at create time would skip the window.
	if cr.Spec.Discard {
		errs = append(errs, field.Invalid(field.NewPath("spec", "discard"),
			cr.Spec.Discard, "must be false at create time; request the discard via update once DiscardReady=True"))
	}

	if len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "CredentialRotation"}, cr.Name, errs)
	}
	return nil, nil
}

func (a *credentialRotationAdmission) ValidateUpdate(ctx context.Context, oldCR, newCR *CredentialRotation) (admission.Warnings, error) {
	var errs field.ErrorList

	// The spec is immutable except for the one-way discard flip.
	if oldCR.Spec.Discard && !newCR.Spec.Discard {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "discard"),
			"cannot be set back to false"))
	}

	if !oldCR.Spec.Discard && newCR.Spec.Discard {
		// The discard may only be requested while the verification window
		// is open. Reading the status during admission is best-effort
		// against concurrent status writes; the controllers re-check the
		// actual state at execution time, so a stale admission decision
		// can only delay the discard, never corrupt it.
		if !oldCR.IsDiscardReady() {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "discard"),
				"can only be set while the verification window is open (DiscardReady=True)"))
		}

		// A discard request against a stale CR (a leftover from a deleted
		// cluster) would be admitted and then silently ignored by the
		// controllers; reject it here instead.
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
		} else {
			if oldCR.IsStaleFor(cluster) {
				errs = append(errs, field.Forbidden(field.NewPath("spec", "discard"),
					"this CredentialRotation is a leftover from a previous MySQLCluster; delete it"))
			}
			// All rotation handlers no-op while the cluster is terminating,
			// so the flip would be admitted and then silently ignored.
			if cluster.DeletionTimestamp != nil {
				errs = append(errs, field.Forbidden(field.NewPath("spec", "discard"),
					"cannot request the discard while the MySQLCluster is being deleted"))
			}
			// The flag is one-way: admitting it while the cluster runs no
			// mysqld instances (0 replicas, or offline — which scales the
			// StatefulSet down to zero Pods) would run the discard
			// automatically when the cluster comes back, without another
			// operator confirmation. The 0-replica case is unreachable
			// through the normal API (the MySQLCluster webhook forbids 0
			// replicas) and kept as defense in depth.
			if cluster.IsOfflineOrZeroReplicas() {
				errs = append(errs, field.Forbidden(field.NewPath("spec", "discard"),
					"cannot request the discard while the MySQLCluster runs no mysqld instances (0 replicas or offline)"))
			}
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
// current passwords, so deleting the CR at any phase never breaks
// connectivity. Mid-cycle deletion can only leave residue (dual passwords in
// MySQL, stale bookkeeping keys in the controller Secret); the next CR adopts
// pending residue and fails on promoted residue until the documented recovery
// procedure cleans it up. The webhook is not registered for the delete verb,
// so this method exists only to satisfy the admission.Validator interface.
func (a *credentialRotationAdmission) ValidateDelete(_ context.Context, _ *CredentialRotation) (admission.Warnings, error) {
	return nil, nil
}
