package v1beta2

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (r *CredentialRotation) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&credentialRotationAdmission{client: mgr.GetAPIReader()}).
		Complete()
}

type credentialRotationAdmission struct {
	client client.Reader
}

//+kubebuilder:webhook:path=/validate-moco-cybozu-com-v1beta2-credentialrotation,mutating=false,failurePolicy=fail,sideEffects=None,matchPolicy=Equivalent,groups=moco.cybozu.com,resources=credentialrotations,verbs=create;update,versions=v1beta2,name=vcredentialrotation.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &credentialRotationAdmission{}

func (a *credentialRotationAdmission) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	cr := obj.(*CredentialRotation)

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

	// discardOldPassword must be false
	if cr.Spec.DiscardOldPassword {
		errs = append(errs, field.Invalid(field.NewPath("spec", "discardOldPassword"),
			cr.Spec.DiscardOldPassword, "must be false on creation"))
	}

	if len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "CredentialRotation"}, cr.Name, errs)
	}
	return nil, nil
}

func (a *credentialRotationAdmission) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldCR := oldObj.(*CredentialRotation)
	newCR := newObj.(*CredentialRotation)

	var errs field.ErrorList

	// rotationGeneration must be monotonically increasing
	if newCR.Spec.RotationGeneration < oldCR.Spec.RotationGeneration {
		errs = append(errs, field.Invalid(field.NewPath("spec", "rotationGeneration"),
			newCR.Spec.RotationGeneration, "must be >= previous value (monotonically increasing)"))
	}

	generationChanged := newCR.Spec.RotationGeneration > oldCR.Spec.RotationGeneration

	if generationChanged {
		// rotationGeneration can only increase when Phase is "" or Completed
		phase := oldCR.Status.Phase
		if phase != "" && phase != RotationPhaseCompleted {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "rotationGeneration"),
				"can only increment rotationGeneration when phase is empty or Completed"))
		}

		// When rotationGeneration increases, discardOldPassword must be false
		if newCR.Spec.DiscardOldPassword {
			errs = append(errs, field.Invalid(field.NewPath("spec", "discardOldPassword"),
				newCR.Spec.DiscardOldPassword, "must be false when incrementing rotationGeneration"))
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
	} else {
		// When rotationGeneration is unchanged, discardOldPassword can only go false→true
		if oldCR.Spec.DiscardOldPassword && !newCR.Spec.DiscardOldPassword {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "discardOldPassword"),
				"cannot change from true to false without incrementing rotationGeneration"))
		}

		// discardOldPassword=true requires Phase==Rotated
		if newCR.Spec.DiscardOldPassword && !oldCR.Spec.DiscardOldPassword {
			if oldCR.Status.Phase != RotationPhaseRotated {
				errs = append(errs, field.Forbidden(field.NewPath("spec", "discardOldPassword"),
					"can only set to true when phase is Rotated"))
			}
		}
	}

	if len(errs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "CredentialRotation"}, newCR.Name, errs)
	}
	return nil, nil
}

func (a *credentialRotationAdmission) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
