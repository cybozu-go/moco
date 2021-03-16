package v1beta1

import (
	"crypto/rand"
	"encoding/binary"
	"math"

	"github.com/cybozu-go/moco/pkg/constants"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func (r *MySQLCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-moco-cybozu-com-v1beta1-mysqlcluster,mutating=true,failurePolicy=fail,sideEffects=None,groups=moco.cybozu.com,resources=mysqlclusters,verbs=create,versions=v1beta1,name=mmysqlcluster.kb.io,admissionReviewVersions={v1,v1beta1}

var _ webhook.Defaulter = &MySQLCluster{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *MySQLCluster) Default() {
	controllerutil.AddFinalizer(r, constants.MySQLClusterFinalizer)

	if r.Spec.ServerIDBase == 0 {
		buf := make([]byte, 4) // server_id is a uint32 value
		_, err := rand.Read(buf)
		if err != nil {
			panic(err)
		}
		r.Spec.ServerIDBase = int32(binary.LittleEndian.Uint32(buf)&uint32(math.MaxInt32>>1)) + 1
	}
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-moco-cybozu-com-v1beta1-mysqlcluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=moco.cybozu.com,resources=mysqlclusters,verbs=create;update,versions=v1beta1,name=vmysqlcluster.kb.io,admissionReviewVersions={v1,v1beta1}

var _ webhook.Validator = &MySQLCluster{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *MySQLCluster) ValidateCreate() error {
	errs := r.Spec.validateCreate()
	if len(errs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "MySQLCluster"}, r.Name, errs)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *MySQLCluster) ValidateUpdate(old runtime.Object) error {
	errs := r.Spec.validateUpdate(old.(*MySQLCluster).Spec)
	if len(errs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "MySQLCluster"}, r.Name, errs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *MySQLCluster) ValidateDelete() error {
	return nil
}
