package v1beta1

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var mysqlclusterlog = logf.Log.WithName("mysqlcluster-resource")

func (r *MySQLCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-moco-cybozu-com-cybozu-com-v1beta1-mysqlcluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=moco.cybozu.com.cybozu.com,resources=mysqlclusters,verbs=create;update,versions=v1beta1,name=vmysqlcluster.kb.io,admissionReviewVersions={v1,v1beta1}

var _ webhook.Validator = &MySQLCluster{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *MySQLCluster) ValidateCreate() error {
	mysqlclusterlog.Info("validate create", "name", r.Name)

	// TODO(user): fill in your validation logic upon object creation.
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *MySQLCluster) ValidateUpdate(old runtime.Object) error {
	mysqlclusterlog.Info("validate update", "name", r.Name)

	// TODO(user): fill in your validation logic upon object update.
	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *MySQLCluster) ValidateDelete() error {
	mysqlclusterlog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}
