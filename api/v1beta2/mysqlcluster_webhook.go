package v1beta2

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"math"

	"github.com/cybozu-go/moco/pkg/constants"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (r *MySQLCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&mySQLClusterAdmission{client: mgr.GetAPIReader()}).
		WithDefaulter(&mySQLClusterAdmission{client: mgr.GetAPIReader()}).
		Complete()
}

type mySQLClusterAdmission struct {
	client client.Reader
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-moco-cybozu-com-v1beta2-mysqlcluster,mutating=true,failurePolicy=fail,sideEffects=None,matchPolicy=Equivalent,groups=moco.cybozu.com,resources=mysqlclusters,verbs=create,versions=v1beta2,name=mmysqlcluster.kb.io,admissionReviewVersions=v1

var _ webhook.CustomDefaulter = &mySQLClusterAdmission{}

func (a *mySQLClusterAdmission) Default(ctx context.Context, obj runtime.Object) error {
	cluster := obj.(*MySQLCluster)

	controllerutil.AddFinalizer(cluster, constants.MySQLClusterFinalizer)

	if cluster.Spec.ServerIDBase == 0 {
		buf := make([]byte, 4) // server_id is a uint32 value
		_, err := rand.Read(buf)
		if err != nil {
			panic(err)
		}
		cluster.Spec.ServerIDBase = int32(binary.LittleEndian.Uint32(buf)&uint32(math.MaxInt32>>1)) + 1
	}

	return nil
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-moco-cybozu-com-v1beta2-mysqlcluster,mutating=false,failurePolicy=fail,sideEffects=None,matchPolicy=Equivalent,groups=moco.cybozu.com,resources=mysqlclusters,verbs=create;update,versions=v1beta2,name=vmysqlcluster.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &mySQLClusterAdmission{}

func (a *mySQLClusterAdmission) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	cluster := obj.(*MySQLCluster)

	warns, errs := cluster.Spec.validateCreate()
	if len(errs) == 0 {
		return warns, nil
	}

	return warns, apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "MySQLCluster"}, cluster.Name, errs)
}

func (a *mySQLClusterAdmission) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldCluster := oldObj.(*MySQLCluster)
	newCluster := newObj.(*MySQLCluster)

	warns, errs := newCluster.Spec.validateUpdate(ctx, a.client, oldCluster.Spec)
	if len(errs) == 0 {
		return warns, nil
	}

	return warns, apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "MySQLCluster"}, newCluster.Name, errs)
}

func (a *mySQLClusterAdmission) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
