package v1beta1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var apiReader client.Reader

func (r *BackupPolicy) SetupWebhookWithManager(mgr ctrl.Manager) error {
	apiReader = mgr.GetAPIReader()
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/validate-moco-cybozu-com-v1beta1-backuppolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=moco.cybozu.com,resources=backuppolicies,verbs=create;update;delete,versions=v1beta1,name=vbackuppolicy.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &BackupPolicy{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *BackupPolicy) ValidateCreate() error {
	errs := r.Spec.validate()
	if len(errs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "BackupPolicy"}, r.Name, errs)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *BackupPolicy) ValidateUpdate(old runtime.Object) error {
	return r.ValidateCreate()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *BackupPolicy) ValidateDelete() error {
	clusters := &MySQLClusterList{}
	if err := apiReader.List(context.Background(), clusters, client.InNamespace(r.Namespace)); err != nil {
		return err
	}

	for _, cluster := range clusters.Items {
		if cluster.Spec.BackupPolicyName == nil {
			continue
		}

		if *cluster.Spec.BackupPolicyName == r.Name {
			return fmt.Errorf("MySQLCluster %s/%s has a reference to this policy", cluster.Namespace, cluster.Name)
		}
	}
	return nil
}
