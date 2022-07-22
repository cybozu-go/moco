package v1beta2

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

func (r *BackupPolicy) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&backupPolicyAdmission{client: mgr.GetAPIReader()}).
		Complete()
}

type backupPolicyAdmission struct {
	client client.Reader
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/validate-moco-cybozu-com-v1beta2-backuppolicy,mutating=false,failurePolicy=fail,sideEffects=None,matchPolicy=Equivalent,groups=moco.cybozu.com,resources=backuppolicies,verbs=create;update;delete,versions=v1beta2,name=vbackuppolicy.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &backupPolicyAdmission{}

func (a *backupPolicyAdmission) ValidateCreate(ctx context.Context, obj runtime.Object) error {
	policy := obj.(*BackupPolicy)

	errs := policy.Spec.validate()
	if len(errs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "BackupPolicy"}, policy.Name, errs)
}

func (a *backupPolicyAdmission) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) error {
	return a.ValidateCreate(ctx, newObj)
}

func (a *backupPolicyAdmission) ValidateDelete(ctx context.Context, obj runtime.Object) error {
	policy := obj.(*BackupPolicy)

	clusters := &MySQLClusterList{}
	if err := a.client.List(context.Background(), clusters, client.InNamespace(policy.Namespace)); err != nil {
		return err
	}

	for _, cluster := range clusters.Items {
		if cluster.Spec.BackupPolicyName == nil {
			continue
		}

		if *cluster.Spec.BackupPolicyName == policy.Name {
			return fmt.Errorf("MySQLCluster %s/%s has a reference to this policy", cluster.Namespace, cluster.Name)
		}
	}
	return nil
}
