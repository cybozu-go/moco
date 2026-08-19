package v1beta2

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/cybozu-go/moco/pkg/constants"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func (r *MySQLCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(&mySQLClusterAdmission{client: mgr.GetAPIReader()}).
		WithDefaulter(&mySQLClusterAdmission{client: mgr.GetAPIReader()}).
		Complete()
}

type mySQLClusterAdmission struct {
	client client.Reader
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-moco-cybozu-com-v1beta2-mysqlcluster,mutating=true,failurePolicy=fail,sideEffects=None,matchPolicy=Equivalent,groups=moco.cybozu.com,resources=mysqlclusters,verbs=create,versions=v1beta2,name=mmysqlcluster.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*MySQLCluster] = &mySQLClusterAdmission{}

func (a *mySQLClusterAdmission) Default(ctx context.Context, cluster *MySQLCluster) error {
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

var _ admission.Validator[*MySQLCluster] = &mySQLClusterAdmission{}

func (a *mySQLClusterAdmission) ValidateCreate(ctx context.Context, cluster *MySQLCluster) (admission.Warnings, error) {
	var errs field.ErrorList

	// The names for StatefulSet and CronJob must be 52 characters or less.
	// MOCO uses "moco-" as a prefix for StatefulSet names and "moco-backup-" for CronJob names.
	// Therefore, the cluster name must be 40 characters or less.
	// refs: https://github.com/kubernetes/kubernetes/issues/64023
	if len(cluster.Name) > 40 {
		errs = append(errs, field.Invalid(field.NewPath("metadata").Child("name"), cluster.Name, "required name must be 40 characters or less"))
	}

	warns, createErrs := cluster.Spec.validateCreate()
	errs = append(errs, createErrs...)
	if len(errs) == 0 {
		return warns, nil
	}

	return warns, apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "MySQLCluster"}, cluster.Name, errs)
}

func (a *mySQLClusterAdmission) ValidateUpdate(ctx context.Context, oldCluster, newCluster *MySQLCluster) (admission.Warnings, error) {
	warns, errs := newCluster.Spec.validateUpdate(ctx, a.client, oldCluster.Spec)
	errs = append(errs, a.validateAgainstActiveRotation(ctx, oldCluster, newCluster)...)
	if len(errs) == 0 {
		return warns, nil
	}

	return warns, apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "MySQLCluster"}, newCluster.Name, errs)
}

// validateAgainstActiveRotation freezes spec.replicas and spec.offline while
// an active CredentialRotation targets this cluster. Every rotation flow
// depends on the set of running mysqld instances staying stable: a scale-up
// forces the RETAIN/DISCARD catch-up passes, and taking the cluster offline
// parks the cycle in Blocked (or holds AwaitingRollout). Rejecting the change
// at admission turns those mid-cycle surprises into an explicit error. The
// runtime handling stays as defense in depth: this read is racy (a
// CredentialRotation can be admitted concurrently with this update), so the
// controllers keep re-checking the live cluster at execution time.
//
// Exemptions:
//   - setting spec.offline back to false is always allowed — it only
//     returns mysqld instances to the rotation, and it is the recovery
//     path out of any offline state (rejecting it could deadlock a cluster
//     that went offline through this check's race window),
//   - scaling up from 0 replicas is always allowed for the same reason —
//     it is the only API path out of the 0-replica AwaitingRollout hold
//     (0 replicas itself needs a bypassed webhook, but the way back must
//     not),
//   - a stale CR (a leftover from a previous cluster incarnation) never
//     blocks — it is inert and only waits for deletion,
//   - a terminal CR (Succeeded or Failed) never blocks — nothing runs
//     anymore, and recovering from Failed may require these very changes,
//   - a Blocked CR never blocks — its resume path is itself a
//     replicas/offline change.
func (a *mySQLClusterAdmission) validateAgainstActiveRotation(ctx context.Context, oldCluster, newCluster *MySQLCluster) field.ErrorList {
	replicasChanged := newCluster.Spec.Replicas != oldCluster.Spec.Replicas && oldCluster.Spec.Replicas != 0
	offlineTurnedOn := !oldCluster.Spec.Offline && newCluster.Spec.Offline
	if !replicasChanged && !offlineTurnedOn {
		return nil
	}

	cr := &CredentialRotation{}
	err := a.client.Get(ctx, types.NamespacedName{
		Namespace: newCluster.Namespace,
		Name:      newCluster.Name,
	}, cr)
	switch {
	case apierrors.IsNotFound(err):
		return nil
	case apimeta.IsNoMatchError(err):
		// The CredentialRotation CRD is not installed (e.g. the controller
		// rolled out before the CRD during an upgrade); there can be no
		// rotation to protect.
		return nil
	case err != nil:
		return field.ErrorList{field.InternalError(field.NewPath("spec"),
			fmt.Errorf("failed to check for an active CredentialRotation: %w", err))}
	}
	if cr.IsStaleFor(newCluster) || cr.IsTerminal() || cr.Status.Phase == PhaseBlocked {
		return nil
	}

	msg := fmt.Sprintf("cannot be changed while the CredentialRotation %q is active (phase: %q); wait for the rotation to finish, or delete the CredentialRotation to abandon it", cr.Name, cr.Status.Phase)
	var errs field.ErrorList
	if replicasChanged {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "replicas"), msg))
	}
	if offlineTurnedOn {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "offline"), msg))
	}
	return errs
}

func (a *mySQLClusterAdmission) ValidateDelete(ctx context.Context, _ *MySQLCluster) (admission.Warnings, error) {
	return nil, nil
}
