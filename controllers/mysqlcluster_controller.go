package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand"
	"path/filepath"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/cybozu-go/moco/metrics"
	"github.com/cybozu-go/moco/runners"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
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
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	agentContainerName          = "agent"
	binaryCopyContainerName     = "binary-copy"
	entrypointInitContainerName = "moco-init"

	mocoBinaryVolumeName              = "moco-bin"
	mysqlDataVolumeName               = "mysql-data"
	mysqlConfVolumeName               = "mysql-conf"
	varRunVolumeName                  = "var-run"
	varLogVolumeName                  = "var-log"
	mysqlFilesVolumeName              = "mysql-files"
	tmpVolumeName                     = "tmp"
	mysqlConfTemplateVolumeName       = "mysql-conf-template"
	replicationSourceSecretVolumeName = "replication-source-secret"
	myCnfSecretVolumeName             = "my-cnf-secret"

	passwordBytes = 16

	defaultTerminationGracePeriodSeconds = 300

	mysqlClusterFinalizer = "moco.cybozu.com/mysqlcluster"
)

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Log                      logr.Logger
	Recorder                 record.EventRecorder
	Scheme                   *runtime.Scheme
	BinaryCopyContainerImage string
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
func (r *MySQLClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("MySQLCluster", req.NamespacedName)

	cluster := &mocov1alpha1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if k8serror.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch MySQLCluster")
		return ctrl.Result{}, err
	}

	if cluster.DeletionTimestamp == nil {
		if !controllerutil.ContainsFinalizer(cluster, mysqlClusterFinalizer) {
			cluster2 := cluster.DeepCopy()
			controllerutil.AddFinalizer(cluster2, mysqlClusterFinalizer)
			patch := client.MergeFrom(cluster)
			if err := r.Patch(ctx, cluster2, patch); err != nil {
				log.Error(err, "failed to add finalizer")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		// initialize
		metrics.UpdateOperationPhase(cluster.Name, moco.PhaseInitializing)
		isUpdated, err := r.reconcileInitialize(ctx, log, cluster)
		if err != nil {
			setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
				Type: mocov1alpha1.ConditionInitialized, Status: corev1.ConditionFalse, Reason: "reconcileInitializeFailed", Message: err.Error()})
			if errUpdate := r.Status().Update(ctx, cluster); errUpdate != nil {
				log.Error(err, "failed to status update", "status", cluster.Status)
			}
			log.Error(err, "failed to initialize MySQLCluster")

			r.Recorder.Eventf(cluster, corev1.EventTypeNormal, moco.EventInitializationFailed.Reason, moco.EventInitializationFailed.Message, err)

			return ctrl.Result{}, err
		}
		if isUpdated {
			setCondition(&cluster.Status.Conditions, mocov1alpha1.MySQLClusterCondition{
				Type: mocov1alpha1.ConditionInitialized, Status: corev1.ConditionTrue})
			if err := r.Status().Update(ctx, cluster); err != nil {
				log.Error(err, "failed to status update", "status", cluster.Status)

				r.Recorder.Eventf(cluster, corev1.EventTypeNormal, moco.EventInitializationFailed.Reason, moco.EventInitializationFailed.Message, err)

				return ctrl.Result{}, err
			}

			r.Recorder.Event(cluster, moco.EventInitializationSucceeded.Type, moco.EventInitializationSucceeded.Reason, moco.EventInitializationSucceeded.Message)
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
	if !controllerutil.ContainsFinalizer(cluster, mysqlClusterFinalizer) {
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
	controllerutil.RemoveFinalizer(cluster2, mysqlClusterFinalizer)
	patch := client.MergeFrom(cluster)
	if err := r.Patch(ctx, cluster2, patch); err != nil {
		log.Error(err, "failed to remove finalizer")
		return ctrl.Result{}, err
	}

	r.MySQLAccessor.Remove(moco.UniqueName(cluster) + "." + cluster.Namespace)
	r.AgentAccessor.Remove(moco.UniqueName(cluster) + "." + cluster.Namespace)

	log.Info("finalizing MySQLCluster is completed")

	return ctrl.Result{}, nil
}

func (r *MySQLClusterReconciler) reconcileInitialize(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
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

	isUpdated, err = r.generateAgentToken(ctx, log, cluster)
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

func (r *MySQLClusterReconciler) reconcileFinalize(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
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
func (r *MySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager, watcherInterval time.Duration) error {
	// SetupWithManager sets up the controller for reconciliation.

	ctx := context.Background()
	err := mgr.GetFieldIndexer().IndexField(ctx, &mocov1alpha1.MySQLCluster{}, moco.InitializedClusterIndexField, selectInitializedCluster)
	if err != nil {
		return err
	}

	ch := make(chan event.GenericEvent)
	watcher := runners.NewMySQLClusterWatcher(mgr.GetClient(), ch, watcherInterval)
	err = mgr.Add(watcher)
	if err != nil {
		return err
	}
	src := source.Channel{
		Source: ch,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1alpha1.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1beta1.PodDisruptionBudget{}).
		Watches(&src, &handler.EnqueueRequestForObject{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: 8},
		).
		Complete(r)
}

func selectInitializedCluster(obj client.Object) []string {
	cluster := obj.(*mocov1alpha1.MySQLCluster)

	for _, cond := range cluster.Status.Conditions {
		if cond.Type == mocov1alpha1.ConditionInitialized {
			return []string{string(cond.Status)}
		}
	}
	return nil
}

func (r *MySQLClusterReconciler) setServerIDBaseIfNotAssigned(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	if cluster.Status.ServerIDBase != nil {
		return false, nil
	}

	serverIDBase := mathrand.Uint32()
	cluster.Status.ServerIDBase = &serverIDBase
	if err := r.Status().Update(ctx, cluster); err != nil {
		log.Error(err, "failed to status update", "status", cluster.Status)
		return false, err
	}

	return true, nil
}

func (r *MySQLClusterReconciler) createControllerSecretIfNotExist(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	secretName := moco.GetControllerSecretName(cluster)
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

func (r *MySQLClusterReconciler) createOrUpdateSecret(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	isUpdatedAtLeastOnce := false
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: moco.GetControllerSecretName(cluster)}, secret)
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

func (r *MySQLClusterReconciler) createOrUpdateMyCnfSecretForLocalCLI(ctx context.Context, log logr.Logger,
	cluster *mocov1alpha1.MySQLCluster, controllerSecret *corev1.Secret) (bool, error) {
	readOnlyPass := controllerSecret.Data[moco.ReadOnlyPasswordKey]
	writablePass := controllerSecret.Data[moco.WritablePasswordKey]

	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(moco.GetMyCnfSecretName(cluster.Name))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, secret, func() error {
		setStandardLabels(&secret.ObjectMeta, cluster)
		secret.Data = map[string][]byte{
			moco.ReadOnlyMyCnfKey: formatCredentialAsMyCnf(moco.ReadOnlyUser, string(readOnlyPass)),
			moco.WritableMyCnfKey: formatCredentialAsMyCnf(moco.WritableUser, string(writablePass)),
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

func (r *MySQLClusterReconciler) createOrUpdateClusterSecret(ctx context.Context, log logr.Logger,
	cluster *mocov1alpha1.MySQLCluster, controllerSecret *corev1.Secret) (bool, error) {
	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(moco.GetClusterSecretName(cluster.Name))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, secret, func() error {
		setStandardLabels(&secret.ObjectMeta, cluster)

		secret.Data = map[string][]byte{
			moco.AdminPasswordKey:       controllerSecret.Data[moco.AdminPasswordKey],
			moco.AgentPasswordKey:       controllerSecret.Data[moco.AgentPasswordKey],
			moco.ReplicationPasswordKey: controllerSecret.Data[moco.ReplicationPasswordKey],
			moco.CloneDonorPasswordKey:  controllerSecret.Data[moco.CloneDonorPasswordKey],
			moco.ReadOnlyPasswordKey:    controllerSecret.Data[moco.ReadOnlyPasswordKey],
			moco.WritablePasswordKey:    controllerSecret.Data[moco.WritablePasswordKey],
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

func (r *MySQLClusterReconciler) createControllerSecret(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, namespace, secretName string) error {
	adminPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return err
	}
	replicatorPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return err
	}
	donorPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return err
	}
	agentPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return err
	}
	readOnlyPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return err
	}
	writablePass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{}
	secret.SetNamespace(namespace)
	secret.SetName(secretName)

	secret.Data = map[string][]byte{
		moco.AdminPasswordKey:       adminPass,
		moco.AgentPasswordKey:       agentPass,
		moco.ReplicationPasswordKey: replicatorPass,
		moco.CloneDonorPasswordKey:  donorPass,
		moco.ReadOnlyPasswordKey:    readOnlyPass,
		moco.WritablePasswordKey:    writablePass,
	}

	if err := r.Client.Create(ctx, secret); err != nil {
		return err
	}

	return nil
}

func (r *MySQLClusterReconciler) removeSecrets(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	secrets := []*types.NamespacedName{
		{Namespace: r.SystemNamespace, Name: moco.GetControllerSecretName(cluster)},
		{Namespace: cluster.Namespace, Name: moco.GetClusterSecretName(cluster.Name)},
		{Namespace: cluster.Namespace, Name: moco.GetMyCnfSecretName(cluster.Name)},
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

func (r *MySQLClusterReconciler) createOrUpdateConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	cm := &corev1.ConfigMap{}
	cm.SetNamespace(cluster.Namespace)
	cm.SetName(moco.UniqueName(cluster))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		setStandardLabels(&cm.ObjectMeta, cluster)
		gen := mysqlConfGenerator{
			log: log,
		}
		gen.mergeSection("mysqld", defaultMycnf)

		// Set innodb_buffer_pool_size if resources.requests.memory or resources.limits.memory is specified
		mem := getMysqldContainerRequests(cluster, corev1.ResourceMemory)
		if mem != nil {
			bufferSize := ((mem.Value() * moco.InnoDBBufferPoolRatioPercent) / 100) >> 20
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
		cm.Data[moco.MySQLConfName] = myCnf

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

func (r *MySQLClusterReconciler) removeConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	cm := &corev1.ConfigMap{}
	cm.SetNamespace(cluster.Namespace)
	cm.SetName(moco.UniqueName(cluster))

	err := r.Delete(ctx, cm)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete ConfigMap")
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) createOrUpdateHeadlessService(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	headless := &corev1.Service{}
	headless.SetNamespace(cluster.Namespace)
	headless.SetName(moco.UniqueName(cluster))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, headless, func() error {
		setStandardLabels(&headless.ObjectMeta, cluster)
		headless.Spec.ClusterIP = corev1.ClusterIPNone
		headless.Spec.PublishNotReadyAddresses = true
		headless.Spec.Selector = map[string]string{
			moco.ClusterKey:   moco.UniqueName(cluster),
			moco.ManagedByKey: moco.MyName,
			moco.AppNameKey:   moco.AppName,
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

func (r *MySQLClusterReconciler) removeHeadlessService(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	headless := &corev1.Service{}
	headless.SetNamespace(cluster.Namespace)
	headless.SetName(moco.UniqueName(cluster))

	err := r.Delete(ctx, headless)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete headless Service")
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) createOrUpdateRBAC(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	if cluster.Spec.PodTemplate.Spec.ServiceAccountName != "" {
		return false, nil
	}

	sa := &corev1.ServiceAccount{}
	sa.SetNamespace(cluster.Namespace)
	sa.SetName(moco.GetServiceAccountName(cluster.Name))

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

func (r *MySQLClusterReconciler) removeRBAC(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	if cluster.Spec.PodTemplate.Spec.ServiceAccountName != "" {
		return nil
	}

	sa := &corev1.ServiceAccount{}
	sa.SetNamespace(cluster.Namespace)
	sa.SetName(moco.GetServiceAccountName(cluster.Name))

	err := r.Delete(ctx, sa)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete ServiceAccount")
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) createOrUpdateStatefulSet(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(cluster.Namespace)
	sts.SetName(moco.UniqueName(cluster))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		setStandardLabels(&sts.ObjectMeta, cluster)
		sts.Spec.Replicas = &cluster.Spec.Replicas
		sts.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
		sts.Spec.ServiceName = moco.UniqueName(cluster)
		sts.Spec.Selector = &metav1.LabelSelector{}
		if sts.Spec.Selector.MatchLabels == nil {
			sts.Spec.Selector.MatchLabels = make(map[string]string)
		}
		sts.Spec.Selector.MatchLabels[moco.ClusterKey] = moco.UniqueName(cluster)
		sts.Spec.Selector.MatchLabels[moco.ManagedByKey] = moco.MyName

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

func (r *MySQLClusterReconciler) removeStatefulSet(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(cluster.Namespace)
	sts.SetName(moco.UniqueName(cluster))

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

func (r *MySQLClusterReconciler) makePodTemplate(log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (*corev1.PodTemplateSpec, error) {
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

	newTemplate.Spec.ServiceAccountName = moco.GetServiceAccountName(cluster.Name)

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
			Name: varRunVolumeName,
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
						Name: moco.UniqueName(cluster),
					},
				},
			},
		},
		corev1.Volume{
			Name: myCnfSecretVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: moco.GetMyCnfSecretName(cluster.Name),
				},
			},
		},
	)

	// find "mysqld" container and update it
	var mysqldContainer *corev1.Container
	newTemplate.Spec.Containers = make([]corev1.Container, len(template.Spec.Containers))
	for i, orig := range template.Spec.Containers {
		if orig.Name != moco.MysqldContainerName {
			newTemplate.Spec.Containers[i] = orig
			continue
		}
		c := orig.DeepCopy()
		c.Args = []string{"--defaults-file=" + filepath.Join(moco.MySQLConfPath, moco.MySQLConfName)}
		c.Ports = []corev1.ContainerPort{
			{
				ContainerPort: moco.MySQLPort, Protocol: corev1.ProtocolTCP,
			},
			{
				ContainerPort: moco.MySQLXPort, Protocol: corev1.ProtocolTCP,
			},
			{
				ContainerPort: moco.MySQLAdminPort, Protocol: corev1.ProtocolTCP,
			},
		}
		c.LivenessProbe = &corev1.Probe{
			Handler: corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{
						filepath.Join(moco.MOCOBinaryPath, "moco-agent"), "ping",
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
						filepath.Join(moco.MOCOBinaryPath, "grpc-health-probe"), fmt.Sprintf("-addr=localhost:%d", moco.AgentPort),
					},
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       5,
		}
		setDefaultProbeParams(c.ReadinessProbe)
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				MountPath: moco.MOCOBinaryPath,
				Name:      mocoBinaryVolumeName,
			},
			corev1.VolumeMount{
				MountPath: moco.MySQLDataPath,
				Name:      mysqlDataVolumeName,
			},
			corev1.VolumeMount{
				MountPath: moco.MySQLConfPath,
				Name:      mysqlConfVolumeName,
			},
			corev1.VolumeMount{
				MountPath: moco.VarRunPath,
				Name:      varRunVolumeName,
			},
			corev1.VolumeMount{
				MountPath: moco.VarLogPath,
				Name:      varLogVolumeName,
			},
			corev1.VolumeMount{
				MountPath: moco.MySQLFilesPath,
				Name:      mysqlFilesVolumeName,
			},
			corev1.VolumeMount{
				MountPath: moco.TmpPath,
				Name:      tmpVolumeName,
			},
			corev1.VolumeMount{
				MountPath: moco.MyCnfSecretPath,
				Name:      myCnfSecretVolumeName,
			},
		)
		newTemplate.Spec.Containers[i] = *c
		mysqldContainer = &newTemplate.Spec.Containers[i]
	}

	if mysqldContainer == nil {
		return nil, fmt.Errorf("container named %q not found in podTemplate", moco.MysqldContainerName)
	}

	for _, orig := range template.Spec.Containers {
		if orig.Name == agentContainerName {
			err := fmt.Errorf("cannot specify %s container in podTemplate", agentContainerName)
			log.Error(err, "invalid container found")
			return nil, err
		}
	}
	agentContainer := corev1.Container{
		Name:  agentContainerName,
		Image: mysqldContainer.Image,
		Command: []string{
			filepath.Join(moco.MOCOBinaryPath, "moco-agent"), "server", "--log-rotation-schedule", cluster.Spec.LogRotationSchedule,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: moco.MOCOBinaryPath,
				Name:      mocoBinaryVolumeName,
			},
			{
				MountPath: moco.MySQLDataPath,
				Name:      mysqlDataVolumeName,
			},
			{
				MountPath: moco.MySQLConfPath,
				Name:      mysqlConfVolumeName,
			},
			{
				MountPath: moco.VarRunPath,
				Name:      varRunVolumeName,
			},
			{
				MountPath: moco.VarLogPath,
				Name:      varLogVolumeName,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name: moco.PodNameEnvName,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
			{
				Name: moco.PodIPEnvName,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
			{
				Name:  moco.AgentTokenEnvName,
				Value: cluster.Status.AgentToken,
			},
		},
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: moco.GetClusterSecretName(cluster.Name),
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
			MountPath: moco.ReplicationSourceSecretPath,
			Name:      replicationSourceSecretVolumeName,
		})
	}

	newTemplate.Spec.Containers = append(newTemplate.Spec.Containers, agentContainer)

	// create init containers and append them to Pod
	newTemplate.Spec.InitContainers = append(newTemplate.Spec.InitContainers,
		r.makeBinaryCopyContainer(),
		r.makeEntrypointInitContainer(log, cluster, mysqldContainer.Image),
	)

	return &newTemplate, nil
}

func (r *MySQLClusterReconciler) makeBinaryCopyContainer() corev1.Container {
	c := corev1.Container{
		Name:  binaryCopyContainerName,
		Image: r.BinaryCopyContainerImage,
		Command: []string{
			"cp",
			"/moco-agent",
			"/grpc-health-probe",
			moco.MOCOBinaryPath},
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: moco.MOCOBinaryPath,
				Name:      mocoBinaryVolumeName,
			},
		},
	}

	return c
}

func (r *MySQLClusterReconciler) makeEntrypointInitContainer(log logr.Logger, cluster *mocov1alpha1.MySQLCluster, mysqldContainerImage string) corev1.Container {
	c := corev1.Container{}
	c.Name = entrypointInitContainerName

	// use the same image with the 'mysqld' container
	c.Image = mysqldContainerImage

	serverIDOption := fmt.Sprintf("--server-id-base=%d", *cluster.Status.ServerIDBase)

	c.Command = []string{filepath.Join(moco.MOCOBinaryPath, "moco-agent"), "init", serverIDOption}
	c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: moco.GetClusterSecretName(cluster.Name),
			},
		},
	})
	c.Env = append(c.Env,
		corev1.EnvVar{
			Name: moco.PodIPEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		corev1.EnvVar{
			Name: moco.PodNameEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
	)
	c.VolumeMounts = append(c.VolumeMounts,
		corev1.VolumeMount{
			MountPath: moco.MOCOBinaryPath,
			Name:      mocoBinaryVolumeName,
		},
		corev1.VolumeMount{
			MountPath: moco.MySQLDataPath,
			Name:      mysqlDataVolumeName,
		},
		corev1.VolumeMount{
			MountPath: moco.MySQLConfPath,
			Name:      mysqlConfVolumeName,
		},
		corev1.VolumeMount{
			MountPath: moco.VarRunPath,
			Name:      varRunVolumeName,
		},
		corev1.VolumeMount{
			MountPath: moco.VarLogPath,
			Name:      varLogVolumeName,
		},
		corev1.VolumeMount{
			MountPath: moco.MySQLFilesPath,
			Name:      mysqlFilesVolumeName,
		},
		corev1.VolumeMount{
			MountPath: moco.TmpPath,
			Name:      tmpVolumeName,
		},
		corev1.VolumeMount{
			MountPath: moco.MySQLConfTemplatePath,
			Name:      mysqlConfTemplateVolumeName,
		},
	)

	return c
}

func (r *MySQLClusterReconciler) makeVolumeClaimTemplates(cluster *mocov1alpha1.MySQLCluster) ([]corev1.PersistentVolumeClaim, error) {
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

func (r *MySQLClusterReconciler) makeDataVolumeClaimTemplate(cluster *mocov1alpha1.MySQLCluster) corev1.PersistentVolumeClaim {
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

func (r *MySQLClusterReconciler) createOrUpdateServices(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	primaryServiceName := fmt.Sprintf("%s-primary", moco.UniqueName(cluster))
	primaryIsUpdated, op, err := r.createOrUpdateService(ctx, cluster, primaryServiceName)
	if err != nil {
		log.Error(err, "unable to create-or-update Primary Service")
		return false, err
	}
	if primaryIsUpdated {
		log.Info("reconcile Primary Service successfully", "op", op)
	}

	replicaServiceName := fmt.Sprintf("%s-replica", moco.UniqueName(cluster))
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

func (r *MySQLClusterReconciler) createOrUpdateService(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, svcName string) (bool, controllerutil.OperationResult, error) {
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
				svc.Spec.Ports[i].Port = moco.MySQLPort
				hasMySQLPort = true
			}
			if port.Name == "mysqlx" {
				svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
				svc.Spec.Ports[i].Port = moco.MySQLXPort
				hasMySQLXPort = true
			}
		}
		if !hasMySQLPort {
			svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
				Name:     "mysql",
				Protocol: corev1.ProtocolTCP,
				Port:     moco.MySQLPort,
			})
		}
		if !hasMySQLXPort {
			svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
				Name:     "mysqlx",
				Protocol: corev1.ProtocolTCP,
				Port:     moco.MySQLXPort,
			})
		}

		svc.Spec.Selector = make(map[string]string)
		svc.Spec.Selector[moco.ClusterKey] = moco.UniqueName(cluster)
		svc.Spec.Selector[moco.RoleKey] = moco.PrimaryRole
		svc.Spec.Selector[moco.AppNameKey] = moco.AppName

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

func (r *MySQLClusterReconciler) removeServices(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	services := []string{
		fmt.Sprintf("%s-primary", moco.UniqueName(cluster)),
		fmt.Sprintf("%s-replica", moco.UniqueName(cluster)),
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

func (r *MySQLClusterReconciler) createOrUpdatePodDisruptionBudget(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	isUpdated := false

	pdb := &policyv1beta1.PodDisruptionBudget{}
	pdb.SetNamespace(cluster.Namespace)
	pdb.SetName(moco.UniqueName(cluster))

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
			moco.ClusterKey:   moco.UniqueName(cluster),
			moco.ManagedByKey: moco.MyName,
			moco.AppNameKey:   moco.AppName,
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

func (r *MySQLClusterReconciler) removePodDisruptionBudget(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	pdb := &policyv1beta1.PodDisruptionBudget{}
	pdb.SetNamespace(cluster.Namespace)
	pdb.SetName(moco.UniqueName(cluster))

	err := r.Delete(ctx, pdb)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "unable to delete PodDisruptionBudget")
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) generateAgentToken(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	if len(cluster.Status.AgentToken) != 0 {
		return false, nil
	}

	cluster.Status.AgentToken = uuid.New().String()
	err := r.Client.Status().Update(ctx, cluster)
	if err != nil {
		return false, err
	}

	return true, nil
}

func setStandardLabels(om *metav1.ObjectMeta, cluster *mocov1alpha1.MySQLCluster) {
	if om.Labels == nil {
		om.Labels = make(map[string]string)
	}
	om.Labels[moco.ClusterKey] = moco.UniqueName(cluster)
	om.Labels[moco.ManagedByKey] = moco.MyName
	om.Labels[moco.AppNameKey] = moco.AppName
}

func getMysqldContainerRequests(cluster *mocov1alpha1.MySQLCluster, resourceName corev1.ResourceName) *resource.Quantity {
	for _, c := range cluster.Spec.PodTemplate.Spec.Containers {
		if c.Name != moco.MysqldContainerName {
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

func setCondition(conditions *[]mocov1alpha1.MySQLClusterCondition, newCondition mocov1alpha1.MySQLClusterCondition) {
	if conditions == nil {
		conditions = &[]mocov1alpha1.MySQLClusterCondition{}
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

func findCondition(conditions []mocov1alpha1.MySQLClusterCondition, conditionType mocov1alpha1.MySQLClusterConditionType) *mocov1alpha1.MySQLClusterCondition {
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
