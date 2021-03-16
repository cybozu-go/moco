// +build old

package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"path/filepath"
	"time"

	"github.com/cybozu-go/moco/accessor"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/metrics"
	"github.com/cybozu-go/moco/operators"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	passwordBytes = 16

	defaultTerminationGracePeriodSeconds = 300
)

// OldMySQLClusterReconciler reconciles a MySQLCluster object
type OldMySQLClusterReconciler struct {
	client.Client
	Log                      logr.Logger
	Recorder                 record.EventRecorder
	Scheme                   *runtime.Scheme
	BinaryCopyContainerImage string
	FluentBitImage           string
	AgentAccessor            *accessor.AgentAccessor
	MySQLAccessor            accessor.DataBaseAccessor
	WaitTime                 time.Duration
	SystemNamespace          string
}

// +kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update
// +kubebuilder:rbac:groups="",resources=pods/status,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status,verbs=get
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services/status,verbs=get
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts/status,verbs=get
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps/status,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete

// Reconcile reconciles MySQLCluster.
func (r *OldMySQLClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("MySQLCluster", req.NamespacedName)

	cluster := &mocov1beta1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if k8serror.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch MySQLCluster")
		return ctrl.Result{}, err
	}

	if cluster.DeletionTimestamp == nil {
		if !controllerutil.ContainsFinalizer(cluster, constants.MySQLClusterFinalizer) {
			cluster2 := cluster.DeepCopy()
			controllerutil.AddFinalizer(cluster2, constants.MySQLClusterFinalizer)
			patch := client.MergeFrom(cluster)
			if err := r.Patch(ctx, cluster2, patch); err != nil {
				log.Error(err, "failed to add finalizer")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		// initialize
		metrics.UpdateOperationPhase(cluster.Name, constants.PhaseInitializing)
		isUpdated, err := r.reconcileInitialize(ctx, log, cluster)
		if err != nil {
			setCondition(&cluster.Status.Conditions, mocov1beta1.MySQLClusterCondition{
				Type: mocov1beta1.ConditionInitialized, Status: corev1.ConditionFalse, Reason: "reconcileInitializeFailed", Message: err.Error()})
			if errUpdate := r.Status().Update(ctx, cluster); errUpdate != nil {
				log.Error(err, "failed to status update", "status", cluster.Status)
			}
			log.Error(err, "failed to initialize MySQLCluster")

			r.Recorder.Eventf(cluster, corev1.EventTypeNormal, event.EventInitializationFailed.Reason, event.EventInitializationFailed.Message, err)

			return ctrl.Result{}, err
		}
		if isUpdated {
			setCondition(&cluster.Status.Conditions, mocov1beta1.MySQLClusterCondition{
				Type: mocov1beta1.ConditionInitialized, Status: corev1.ConditionTrue})
			if err := r.Status().Update(ctx, cluster); err != nil {
				log.Error(err, "failed to status update", "status", cluster.Status)

				r.Recorder.Eventf(cluster, corev1.EventTypeNormal, event.EventInitializationFailed.Reason, event.EventInitializationFailed.Message, err)

				return ctrl.Result{}, err
			}

			r.Recorder.Event(cluster, event.EventInitializationSucceeded.Type, event.EventInitializationSucceeded.Reason, event.EventInitializationSucceeded.Message)
			return ctrl.Result{Requeue: true}, nil
		}
		metrics.UpdateTotalReplicasMetrics(cluster.Name, cluster.Spec.Replicas)

		// clustering
		result, err := r.reconcileClustering(ctx, log, cluster)
		if err != nil {
			log.Info("failed to ready MySQLCluster", "err", err)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		return result, nil
	}

	// finalization
	if !controllerutil.ContainsFinalizer(cluster, constants.MySQLClusterFinalizer) {
		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, nil
	}

	log.Info("start finalizing MySQLCluster")
	err := r.reconcileFinalize(ctx, log, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	metrics.DeleteAllControllerMetrics(cluster.Name)

	cluster2 := cluster.DeepCopy()
	controllerutil.RemoveFinalizer(cluster2, constants.MySQLClusterFinalizer)
	patch := client.MergeFrom(cluster)
	if err := r.Patch(ctx, cluster2, patch); err != nil {
		log.Error(err, "failed to remove finalizer")
		return ctrl.Result{}, err
	}

	r.MySQLAccessor.Remove(cluster.PrefixedName() + "." + cluster.Namespace)
	r.AgentAccessor.Remove(cluster.PrefixedName() + "." + cluster.Namespace)

	log.Info("finalizing MySQLCluster is completed")

	return ctrl.Result{}, nil
}

func (r *OldMySQLClusterReconciler) reconcileInitialize(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	isUpdatedAtLeastOnce := false

	isUpdated, err := r.setServerIDBaseIfNotAssigned(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createControllerSecretIfNotExist(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createOrUpdateSecret(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createOrUpdateConfigMap(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createOrUpdateHeadlessService(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createOrUpdateRBAC(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createOrUpdateStatefulSet(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createOrUpdateServices(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createOrUpdatePodDisruptionBudget(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	return isUpdatedAtLeastOnce, nil
}

func (r *OldMySQLClusterReconciler) reconcileFinalize(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	var err error
	err = r.removePodDisruptionBudget(ctx, log, cluster)
	if err != nil {
		return err
	}

	err = r.removeServices(ctx, log, cluster)
	if err != nil {
		return err
	}

	err = r.removeStatefulSet(ctx, log, cluster)
	if err != nil {
		return err
	}

	err = r.removeRBAC(ctx, log, cluster)
	if err != nil {
		return err
	}

	err = r.removeHeadlessService(ctx, log, cluster)
	if err != nil {
		return err
	}

	err = r.removeConfigMap(ctx, log, cluster)
	if err != nil {
		return err
	}

	err = r.removeSecrets(ctx, log, cluster)
	if err != nil {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller for reconciliation.
func (r *OldMySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager, watcherInterval time.Duration) error {
	// SetupWithManager sets up the controller for reconciliation.

	ctx := context.Background()
	err := mgr.GetFieldIndexer().IndexField(ctx, &mocov1beta1.MySQLCluster{}, constants.InitializedClusterIndexField, selectInitializedCluster)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1beta1.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1beta1.PodDisruptionBudget{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: 8},
		).
		Complete(r)
}

func selectInitializedCluster(obj client.Object) []string {
	cluster := obj.(*mocov1beta1.MySQLCluster)

	for _, cond := range cluster.Status.Conditions {
		if cond.Type == mocov1beta1.ConditionInitialized {
			return []string{string(cond.Status)}
		}
	}
	return nil
}

func (r *OldMySQLClusterReconciler) setServerIDBaseIfNotAssigned(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	if cluster.Spec.ServerIDBase != nil {
		return false, nil
	}

	serverIDBase := mathrand.Uint32()
	cluster.Spec.ServerIDBase = &serverIDBase
	if err := r.Status().Update(ctx, cluster); err != nil {
		log.Error(err, "failed to status update", "status", cluster.Status)
		return false, err
	}

	return true, nil
}

func (r *OldMySQLClusterReconciler) createControllerSecretIfNotExist(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	secretName := cluster.ControllerSecretName()
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: secretName}, secret)
	if err == nil {
		return false, nil
	}
	if !k8serror.IsNotFound(err) {
		log.Error(err, "unable to get ControllerSecret")
		return false, err
	}

	if err = r.createControllerSecret(ctx, cluster, r.SystemNamespace, secretName); err != nil {
		log.Error(err, "unable to create ControllerSecret")
		return false, err
	}

	return true, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateSecret(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	isUpdatedAtLeastOnce := false
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()}, secret)
	if err != nil {
		log.Error(err, "unable to get ControllerSecret")
		return false, err
	}

	isUpdated, err := r.createOrUpdateClusterSecret(ctx, log, cluster, secret)
	if err != nil {
		return false, err
	}
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated

	isUpdated, err = r.createOrUpdateMyCnfSecretForLocalCLI(ctx, log, cluster, secret)
	if err != nil {
		return false, err
	}
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated

	return isUpdatedAtLeastOnce, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateMyCnfSecretForLocalCLI(ctx context.Context, log logr.Logger,
	cluster *mocov1beta1.MySQLCluster, controllerSecret *corev1.Secret) (bool, error) {
	password, err := operators.NewMySQLPasswordFromSecret(controllerSecret)
	if err != nil {
		return false, err
	}
	readOnlyPass := password.ReadOnly()
	writablePass := password.Writable()

	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(cluster.MyCnfSecretName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, secret, func() error {
		setStandardLabels(&secret.ObjectMeta, cluster)
		secret.Data = map[string][]byte{
			constants.ReadOnlyMyCnfKey: formatCredentialAsMyCnf(constants.ReadOnlyUser, string(readOnlyPass)),
			constants.WritableMyCnfKey: formatCredentialAsMyCnf(constants.WritableUser, string(writablePass)),
		}
		return ctrl.SetControllerReference(cluster, secret, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update MyCnfSecret")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile MyCnfSecret successfully", "op", op)
		return true, nil
	}

	return false, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateClusterSecret(ctx context.Context, log logr.Logger,
	cluster *mocov1beta1.MySQLCluster, controllerSecret *corev1.Secret) (bool, error) {
	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(cluster.UserSecretName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, secret, func() error {
		setStandardLabels(&secret.ObjectMeta, cluster)

		secret.Data = map[string][]byte{
			// constants.AdminPasswordKey:       controllerSecret.Data[constants.AdminPasswordKey],
			// constants.AgentPasswordKey:       controllerSecret.Data[constants.AgentPasswordKey],
			// constants.ReplicationPasswordKey: controllerSecret.Data[constants.ReplicationPasswordKey],
			// constants.CloneDonorPasswordKey:  controllerSecret.Data[constants.CloneDonorPasswordKey],
			// constants.ReadOnlyPasswordKey:    controllerSecret.Data[constants.ReadOnlyPasswordKey],
			// constants.WritablePasswordKey:    controllerSecret.Data[constants.WritablePasswordKey],
		}

		return ctrl.SetControllerReference(cluster, secret, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update ClusterSecret")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile ClusterSecret successfully", "op", op)
		return true, nil
	}

	return false, nil
}

func (r *OldMySQLClusterReconciler) createControllerSecret(ctx context.Context, cluster *mocov1beta1.MySQLCluster, namespace, secretName string) error {
	// adminPass, err := generateRandomBytes(passwordBytes)
	// if err != nil {
	// 	return err
	// }
	// replicatorPass, err := generateRandomBytes(passwordBytes)
	// if err != nil {
	// 	return err
	// }
	// donorPass, err := generateRandomBytes(passwordBytes)
	// if err != nil {
	// 	return err
	// }
	// agentPass, err := generateRandomBytes(passwordBytes)
	// if err != nil {
	// 	return err
	// }
	// readOnlyPass, err := generateRandomBytes(passwordBytes)
	// if err != nil {
	// 	return err
	// }
	// writablePass, err := generateRandomBytes(passwordBytes)
	// if err != nil {
	// 	return err
	// }

	// secret := &corev1.Secret{}
	// secret.SetNamespace(namespace)
	// secret.SetName(secretName)

	// secret.Data = map[string][]byte{
	// 	constants.AdminPasswordKey:       adminPass,
	// 	constants.AgentPasswordKey:       agentPass,
	// 	constants.ReplicationPasswordKey: replicatorPass,
	// 	constants.CloneDonorPasswordKey:  donorPass,
	// 	constants.ReadOnlyPasswordKey:    readOnlyPass,
	// 	constants.WritablePasswordKey:    writablePass,
	// }

	// if err := r.Client.Create(ctx, secret); err != nil {
	// 	return err
	// }

	return nil
}

func (r *OldMySQLClusterReconciler) removeSecrets(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	secrets := []*types.NamespacedName{
		{Namespace: r.SystemNamespace, Name: cluster.ControllerSecretName()},
		{Namespace: cluster.Namespace, Name: cluster.UserSecretName()},
		{Namespace: cluster.Namespace, Name: cluster.MyCnfSecretName()},
	}
	for _, s := range secrets {
		secret := &corev1.Secret{}
		secret.SetNamespace(s.Namespace)
		secret.SetName(s.Name)

		err := r.Delete(ctx, secret)
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "unable to delete Secret")
			return err
		}
	}
	return nil
}

func generateRandomBytes(n int) ([]byte, error) {
	bytes := make([]byte, n)
	_, err := rand.Read(bytes)
	if err != nil {
		return nil, err
	}

	ret := make([]byte, hex.EncodedLen(n))
	hex.Encode(ret, bytes)
	return ret, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	isUpdatedAtLeastOnce := false

	isUpdated, err := r.createOrUpdateMySQLConfConfigMap(ctx, log, cluster)
	if err != nil {
		return false, err
	}
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated

	isUpdated, err = r.createOrUpdateErrLogAgentConfConfigMap(ctx, log, cluster)
	if err != nil {
		return false, err
	}
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated

	isUpdated, err = r.createOrUpdateSlowLogAgentConfConfigMap(ctx, log, cluster)
	if err != nil {
		return false, err
	}
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated

	return isUpdatedAtLeastOnce, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateMySQLConfConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	cm := &corev1.ConfigMap{}
	cm.SetNamespace(cluster.Namespace)
	cm.SetName(cluster.PrefixedName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		setStandardLabels(&cm.ObjectMeta, cluster)
		gen := mysqlConfGenerator{
			log: log,
		}
		gen.mergeSection("mysqld", defaultMycnf)

		// Set innodb_buffer_pool_size if resources.requests.memory or resources.limits.memory is specified
		mem := getMysqldContainerRequests(cluster, corev1.ResourceMemory)
		if mem != nil {
			bufferSize := ((mem.Value() * constants.InnoDBBufferPoolRatioPercent) / 100) >> 20
			// 128MiB is the default innodb_buffer_pool_size value
			if bufferSize > 128 {
				gen.mergeSection("mysqld", map[string]string{"innodb_buffer_pool_size": fmt.Sprintf("%dM", bufferSize)})
			}
		}

		if cluster.Spec.MySQLConfigMapName != nil {
			cm := &corev1.ConfigMap{}
			err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: *cluster.Spec.MySQLConfigMapName}, cm)
			if err != nil {
				return err
			}
			gen.mergeSection("mysqld", cm.Data)
		}

		gen.merge(constMycnf)

		myCnf, err := gen.generate()
		if err != nil {
			return err
		}
		cm.Data = make(map[string]string)
		cm.Data[constants.MySQLConfName] = myCnf

		return ctrl.SetControllerReference(cluster, cm, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update ConfigMap")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile ConfigMap successfully", "op", op)
		return true, nil
	}
	return false, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateErrLogAgentConfConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	cm := &corev1.ConfigMap{}
	cm.SetNamespace(cluster.Namespace)
	cm.SetName(cluster.ErrLogAgentConfigMapName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		setStandardLabels(&cm.ObjectMeta, cluster)

		cm.Data = make(map[string]string)
		cm.Data[constants.FluentBitConfigName] = fmt.Sprintf(constants.DefaultFluentBitConfigTemplate, filepath.Join(constants.VarLogPath, constants.MySQLErrorLogName))

		return ctrl.SetControllerReference(cluster, cm, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update err-log-agent ConfigMap")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile err-log-agent ConfigMap successfully", "op", op)
		return true, nil
	}

	return false, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateSlowLogAgentConfConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	cm := &corev1.ConfigMap{}
	cm.SetNamespace(cluster.Namespace)
	cm.SetName(cluster.SlowQueryLogAgentConfigMapName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		setStandardLabels(&cm.ObjectMeta, cluster)

		cm.Data = make(map[string]string)
		cm.Data[constants.FluentBitConfigName] = fmt.Sprintf(constants.DefaultFluentBitConfigTemplate, filepath.Join(constants.VarLogPath, constants.MySQLSlowLogName))

		return ctrl.SetControllerReference(cluster, cm, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update slow-log-agent ConfigMap")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile slow-log-agent ConfigMap successfully", "op", op)
		return true, nil
	}

	return false, nil
}

func (r *OldMySQLClusterReconciler) removeConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	configmaps := []*types.NamespacedName{
		{Namespace: cluster.Namespace, Name: cluster.PrefixedName()},
		{Namespace: cluster.Namespace, Name: cluster.ErrLogAgentConfigMapName()},
		{Namespace: cluster.Namespace, Name: cluster.SlowQueryLogAgentConfigMapName()},
	}
	for _, c := range configmaps {
		cm := &corev1.ConfigMap{}
		cm.SetNamespace(c.Namespace)
		cm.SetName(c.Name)

		err := r.Delete(ctx, cm)
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "unable to delete ConfigMap")
			return err
		}
	}
	return nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateHeadlessService(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	headless := &corev1.Service{}
	headless.SetNamespace(cluster.Namespace)
	headless.SetName(cluster.PrefixedName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, headless, func() error {
		setStandardLabels(&headless.ObjectMeta, cluster)
		headless.Spec.ClusterIP = corev1.ClusterIPNone
		headless.Spec.PublishNotReadyAddresses = true
		headless.Spec.Selector = map[string]string{
			constants.LabelAppInstance: cluster.PrefixedName(),
			constants.LabelAppName:     constants.AppName,
		}
		return ctrl.SetControllerReference(cluster, headless, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update headless Service")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile headless Service successfully", "op", op)
		return true, nil
	}
	return false, nil
}

func (r *OldMySQLClusterReconciler) removeHeadlessService(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	headless := &corev1.Service{}
	headless.SetNamespace(cluster.Namespace)
	headless.SetName(cluster.PrefixedName())

	err := r.Delete(ctx, headless)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete headless Service")
		return err
	}
	return nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateRBAC(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	if cluster.Spec.PodTemplate.Spec.ServiceAccountName != "" {
		return false, nil
	}

	sa := &corev1.ServiceAccount{}
	sa.SetNamespace(cluster.Namespace)
	sa.SetName(cluster.ServiceAccountName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sa, func() error {
		setStandardLabels(&sa.ObjectMeta, cluster)
		return ctrl.SetControllerReference(cluster, sa, r.Scheme)
	})

	if err != nil {
		log.Error(err, "unable to create-or-update ServiceAccount")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile ServiceAccount successfully", "op", op)
		return true, nil
	}
	return false, nil
}

func (r *OldMySQLClusterReconciler) removeRBAC(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	if cluster.Spec.PodTemplate.Spec.ServiceAccountName != "" {
		return nil
	}

	sa := &corev1.ServiceAccount{}
	sa.SetNamespace(cluster.Namespace)
	sa.SetName(cluster.ServiceAccountName())

	err := r.Delete(ctx, sa)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete ServiceAccount")
		return err
	}
	return nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateStatefulSet(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(cluster.Namespace)
	sts.SetName(cluster.PrefixedName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		setStandardLabels(&sts.ObjectMeta, cluster)
		sts.Spec.Replicas = &cluster.Spec.Replicas
		sts.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
		sts.Spec.ServiceName = cluster.PrefixedName()
		sts.Spec.Selector = &metav1.LabelSelector{}
		if sts.Spec.Selector.MatchLabels == nil {
			sts.Spec.Selector.MatchLabels = make(map[string]string)
		}
		sts.Spec.Selector.MatchLabels[constants.LabelAppInstance] = cluster.PrefixedName()

		podTemplate, err := r.makePodTemplate(log, cluster)
		if err != nil {
			return err
		}
		if !equality.Semantic.DeepDerivative(podTemplate, &sts.Spec.Template) {
			sts.Spec.Template = *podTemplate
		}

		volumeTemplates, err := r.makeVolumeClaimTemplates(cluster)
		if err != nil {
			log.Error(err, "invalid volume template found")
			return err
		}
		volumeClaimTemplates := append(
			volumeTemplates,
			r.makeDataVolumeClaimTemplate(cluster),
		)
		if !equality.Semantic.DeepDerivative(volumeClaimTemplates, sts.Spec.VolumeClaimTemplates) {
			sts.Spec.VolumeClaimTemplates = volumeClaimTemplates
		}

		return ctrl.SetControllerReference(cluster, sts, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update StatefulSet")
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile StatefulSet successfully", "op", op)
		return true, nil
	}
	return false, nil
}

func (r *OldMySQLClusterReconciler) removeStatefulSet(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(cluster.Namespace)
	sts.SetName(cluster.PrefixedName())

	err := r.Delete(ctx, sts)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete StatefulSet")
		return err
	}
	return nil
}

func setDefaultProbeParams(probe *corev1.Probe) {
	if probe == nil {
		return
	}
	if probe.TimeoutSeconds == 0 {
		probe.TimeoutSeconds = 1
	}
	if probe.PeriodSeconds == 0 {
		probe.PeriodSeconds = 10
	}
	if probe.SuccessThreshold == 0 {
		probe.SuccessThreshold = 1
	}
	if probe.FailureThreshold == 0 {
		probe.FailureThreshold = 3
	}
}

func (r *OldMySQLClusterReconciler) makePodTemplate(log logr.Logger, cluster *mocov1beta1.MySQLCluster) (*corev1.PodTemplateSpec, error) {
	template := cluster.Spec.PodTemplate

	// Workaround: equality.Semantic.DeepDerivative cannot ignore numeric field. So set the default values explicitly.
	for _, c := range template.Spec.Containers {
		setDefaultProbeParams(c.LivenessProbe)
		setDefaultProbeParams(c.ReadinessProbe)
	}
	for _, c := range template.Spec.InitContainers {
		setDefaultProbeParams(c.LivenessProbe)
		setDefaultProbeParams(c.ReadinessProbe)
	}

	newTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: template.Annotations,
		},
		Spec: template.Spec,
	}

	newTemplate.Labels = make(map[string]string)
	for k, v := range template.Labels {
		newTemplate.Labels[k] = v
	}

	// add labels to describe application
	setStandardLabels(&newTemplate.ObjectMeta, cluster)

	newTemplate.Spec.ServiceAccountName = cluster.ServiceAccountName()

	if newTemplate.Spec.TerminationGracePeriodSeconds == nil {
		var t int64 = defaultTerminationGracePeriodSeconds
		newTemplate.Spec.TerminationGracePeriodSeconds = &t
	}

	// add volumes to Pod
	// If the original template contains volumes with the same names as below, CreateOrUpdate fails.
	newTemplate.Spec.Volumes = append(newTemplate.Spec.Volumes,
		corev1.Volume{
			Name: mocoBinaryVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: mysqlConfVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: runVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: varLogVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: mysqlFilesVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: tmpVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: mysqlConfTemplateVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cluster.PrefixedName(),
					},
				},
			},
		},
		corev1.Volume{
			Name: myCnfSecretVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: cluster.MyCnfSecretName(),
				},
			},
		},
		corev1.Volume{
			Name: errLogAgentConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cluster.ErrLogAgentConfigMapName(),
					},
				},
			},
		},
		corev1.Volume{
			Name: slowQueryLogAgentConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cluster.SlowQueryLogAgentConfigMapName(),
					},
				},
			},
		},
	)

	// find "mysqld" container and update it
	var mysqldContainer *corev1.Container
	newTemplate.Spec.Containers = make([]corev1.Container, len(template.Spec.Containers))
	for i, orig := range template.Spec.Containers {
		if orig.Name != constants.MysqldContainerName {
			newTemplate.Spec.Containers[i] = orig
			continue
		}
		c := orig.DeepCopy()
		c.Args = []string{"--defaults-file=" + filepath.Join(constants.MySQLConfPath, constants.MySQLConfName)}
		c.Ports = []corev1.ContainerPort{
			{
				ContainerPort: constants.MySQLPort, Protocol: corev1.ProtocolTCP,
			},
			{
				ContainerPort: constants.MySQLXPort, Protocol: corev1.ProtocolTCP,
			},
			{
				ContainerPort: constants.MySQLAdminPort, Protocol: corev1.ProtocolTCP,
			},
		}
		c.LivenessProbe = &corev1.Probe{
			Handler: corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{
						filepath.Join(constants.MOCOBinaryPath, "moco-agent"), "ping",
					},
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
		}
		setDefaultProbeParams(c.LivenessProbe)
		c.ReadinessProbe = &corev1.Probe{
			Handler: corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{
						filepath.Join(constants.MOCOBinaryPath, "grpc-health-probe"), fmt.Sprintf("-addr=localhost:%d", constants.AgentPort),
					},
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       5,
		}
		setDefaultProbeParams(c.ReadinessProbe)
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				MountPath: constants.MOCOBinaryPath,
				Name:      mocoBinaryVolumeName,
			},
			corev1.VolumeMount{
				MountPath: constants.MySQLDataPath,
				Name:      mysqlDataVolumeName,
			},
			corev1.VolumeMount{
				MountPath: constants.MySQLConfPath,
				Name:      mysqlConfVolumeName,
			},
			corev1.VolumeMount{
				MountPath: constants.RunPath,
				Name:      runVolumeName,
			},
			corev1.VolumeMount{
				MountPath: constants.VarLogPath,
				Name:      varLogVolumeName,
			},
			corev1.VolumeMount{
				MountPath: constants.MySQLFilesPath,
				Name:      mysqlFilesVolumeName,
			},
			corev1.VolumeMount{
				MountPath: constants.TmpPath,
				Name:      tmpVolumeName,
			},
			corev1.VolumeMount{
				MountPath: constants.MyCnfSecretPath,
				Name:      myCnfSecretVolumeName,
			},
		)
		newTemplate.Spec.Containers[i] = *c
		mysqldContainer = &newTemplate.Spec.Containers[i]
	}

	if mysqldContainer == nil {
		return nil, fmt.Errorf("container named %q not found in podTemplate", constants.MysqldContainerName)
	}

	for _, orig := range template.Spec.Containers {
		if orig.Name == constants.AgentContainerName {
			err := fmt.Errorf("cannot specify %s container in podTemplate", constants.AgentContainerName)
			log.Error(err, "invalid container found")
			return nil, err
		}
	}
	agentContainer := corev1.Container{
		Name:  constants.AgentContainerName,
		Image: mysqldContainer.Image,
		Command: []string{
			filepath.Join(constants.MOCOBinaryPath, "moco-agent"), "server", "--log-rotation-schedule", cluster.Spec.LogRotationSchedule,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: constants.MOCOBinaryPath,
				Name:      mocoBinaryVolumeName,
			},
			{
				MountPath: constants.MySQLDataPath,
				Name:      mysqlDataVolumeName,
			},
			{
				MountPath: constants.MySQLConfPath,
				Name:      mysqlConfVolumeName,
			},
			{
				MountPath: constants.RunPath,
				Name:      runVolumeName,
			},
			{
				MountPath: constants.VarLogPath,
				Name:      varLogVolumeName,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name: constants.PodNameEnvName,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
			{
				Name: constants.PodIPEnvName,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
		},
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cluster.UserSecretName(),
					},
				},
			},
		},
	}

	if cluster.Spec.ReplicationSourceSecretName != nil {
		newTemplate.Spec.Volumes = append(newTemplate.Spec.Volumes, corev1.Volume{
			Name: replicationSourceSecretVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: *cluster.Spec.ReplicationSourceSecretName,
				},
			},
		})
		agentContainer.VolumeMounts = append(agentContainer.VolumeMounts, corev1.VolumeMount{
			MountPath: constants.ReplicationSourceSecretPath,
			Name:      replicationSourceSecretVolumeName,
		})
	}

	newTemplate.Spec.Containers = append(newTemplate.Spec.Containers, agentContainer)

	// find "err-log" container and update it
	if !cluster.Spec.DisableErrorLogContainer {
		errLogContainer, err := r.makeErrLogAgentContainer(cluster)
		if err != nil {
			return nil, err
		}

		found := false

		for i, c := range newTemplate.Spec.Containers {
			if c.Name == constants.ErrLogAgentContainerName {
				newTemplate.Spec.Containers[i] = errLogContainer
				found = true
			}
		}

		if !found {
			newTemplate.Spec.Containers = append(newTemplate.Spec.Containers, errLogContainer)
		}
	}

	// find "slow-log" container and update it
	if !cluster.Spec.DisableSlowQueryLogContainer {
		slowQueryLogContainer, err := r.makeSlowQueryLogAgentContainer(cluster)
		if err != nil {
			return nil, err
		}

		found := false

		for i, c := range newTemplate.Spec.Containers {
			if c.Name == constants.SlowQueryLogAgentContainerName {
				newTemplate.Spec.Containers[i] = slowQueryLogContainer
			}
		}

		if !found {
			newTemplate.Spec.Containers = append(newTemplate.Spec.Containers, slowQueryLogContainer)
		}
	}

	// create init containers and append them to Pod
	newTemplate.Spec.InitContainers = append(newTemplate.Spec.InitContainers,
		r.makeBinaryCopyContainer(),
		r.makeEntrypointInitContainer(log, cluster, mysqldContainer.Image),
	)

	return &newTemplate, nil
}

func (r *OldMySQLClusterReconciler) makeErrLogAgentContainer(cluster *mocov1beta1.MySQLCluster) (corev1.Container, error) {
	base := corev1.Container{
		Name:  constants.ErrLogAgentContainerName,
		Image: r.FluentBitImage,
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: constants.FluentBitConfigPath,
				Name:      errLogAgentConfigVolumeName,
				ReadOnly:  true,
				SubPath:   constants.FluentBitConfigName,
			},
			{
				MountPath: constants.VarLogPath,
				Name:      varLogVolumeName,
			},
		},
	}

	for _, c := range cluster.Spec.PodTemplate.Spec.Containers {
		if c.Name == constants.ErrLogAgentContainerName {
			return mergePatchContainers(base, c)
		}
	}

	return base, nil
}

func (r *OldMySQLClusterReconciler) makeSlowQueryLogAgentContainer(cluster *mocov1beta1.MySQLCluster) (corev1.Container, error) {
	base := corev1.Container{
		Name:  constants.SlowQueryLogAgentContainerName,
		Image: r.FluentBitImage,
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: constants.FluentBitConfigPath,
				Name:      slowQueryLogAgentConfigVolumeName,
				ReadOnly:  true,
				SubPath:   constants.FluentBitConfigName,
			},
			{
				MountPath: constants.VarLogPath,
				Name:      varLogVolumeName,
			},
		},
	}

	for _, c := range cluster.Spec.PodTemplate.Spec.Containers {
		if c.Name == constants.SlowQueryLogAgentContainerName {
			return mergePatchContainers(base, c)
		}
	}

	return base, nil
}

func (r *OldMySQLClusterReconciler) makeBinaryCopyContainer() corev1.Container {
	c := corev1.Container{
		Name:  constants.BinaryCopyContainerName,
		Image: r.BinaryCopyContainerImage,
		Command: []string{
			"cp",
			"/moco-agent",
			"/grpc-health-probe",
			constants.MOCOBinaryPath},
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: constants.MOCOBinaryPath,
				Name:      mocoBinaryVolumeName,
			},
		},
	}

	return c
}

func (r *OldMySQLClusterReconciler) makeEntrypointInitContainer(log logr.Logger, cluster *mocov1beta1.MySQLCluster, mysqldContainerImage string) corev1.Container {
	c := corev1.Container{}
	c.Name = constants.EntrypointInitContainerName

	// use the same image with the 'mysqld' container
	c.Image = mysqldContainerImage

	serverIDOption := fmt.Sprintf("--server-id-base=%d", *cluster.Spec.ServerIDBase)

	c.Command = []string{filepath.Join(constants.MOCOBinaryPath, "moco-agent"), "init", serverIDOption}
	c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: cluster.UserSecretName(),
			},
		},
	})
	c.Env = append(c.Env,
		corev1.EnvVar{
			Name: constants.PodIPEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		corev1.EnvVar{
			Name: constants.PodNameEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
	)
	c.VolumeMounts = append(c.VolumeMounts,
		corev1.VolumeMount{
			MountPath: constants.MOCOBinaryPath,
			Name:      mocoBinaryVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MySQLDataPath,
			Name:      mysqlDataVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MySQLConfPath,
			Name:      mysqlConfVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.RunPath,
			Name:      runVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.VarLogPath,
			Name:      varLogVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MySQLFilesPath,
			Name:      mysqlFilesVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.TmpPath,
			Name:      tmpVolumeName,
		},
		corev1.VolumeMount{
			MountPath: constants.MySQLConfTemplatePath,
			Name:      mysqlConfTemplateVolumeName,
		},
	)

	return c
}

func (r *OldMySQLClusterReconciler) makeVolumeClaimTemplates(cluster *mocov1beta1.MySQLCluster) ([]corev1.PersistentVolumeClaim, error) {
	templates := cluster.Spec.VolumeClaimTemplates
	newTemplates := make([]corev1.PersistentVolumeClaim, len(templates))

	for i, template := range templates {
		if template.Name == mysqlDataVolumeName {
			err := fmt.Errorf("cannot specify %s volume in volumeClaimTemplates", mysqlDataVolumeName)
			return nil, err
		}
		newTemplates[i] = corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:        template.Name,
				Labels:      template.Labels,
				Annotations: template.Annotations,
			},
			Spec: template.Spec,
		}
	}

	return newTemplates, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func (r *OldMySQLClusterReconciler) makeDataVolumeClaimTemplate(cluster *mocov1beta1.MySQLCluster) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: mysqlDataVolumeName,
			// Set ownerReference to delete the data PVC automatically when deleting the MySQLCluster CR.
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         cluster.GroupVersionKind().GroupVersion().String(),
					Kind:               cluster.GroupVersionKind().Kind,
					Name:               cluster.Name,
					UID:                cluster.UID,
					BlockOwnerDeletion: boolPtr(true),
					Controller:         boolPtr(true),
				},
			},
		},
		Spec: cluster.Spec.DataVolumeClaimTemplateSpec,
	}
}

func (r *OldMySQLClusterReconciler) createOrUpdateServices(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	primaryServiceName := fmt.Sprintf("%s-primary", cluster.PrefixedName())
	primaryIsUpdated, op, err := r.createOrUpdateService(ctx, cluster, primaryServiceName)
	if err != nil {
		log.Error(err, "unable to create-or-update Primary Service")
		return false, err
	}
	if primaryIsUpdated {
		log.Info("reconcile Primary Service successfully", "op", op)
	}

	replicaServiceName := fmt.Sprintf("%s-replica", cluster.PrefixedName())
	replicaIsUpdated, op, err := r.createOrUpdateService(ctx, cluster, replicaServiceName)
	if err != nil {
		log.Error(err, "unable to create-or-update Replica Service")
		return false, err
	}
	if replicaIsUpdated {
		log.Info("reconcile Replica Service successfully", "op", op)
	}

	return primaryIsUpdated || replicaIsUpdated, nil
}

func (r *OldMySQLClusterReconciler) createOrUpdateService(ctx context.Context, cluster *mocov1beta1.MySQLCluster, svcName string) (bool, controllerutil.OperationResult, error) {
	isUpdated := false
	svc := &corev1.Service{}
	svc.SetNamespace(cluster.Namespace)
	svc.SetName(svcName)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if cluster.Spec.ServiceTemplate != nil {
			if !equality.Semantic.DeepDerivative(cluster.Spec.ServiceTemplate.Annotations, svc.Annotations) {
				if svc.Annotations == nil {
					svc.Annotations = make(map[string]string)
				}
				for k, v := range cluster.Spec.ServiceTemplate.Annotations {
					svc.Annotations[k] = v
				}
			}

			if !equality.Semantic.DeepDerivative(cluster.Spec.ServiceTemplate.Labels, svc.Labels) {
				svc.Labels = make(map[string]string)
				for k, v := range cluster.Spec.ServiceTemplate.Labels {
					svc.Labels[k] = v
				}
			}

			if cluster.Spec.ServiceTemplate.Spec != nil &&
				((*cluster.Spec.ServiceTemplate).Spec.Type != svc.Spec.Type) {
				svc.Spec.Type = (*cluster.Spec.ServiceTemplate).Spec.Type
			}
		}

		setStandardLabels(&svc.ObjectMeta, cluster)

		var hasMySQLPort, hasMySQLXPort bool
		for i, port := range svc.Spec.Ports {
			if port.Name == "mysql" {
				svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
				svc.Spec.Ports[i].Port = constants.MySQLPort
				hasMySQLPort = true
			}
			if port.Name == "mysqlx" {
				svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
				svc.Spec.Ports[i].Port = constants.MySQLXPort
				hasMySQLXPort = true
			}
		}
		if !hasMySQLPort {
			svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
				Name:     "mysql",
				Protocol: corev1.ProtocolTCP,
				Port:     constants.MySQLPort,
			})
		}
		if !hasMySQLXPort {
			svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
				Name:     "mysqlx",
				Protocol: corev1.ProtocolTCP,
				Port:     constants.MySQLXPort,
			})
		}

		svc.Spec.Selector = make(map[string]string)
		svc.Spec.Selector[constants.LabelAppInstance] = cluster.PrefixedName()
		svc.Spec.Selector[constants.LabelMocoRole] = constants.RolePrimary
		svc.Spec.Selector[constants.LabelAppName] = constants.AppName

		return ctrl.SetControllerReference(cluster, svc, r.Scheme)
	})
	if err != nil {
		return false, op, err
	}

	if op != controllerutil.OperationResultNone {
		isUpdated = true
	}
	return isUpdated, op, nil
}

func (r *OldMySQLClusterReconciler) removeServices(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	services := []string{
		fmt.Sprintf("%s-primary", cluster.PrefixedName()),
		fmt.Sprintf("%s-replica", cluster.PrefixedName()),
	}
	for _, svcName := range services {
		svc := &corev1.Service{}
		svc.SetNamespace(cluster.Namespace)
		svc.SetName(svcName)

		err := r.Delete(ctx, svc)
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "unable to delete Service")
			return err
		}
	}
	return nil
}

func (r *OldMySQLClusterReconciler) createOrUpdatePodDisruptionBudget(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) (bool, error) {
	isUpdated := false

	pdb := &policyv1beta1.PodDisruptionBudget{}
	pdb.SetNamespace(cluster.Namespace)
	pdb.SetName(cluster.PrefixedName())

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, pdb, func() error {
		setStandardLabels(&pdb.ObjectMeta, cluster)
		pdb.Spec.MaxUnavailable = &intstr.IntOrString{}
		pdb.Spec.MaxUnavailable.Type = intstr.Int
		if cluster.Spec.Replicas > 1 {
			pdb.Spec.MaxUnavailable.IntVal = cluster.Spec.Replicas / 2
		} else {
			pdb.Spec.MaxUnavailable.IntVal = 1
		}
		pdb.Spec.Selector = &metav1.LabelSelector{}
		pdb.Spec.Selector.MatchLabels = map[string]string{
			constants.LabelAppInstance: cluster.PrefixedName(),
			constants.LabelAppName:     constants.AppName,
		}

		return ctrl.SetControllerReference(cluster, pdb, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update PodDisruptionBudget")
		return false, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("reconcile PodDisruptionBudget successfully", "op", op)
		isUpdated = true
	}

	return isUpdated, nil
}

func (r *OldMySQLClusterReconciler) removePodDisruptionBudget(ctx context.Context, log logr.Logger, cluster *mocov1beta1.MySQLCluster) error {
	pdb := &policyv1beta1.PodDisruptionBudget{}
	pdb.SetNamespace(cluster.Namespace)
	pdb.SetName(cluster.PrefixedName())

	err := r.Delete(ctx, pdb)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete PodDisruptionBudget")
		return err
	}
	return nil
}

func setStandardLabels(om *metav1.ObjectMeta, cluster *mocov1beta1.MySQLCluster) {
	if om.Labels == nil {
		om.Labels = make(map[string]string)
	}
	om.Labels[constants.LabelAppInstance] = cluster.PrefixedName()
	om.Labels[constants.LabelAppName] = constants.AppName
}

func getMysqldContainerRequests(cluster *mocov1beta1.MySQLCluster, resourceName corev1.ResourceName) *resource.Quantity {
	for _, c := range cluster.Spec.PodTemplate.Spec.Containers {
		if c.Name != constants.MysqldContainerName {
			continue
		}
		r, ok := c.Resources.Requests[resourceName]
		if ok {
			return &r
		}
		r, ok = c.Resources.Limits[resourceName]
		if ok {
			return &r
		}
		return nil
	}
	return nil
}

func setCondition(conditions *[]mocov1beta1.MySQLClusterCondition, newCondition mocov1beta1.MySQLClusterCondition) {
	if conditions == nil {
		conditions = &[]mocov1beta1.MySQLClusterCondition{}
	}
	current := findCondition(*conditions, newCondition.Type)
	if current == nil {
		newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		*conditions = append(*conditions, newCondition)
		return
	}
	if current.Status != newCondition.Status {
		current.Status = newCondition.Status
		current.LastTransitionTime = metav1.NewTime(time.Now())
	}
	current.Reason = newCondition.Reason
	current.Message = newCondition.Message
}

func findCondition(conditions []mocov1beta1.MySQLClusterCondition, conditionType mocov1beta1.MySQLClusterConditionType) *mocov1beta1.MySQLClusterCondition {
	for i, c := range conditions {
		if c.Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func formatCredentialAsMyCnf(user, password string) []byte {
	return []byte(fmt.Sprintf(`[client]
user="%s"
password="%s"
`, user, password))
}

// mergePatchContainers adds patches to base using a strategic merge patch.
func mergePatchContainers(base, patches corev1.Container) (corev1.Container, error) {
	containerBytes, err := json.Marshal(base)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("failed to marshal json for container %s, err: %w", base.Name, err)
	}
	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("failed to marshal json for patch container %s, err: %w", patches.Name, err)
	}

	jsonResult, err := strategicpatch.StrategicMergePatch(containerBytes, patchBytes, corev1.Container{})
	if err != nil {
		return corev1.Container{}, fmt.Errorf("failed to generate merge patch for %s, err: %w", base.Name, err)
	}

	var patchResult corev1.Container
	if err := json.Unmarshal(jsonResult, &patchResult); err != nil {
		return corev1.Container{}, fmt.Errorf("failed to unmarshal merged container %s, err: %w", base.Name, err)
	}

	return patchResult, nil
}
