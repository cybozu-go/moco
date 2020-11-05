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
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	entrypointInitContainerName = "moco-init"
	confInitContainerName       = "moco-conf-gen"

	mysqlDataVolumeName               = "mysql-data"
	mysqlConfVolumeName               = "mysql-conf"
	varRunVolumeName                  = "var-run"
	varLogVolumeName                  = "var-log"
	tmpVolumeName                     = "tmp"
	mysqlConfTemplateVolumeName       = "mysql-conf-template"
	replicationSourceSecretVolumeName = "replication-source-secret"

	passwordBytes = 16

	defaultTerminationGracePeriodSeconds = 300

	mysqlClusterFinalizer = "moco.cybozu.com/mysqlcluster"

	rootPasswordSecretPrefix = "root-password-"
	serviceAccountPrefix     = "mysqld-sa-"
)

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Log                    logr.Logger
	Recorder               record.EventRecorder
	Scheme                 *runtime.Scheme
	ConfInitContainerImage string
	CurlContainerImage     string
	MySQLAccessor          accessor.DataBaseAccessor
	WaitTime               time.Duration
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
// +kubebuilder:rbac:groups="batch",resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="batch",resources=cronjobs/status,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=get;list;watch;create;update

// Reconcile reconciles MySQLCluster.
func (r *MySQLClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("MySQLCluster", req.NamespacedName)

	cluster := &mocov1alpha1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		log.Error(err, "unable to fetch MySQLCluster", "name", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.DeletionTimestamp == nil {
		if !controllerutil.ContainsFinalizer(cluster, mysqlClusterFinalizer) {
			cluster2 := cluster.DeepCopy()
			controllerutil.AddFinalizer(cluster2, mysqlClusterFinalizer)
			patch := client.MergeFrom(cluster)
			if err := r.Patch(ctx, cluster2, patch); err != nil {
				log.Error(err, "failed to add finalizer", "name", cluster.Name)
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
				log.Error(err, "failed to status update")
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

			return ctrl.Result{}, nil
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

	log.Info("start finalizing MySQLCluster", "name", cluster.Name)
	err := r.removePasswordSecretForController(ctx, log, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	metrics.DeleteAllControllerMetrics(cluster.Name)

	cluster2 := cluster.DeepCopy()
	controllerutil.RemoveFinalizer(cluster2, mysqlClusterFinalizer)
	patch := client.MergeFrom(cluster)
	if err := r.Patch(ctx, cluster2, patch); err != nil {
		log.Error(err, "failed to remove finalizer", "name", cluster.Name)
		return ctrl.Result{}, err
	}

	r.MySQLAccessor.Remove(moco.UniqueName(cluster) + "." + cluster.Namespace)

	return ctrl.Result{}, nil
}

func (r *MySQLClusterReconciler) reconcileInitialize(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	isUpdatedAtLeastOnce := false

	isUpdated, err := r.setServerIDBaseIfNotAssigned(ctx, log, cluster)
	isUpdatedAtLeastOnce = isUpdatedAtLeastOnce || isUpdated
	if err != nil {
		return false, err
	}

	isUpdated, err = r.createSecretIfNotExist(ctx, log, cluster)
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

	isUpdated, err = r.createOrUpdateCronJob(ctx, log, cluster)
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
		Owns(&batchv1beta1.CronJob{}).
		Owns(&policyv1beta1.PodDisruptionBudget{}).
		Watches(&src, &handler.EnqueueRequestForObject{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: 8},
		).
		Complete(r)
}

func selectInitializedCluster(obj runtime.Object) []string {
	cluster := obj.(*mocov1alpha1.MySQLCluster)

	for _, cond := range cluster.Status.Conditions {
		if cond.Type == mocov1alpha1.ConditionInitialized {
			return []string{string(cond.Status)}
		}
	}
	return []string{string(corev1.ConditionUnknown)}
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

func (r *MySQLClusterReconciler) createSecretIfNotExist(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	secret := &corev1.Secret{}
	myNS, mySecretName := moco.GetSecretNameForController(cluster)
	err := r.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err == nil {
		return false, nil
	}
	if !errors.IsNotFound(err) {
		log.Error(err, "unable to get Secret")
		return false, err
	}

	operatorPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return false, err
	}
	replicatorPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return false, err
	}
	donorPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return false, err
	}

	err = r.createPasswordSecretForInit(ctx, cluster, operatorPass, replicatorPass, donorPass)
	if err != nil {
		log.Error(err, "unable to create Secret for user")
		return false, err
	}

	// Secret for controller must be created lastly, because its existence is checked at the beginning of the process
	err = r.createPasswordSecretForController(ctx, myNS, mySecretName, operatorPass, replicatorPass, donorPass)
	if err != nil {
		log.Error(err, "unable to create Secret for Controller")
		return false, err
	}

	return true, nil
}

func (r *MySQLClusterReconciler) createPasswordSecretForInit(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, operatorPass, replicatorPass, donorPass []byte) error {
	var rootPass []byte
	if cluster.Spec.RootPasswordSecretName != nil {
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: *cluster.Spec.RootPasswordSecretName}, secret)
		if err != nil {
			return err
		}
		rootPass = secret.Data[moco.RootPasswordKey]
	}
	if len(rootPass) == 0 {
		var err error
		rootPass, err = generateRandomBytes(passwordBytes)
		if err != nil {
			return err
		}
	}
	miscPass, err := generateRandomBytes(passwordBytes)
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
	secretName := rootPasswordSecretPrefix + moco.UniqueName(cluster)
	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(secretName)

	setStandardLabels(&secret.ObjectMeta, cluster)

	secret.Data = map[string][]byte{
		moco.RootPasswordKey:        rootPass,
		moco.OperatorPasswordKey:    operatorPass,
		moco.ReplicationPasswordKey: replicatorPass,
		moco.CloneDonorPasswordKey:  donorPass,
		moco.MiscPasswordKey:        miscPass,
		moco.ReadOnlyPasswordKey:    readOnlyPass,
		moco.WritablePasswordKey:    writablePass,
	}

	err = ctrl.SetControllerReference(cluster, secret, r.Scheme)
	if err != nil {
		return err
	}

	return r.Client.Create(ctx, secret)
}

func (r *MySQLClusterReconciler) createPasswordSecretForController(ctx context.Context, namespace, secretName string, operatorPass, replicatorPass, donorPass []byte) error {
	secret := &corev1.Secret{}
	secret.SetNamespace(namespace)
	secret.SetName(secretName)

	secret.Data = map[string][]byte{
		moco.OperatorPasswordKey:    operatorPass,
		moco.ReplicationPasswordKey: replicatorPass,
		moco.CloneDonorPasswordKey:  donorPass,
	}

	return r.Client.Create(ctx, secret)
}

func (r *MySQLClusterReconciler) removePasswordSecretForController(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	secret := &corev1.Secret{}
	myNS, mySecretName := moco.GetSecretNameForController(cluster)
	err := r.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "unable to get Secret")
		return err
	}
	err = r.Delete(ctx, secret)
	if err != nil {
		log.Error(err, "unable to delete Secret")
		return err
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
		gen.mergeSection("mysqld", defaultMycnf, false)

		// Set innodb_buffer_pool_size if resources.requests.memory or resources.limits.memory is specified
		mem := getMysqldContainerRequests(cluster, corev1.ResourceMemory)
		if mem != nil {
			bufferSize := ((mem.Value() * moco.InnoDBBufferPoolRatioPercent) / 100) >> 20
			// 128MiB is the default innodb_buffer_pool_size value
			if bufferSize > 128 {
				gen.mergeSection("mysqld", map[string]string{"innodb_buffer_pool_size": fmt.Sprintf("%dM", bufferSize)}, false)
			}
		}

		if cluster.Spec.MySQLConfigMapName != nil {
			cm := &corev1.ConfigMap{}
			err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: *cluster.Spec.MySQLConfigMapName}, cm)
			if err != nil {
				return err
			}
			gen.mergeSection("mysqld", cm.Data, false)
		}

		gen.merge(constMycnf, true)

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

func (r *MySQLClusterReconciler) createOrUpdateRBAC(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	if cluster.Spec.PodTemplate.Spec.ServiceAccountName != "" {
		return false, nil
	}

	saName := serviceAccountPrefix + moco.UniqueName(cluster)
	sa := &corev1.ServiceAccount{}
	sa.SetNamespace(cluster.Namespace)
	sa.SetName(saName)

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

func defaultProbe(probe *corev1.Probe) {
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

	// Workaround: equality.Semantic.DeepDerivative cannot ignore numeric field.
	for _, c := range template.Spec.Containers {
		defaultProbe(c.LivenessProbe)
		defaultProbe(c.ReadinessProbe)
	}
	for _, c := range template.Spec.InitContainers {
		defaultProbe(c.LivenessProbe)
		defaultProbe(c.ReadinessProbe)
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

	newTemplate.Spec.ServiceAccountName = serviceAccountPrefix + moco.UniqueName(cluster)

	if newTemplate.Spec.TerminationGracePeriodSeconds == nil {
		var t int64 = defaultTerminationGracePeriodSeconds
		newTemplate.Spec.TerminationGracePeriodSeconds = &t
	}

	// add volumes to Pod
	// If the original template contains volumes with the same names as below, CreateOrUpdate fails.
	newTemplate.Spec.Volumes = append(newTemplate.Spec.Volumes,
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
		c.VolumeMounts = append(c.VolumeMounts,
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
				MountPath: moco.TmpPath,
				Name:      tmpVolumeName,
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
	rootPasswordSecretName := rootPasswordSecretPrefix + moco.UniqueName(cluster)
	agentContainer := corev1.Container{
		Name:  agentContainerName,
		Image: mysqldContainer.Image,
		Command: []string{
			"/entrypoint", "agent",
		},
		VolumeMounts: []corev1.VolumeMount{
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
						Name: rootPasswordSecretName,
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
		r.makeConfInitContainer(log, cluster),
		r.makeEntrypointInitContainer(log, cluster, mysqldContainer.Image),
	)

	return &newTemplate, nil
}

func (r *MySQLClusterReconciler) makeConfInitContainer(log logr.Logger, cluster *mocov1alpha1.MySQLCluster) corev1.Container {
	c := corev1.Container{}
	c.Name = confInitContainerName

	c.Image = r.ConfInitContainerImage

	serverIDOption := fmt.Sprintf("--server-id-base=%d", *cluster.Status.ServerIDBase)
	c.Command = []string{"/moco-conf-gen", serverIDOption}
	c.Env = append(c.Env,
		corev1.EnvVar{
			Name: moco.PodNameEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		corev1.EnvVar{
			Name: moco.PodNamespaceEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		corev1.EnvVar{
			Name: moco.PodIPEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		corev1.EnvVar{
			Name: moco.NodeNameEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "spec.nodeName",
				},
			},
		},
	)
	c.VolumeMounts = append(c.VolumeMounts,
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

func (r *MySQLClusterReconciler) makeEntrypointInitContainer(log logr.Logger, cluster *mocov1alpha1.MySQLCluster, mysqldContainerImage string) corev1.Container {
	c := corev1.Container{}
	c.Name = entrypointInitContainerName

	// use the same image with the 'mysqld' container
	c.Image = mysqldContainerImage

	c.Command = []string{"/entrypoint", "init"}
	secretName := rootPasswordSecretPrefix + moco.UniqueName(cluster)
	c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: secretName,
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
	)
	c.VolumeMounts = append(c.VolumeMounts,
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
			MountPath: moco.TmpPath,
			Name:      tmpVolumeName,
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

func (r *MySQLClusterReconciler) makeDataVolumeClaimTemplate(cluster *mocov1alpha1.MySQLCluster) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: mysqlDataVolumeName,
		},
		Spec: cluster.Spec.DataVolumeClaimTemplateSpec,
	}
}

// createOrUpdateCronJob doesn't remove cron jobs when the replica number is decreased
func (r *MySQLClusterReconciler) createOrUpdateCronJob(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	isUpdated := false
	for i := int32(0); i < cluster.Spec.Replicas; i++ {
		cronJob := &batchv1beta1.CronJob{}
		cronJob.SetNamespace(cluster.Namespace)
		podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), i)
		cronJob.SetName(podName)

		op, err := ctrl.CreateOrUpdate(ctx, r.Client, cronJob, func() error {
			setStandardLabels(&cronJob.ObjectMeta, cluster)
			cronJob.Spec.Schedule = cluster.Spec.LogRotationSchedule
			cronJob.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
			containers := []corev1.Container{
				{
					Name:    "curl",
					Image:   r.CurlContainerImage,
					Command: []string{"curl", "-sf", fmt.Sprintf("http://%s.%s:%d/rotate?token=%s", podName, moco.UniqueName(cluster), moco.AgentPort, cluster.Status.AgentToken)},
				},
			}
			if !equality.Semantic.DeepDerivative(containers, cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers) {
				cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers = containers
			}
			return ctrl.SetControllerReference(cluster, cronJob, r.Scheme)
		})
		if err != nil {
			log.Error(err, "unable to create-or-update CronJob")
			return isUpdated, err
		}
		if op != controllerutil.OperationResultNone {
			log.Info("reconcile CronJob successfully", "op", op)
			isUpdated = true
		}
	}
	return isUpdated, nil
}

func (r *MySQLClusterReconciler) createOrUpdateServices(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	primaryServiceName := fmt.Sprintf("%s-primary", moco.UniqueName(cluster))
	primaryIsUpdated, err := r.createOrUpdateService(ctx, cluster, primaryServiceName)
	if err != nil {
		log.Error(err, "unable to create-or-update Primary Service")
		return false, err
	}
	if primaryIsUpdated {
		log.Info("reconcile Primary Service successfully")
	}

	replicaServiceName := fmt.Sprintf("%s-replica", moco.UniqueName(cluster))
	replicaIsUpdated, err := r.createOrUpdateService(ctx, cluster, replicaServiceName)
	if err != nil {
		log.Error(err, "unable to create-or-update Replica Service")
		return false, err
	}
	if replicaIsUpdated {
		log.Info("reconcile Replica Service successfully")
	}

	return primaryIsUpdated || replicaIsUpdated, nil
}

func (r *MySQLClusterReconciler) createOrUpdateService(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, svcName string) (bool, error) {
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
		return false, err
	}

	if op != controllerutil.OperationResultNone {
		isUpdated = true
	}
	return isUpdated, nil
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
