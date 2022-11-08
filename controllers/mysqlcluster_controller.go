package controllers

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/clustering"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/cybozu-go/moco/pkg/mycnf"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/google/go-cmp/cmp"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	batchv1ac "k8s.io/client-go/applyconfigurations/batch/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	policyv1ac "k8s.io/client-go/applyconfigurations/policy/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	defaultTerminationGracePeriodSeconds = 300
	fieldManager                         = "moco-controller"
)

// debug and test variables
var (
	debugController = os.Getenv("DEBUG_CONTROLLER") == "1"
	noJobResource   = os.Getenv("TEST_NO_JOB_RESOURCE") == "1"
)

// `controller` should be true only if the resource is created in the same namespace as moco-controller.
func labelSet(cluster *mocov1beta2.MySQLCluster, controller bool) map[string]string {
	labels := map[string]string{
		constants.LabelAppName:      constants.AppNameMySQL,
		constants.LabelAppInstance:  cluster.Name,
		constants.LabelAppCreatedBy: constants.AppCreator,
	}
	if controller {
		labels[constants.LabelAppNamespace] = cluster.Namespace
	}
	return labels
}

func labelSetForJob(cluster *mocov1beta2.MySQLCluster) map[string]string {
	labels := map[string]string{
		constants.LabelAppName:      constants.AppNameBackup,
		constants.LabelAppInstance:  cluster.Name,
		constants.LabelAppCreatedBy: constants.AppCreator,
	}
	return labels
}

func mergeMap(m1, m2 map[string]string) map[string]string {
	m := make(map[string]string)
	for k, v := range m1 {
		m[k] = v
	}
	for k, v := range m2 {
		m[k] = v
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Recorder        record.EventRecorder
	AgentImage      string
	BackupImage     string
	FluentBitImage  string
	ExporterImage   string
	SystemNamespace string
	ClusterManager  clustering.ClusterManager
}

//+kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups=moco.cybozu.com,resources=backuppolicies,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets/status,verbs=get
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services/status,verbs=get
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=serviceaccounts/status,verbs=get
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps/status,verbs=get
//+kubebuilder:rbac:groups="",resources=events,verbs=create;update;patch
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses,verbs=get;list;watch
//+kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="cert-manager.io",resources=certificates,verbs=get;list;watch;create;delete
//+kubebuilder:rbac:groups="batch",resources=cronjobs;jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements Reconciler interface.
// See https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile#Reconciler
func (r *MySQLClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	cluster := &mocov1beta2.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			r.ClusterManager.Stop(req.NamespacedName)
			return ctrl.Result{}, nil
		}

		log.Error(err, "unable to fetch MySQLCluster")
		return ctrl.Result{}, err
	}

	// The highest reconciler version
	reconciler := r.reconcileV1

	// A MySQLCluster reconciler should create or update Kubernetes resources
	// in a consistent manner until the MySQLCluster resource is updated
	// so that MySQL would not get restarted when MOCO is updated.
	// Therefore, we implement multiple reconcilers and gives different
	// versions to them.
	if cluster.Status.ReconcileInfo.Generation == cluster.Generation || cluster.DeletionTimestamp != nil {
		switch cluster.Status.ReconcileInfo.ReconcileVersion {
		case 0:
			// prefer the highest version
		case 1:
			reconciler = r.reconcileV1
		}
	}
	return reconciler(ctx, req, cluster)
}

func (r *MySQLClusterReconciler) reconcileV1(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cluster.DeletionTimestamp != nil {
		if !controllerutil.ContainsFinalizer(cluster, constants.MySQLClusterFinalizer) {
			return ctrl.Result{}, nil
		}

		log.Info("start finalizing MySQLCluster")

		r.ClusterManager.Stop(req.NamespacedName)

		if err := r.finalizeV1(ctx, cluster); err != nil {
			log.Error(err, "failed to finalize")
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(cluster, constants.MySQLClusterFinalizer)
		if err := r.Update(ctx, cluster); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}

		log.Info("finalizing MySQLCluster is completed")

		return ctrl.Result{}, nil
	}

	if err := r.reconcileV1Secret(ctx, req, cluster); err != nil {
		log.Error(err, "failed to reconcile secret")
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1Certificate(ctx, req, cluster); err != nil {
		log.Error(err, "failed to reconcile certificate")
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1GRPCSecret(ctx, req, cluster); err != nil {
		log.Error(err, "failed to reconcile gRPC secret")
		return ctrl.Result{}, err
	}

	mycnf, err := r.reconcileV1MyCnf(ctx, req, cluster)
	if err != nil {
		log.Error(err, "failed to reconcile my.conf config map")
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1FluentBitConfigMap(ctx, req, cluster); err != nil {
		log.Error(err, "failed to reconcile config maps for fluent-bit")
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1ServiceAccount(ctx, req, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1Service(ctx, req, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcilePVC(ctx, req, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1StatefulSet(ctx, req, cluster, mycnf); err != nil {
		log.Error(err, "failed to reconcile stateful set")
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1PDB(ctx, req, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1BackupJob(ctx, req, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1RestoreJob(ctx, req, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if cluster.Status.ReconcileInfo.Generation != cluster.Generation {
		cluster.Status.ReconcileInfo.Generation = cluster.Generation
		cluster.Status.ReconcileInfo.ReconcileVersion = 1
		if err := r.Status().Update(ctx, cluster); err != nil {
			log.Error(err, "failed to update reconciliation info")
			return ctrl.Result{}, err
		}
	}

	r.ClusterManager.Update(client.ObjectKeyFromObject(cluster))
	return ctrl.Result{}, nil
}

func (r *MySQLClusterReconciler) reconcileV1Secret(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	name := cluster.ControllerSecretName()
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: name}, secret)
	if apierrors.IsNotFound(err) {
		passwd, err := password.NewMySQLPassword()
		if err != nil {
			return err
		}

		secret = passwd.ToSecret()
		secret.Namespace = r.SystemNamespace
		secret.Name = name
		secret.Labels = labelSet(cluster, true)
		if err := r.Client.Create(ctx, secret); err != nil {
			return err
		}

		log.Info("created controller Secret", "secretName", name)
	} else if err != nil {
		return err
	}

	if err := r.reconcileUserSecret(ctx, req, cluster, secret); err != nil {
		return err
	}

	if err := r.reconcileMyCnfSecret(ctx, req, cluster, secret); err != nil {
		return err
	}

	return nil
}

func (r *MySQLClusterReconciler) reconcileUserSecret(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster, controllerSecret *corev1.Secret) error {
	log := crlog.FromContext(ctx)

	passwd, err := password.NewMySQLPasswordFromSecret(controllerSecret)
	if err != nil {
		return fmt.Errorf("failed to create password from secret %s/%s: %w", controllerSecret.Namespace, controllerSecret.Name, err)
	}
	newSecret := passwd.ToSecret()

	name := cluster.UserSecretName()
	secret := corev1ac.Secret(name, cluster.Namespace).
		WithAnnotations(newSecret.Annotations).
		WithLabels(labelSet(cluster, false)).
		WithData(newSecret.Data)

	if err := setControllerReferenceWithSecret(cluster, secret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	var orig corev1.Secret
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := corev1ac.ExtractSecret(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(secret, origApplyConfig) {
		return nil
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		return fmt.Errorf("failed to convert Secret %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	if err := r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	}); err != nil {
		return fmt.Errorf("failed to reconcile user Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	log.Info("reconciled user Secret", "secretName", name)

	return nil
}

func (r *MySQLClusterReconciler) reconcileMyCnfSecret(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster, controllerSecret *corev1.Secret) error {
	log := crlog.FromContext(ctx)

	passwd, err := password.NewMySQLPasswordFromSecret(controllerSecret)
	if err != nil {
		return fmt.Errorf("failed to create password from Secret %s/%s: %w", controllerSecret.Namespace, controllerSecret.Name, err)
	}
	mycnfSecret := passwd.ToMyCnfSecret()

	name := cluster.MyCnfSecretName()
	secret := corev1ac.Secret(name, cluster.Namespace).
		WithAnnotations(mycnfSecret.Annotations).
		WithLabels(labelSet(cluster, false)).
		WithData(mycnfSecret.Data)

	if err := setControllerReferenceWithSecret(cluster, secret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	var orig corev1.Secret
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := corev1ac.ExtractSecret(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(secret, origApplyConfig) {
		return nil
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(secret)
	if err != nil {
		return fmt.Errorf("failed to convert Secret %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	if err := r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	}); err != nil {
		return fmt.Errorf("failed to reconcile my.cnf Secret %s/%s: %w", cluster.Namespace, name, err)
	}

	log.Info("reconciled my.cnf Secret", "secretName", name)

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1MyCnf(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) (*corev1ac.ConfigMapApplyConfiguration, error) {
	log := crlog.FromContext(ctx)

	var mysqldContainer *corev1ac.ContainerApplyConfiguration
	for i, c := range cluster.Spec.PodTemplate.Spec.Containers {
		if *c.Name == constants.MysqldContainerName {
			mysqldContainer = &cluster.Spec.PodTemplate.Spec.Containers[i]
			break
		}
	}
	if mysqldContainer == nil {
		return nil, fmt.Errorf("MySQLD container not found")
	}

	// resources.requests.memory takes precedence over resources.limits.memory.
	var totalMem int64
	if mysqldContainer.Resources != nil {
		if mysqldContainer.Resources.Limits != nil {
			if res := mysqldContainer.Resources.Limits.Memory(); !res.IsZero() {
				totalMem = res.Value()
			}
		}

		if mysqldContainer.Resources.Requests != nil {
			if res := mysqldContainer.Resources.Requests.Memory(); !res.IsZero() {
				totalMem = res.Value()
			}
		}
	}

	var userConf map[string]string
	if cluster.Spec.MySQLConfigMapName != nil {
		cm := &corev1.ConfigMap{}
		err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: *cluster.Spec.MySQLConfigMapName}, cm)
		if err != nil {
			log.Error(err, "failed to get specified configmap", "configmap", *cluster.Spec.MySQLConfigMapName)
			return nil, err
		}
		userConf = cm.Data
	}

	conf := mycnf.Generate(userConf, totalMem)

	fnv32a := fnv.New32a()
	fnv32a.Write([]byte(conf))
	suffix := hex.EncodeToString(fnv32a.Sum(nil))

	prefix := cluster.PrefixedName() + "."

	cmName := prefix + suffix
	cmData := map[string]string{
		constants.MySQLConfName: conf,
	}

	cm := corev1ac.ConfigMap(cmName, cluster.Namespace).
		WithLabels(labelSet(cluster, false)).
		WithData(cmData)

	if err := setControllerReferenceWithConfigMap(cluster, cm, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set ownerReference to ConfigMap %s/%s: %w", cluster.Namespace, cmName, err)
	}

	var orig corev1.ConfigMap
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cmName}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get ConfigMap %s/%s: %w", cluster.Namespace, cmName, err)
	}

	origApplyConfig, err := corev1ac.ExtractConfigMap(&orig, fieldManager)
	if err != nil {
		return nil, fmt.Errorf("failed to extract ConfigMap %s/%s: %w", cluster.Namespace, cmName, err)
	}

	if equality.Semantic.DeepEqual(cm, origApplyConfig) {
		return cm, nil
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
	if err != nil {
		return nil, fmt.Errorf("failed to convert ConfigMap %s/%s to unstructured: %w", cluster.Namespace, cmName, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	if err := r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile my.cnf configmap %s/%s: %w", cluster.Namespace, cmName, err)
	}

	log.Info("reconciled my.cnf ConfigMap", "configMapName", cmName)

	cms := &corev1.ConfigMapList{}
	if err := r.List(ctx, cms, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}
	for _, old := range cms.Items {
		if strings.HasPrefix(old.Name, prefix) && old.Name != cmName {
			if err := r.Delete(ctx, &old); err != nil {
				return nil, fmt.Errorf("failed to delete old my.cnf configmap %s/%s: %w", old.Namespace, old.Name, err)
			}
		}
	}

	return cm, nil
}

func (r *MySQLClusterReconciler) reconcileV1FluentBitConfigMap(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	configTmpl := `[SERVICE]
  Log_Level      error
[INPUT]
  Name           tail
  Path           %s
  Read_from_Head true
[OUTPUT]
  Name           file
  Match          *
  Path           /dev
  File           stdout
  Format         template
  Template       {log}
`

	if !cluster.Spec.DisableSlowQueryLogContainer {
		name := cluster.SlowQueryLogAgentConfigMapName()
		confVal := fmt.Sprintf(configTmpl, filepath.Join(constants.LogDirPath, constants.MySQLSlowLogName))
		data := map[string]string{
			constants.FluentBitConfigName: confVal,
		}

		cm := corev1ac.ConfigMap(name, cluster.Namespace).
			WithLabels(labelSet(cluster, false)).
			WithData(data)

		if err := setControllerReferenceWithConfigMap(cluster, cm, r.Scheme); err != nil {
			return fmt.Errorf("failed to set ownerReference to ConfigMap %s/%s: %w", cluster.Namespace, name, err)
		}

		var orig corev1.ConfigMap
		err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get ConfigMap %s/%s: %w", cluster.Namespace, name, err)
		}

		origApplyConfig, err := corev1ac.ExtractConfigMap(&orig, fieldManager)
		if err != nil {
			return fmt.Errorf("failed to extract ConfigMap %s/%s: %w", cluster.Namespace, name, err)
		}

		if equality.Semantic.DeepEqual(cm, origApplyConfig) {
			return nil
		}

		obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
		if err != nil {
			return fmt.Errorf("failed to convert ConfigMap %s/%s to unstructured: %w", cluster.Namespace, name, err)
		}
		patch := &unstructured.Unstructured{
			Object: obj,
		}

		if err := r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
			FieldManager: fieldManager,
			Force:        pointer.Bool(true),
		}); err != nil {
			return fmt.Errorf("failed to reconcile configmap %s/%s for slow logs: %w", cluster.Namespace, name, err)
		}

		log.Info("reconciled ConfigMap for slow logs", "configMapName", name)
	} else {
		cm := &corev1.ConfigMap{}
		cm.Namespace = cluster.Namespace
		cm.Name = cluster.SlowQueryLogAgentConfigMapName()
		err := r.Client.Delete(ctx, cm)
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete configmap for slow logs: %w", err)
		}
	}

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1ServiceAccount(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	name := cluster.PrefixedName()
	sa := corev1ac.ServiceAccount(name, cluster.Namespace).
		WithLabels(labelSet(cluster, false))

	if err := setControllerReferenceWithServiceAccount(cluster, sa, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to Service %s/%s: %w", cluster.Namespace, name, err)
	}

	var orig corev1.ServiceAccount
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ServiceAccount %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := corev1ac.ExtractServiceAccount(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract ServiceAccount %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(sa, origApplyConfig) {
		return nil
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sa)
	if err != nil {
		return fmt.Errorf("failed to convert ServiceAccount %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	if err := r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	}); err != nil {
		return fmt.Errorf("failed to reconcile service account %s/%s: %w", cluster.Namespace, name, err)
	}

	log.Info("reconciled ServiceAccount", "serviceAccountName", name)

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1Service(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	if err := r.reconcileV1Service1(ctx, cluster, nil, cluster.HeadlessServiceName(), true, labelSet(cluster, false)); err != nil {
		return err
	}

	primarySelector := labelSet(cluster, false)
	primarySelector[constants.LabelMocoRole] = constants.RolePrimary
	if err := r.reconcileV1Service1(ctx, cluster, cluster.Spec.PrimaryServiceTemplate, cluster.PrimaryServiceName(), false, primarySelector); err != nil {
		return err
	}

	replicaSelector := labelSet(cluster, false)
	replicaSelector[constants.LabelMocoRole] = constants.RoleReplica
	if err := r.reconcileV1Service1(ctx, cluster, cluster.Spec.ReplicaServiceTemplate, cluster.ReplicaServiceName(), false, replicaSelector); err != nil {
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) reconcileV1Service1(ctx context.Context, cluster *mocov1beta2.MySQLCluster, template *mocov1beta2.ServiceTemplate, name string, headless bool, selector map[string]string) error {
	log := crlog.FromContext(ctx)

	svc := corev1ac.Service(name, cluster.Namespace).WithSpec(corev1ac.ServiceSpec())

	tmpl := template.DeepCopy()

	if !headless && tmpl != nil {
		svc.WithAnnotations(tmpl.Annotations).
			WithLabels(tmpl.Labels).
			WithLabels(labelSet(cluster, false))

		if tmpl.Spec != nil {
			s := (*corev1ac.ServiceSpecApplyConfiguration)(tmpl.Spec)
			svc.WithSpec(s)
		}
	} else {
		svc.WithLabels(labelSet(cluster, false))
	}

	if headless {
		svc.Spec.WithClusterIP(corev1.ClusterIPNone).
			WithType(corev1.ServiceTypeClusterIP).
			WithPublishNotReadyAddresses(true)
	}

	svc.Spec.WithSelector(selector)

	svc.Spec.WithPorts(
		corev1ac.ServicePort().
			WithName(constants.MySQLPortName).
			WithProtocol(corev1.ProtocolTCP).
			WithPort(constants.MySQLPort).
			WithTargetPort(intstr.FromString(constants.MySQLPortName)),
		corev1ac.ServicePort().
			WithName(constants.MySQLXPortName).
			WithProtocol(corev1.ProtocolTCP).
			WithPort(constants.MySQLXPort).
			WithTargetPort(intstr.FromString(constants.MySQLXPortName)),
	)

	if err := setControllerReferenceWithService(cluster, svc, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to Service %s/%s: %w", cluster.Namespace, name, err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(svc)
	if err != nil {
		return fmt.Errorf("failed to convert Service %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	var orig corev1.Service
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Service %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := corev1ac.ExtractService(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract Service %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(svc, origApplyConfig) {
		return nil
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s service: %w", name, err)
	}

	if debugController {
		var updated corev1.Service

		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get Service %s/%s: %w", cluster.Namespace, name, err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled Service", "serviceName", name)

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1StatefulSet(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster, mycnf *corev1ac.ConfigMapApplyConfiguration) error {
	log := crlog.FromContext(ctx)

	var orig appsv1.StatefulSet
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.PrefixedName()}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get StatefulSet %s/%s: %w", cluster.Namespace, cluster.PrefixedName(), err)
	}

	sts := appsv1ac.StatefulSet(cluster.PrefixedName(), cluster.Namespace).
		WithLabels(labelSet(cluster, false)).
		WithSpec(appsv1ac.StatefulSetSpec().
			WithReplicas(cluster.Spec.Replicas).
			WithSelector(metav1ac.LabelSelector().
				WithMatchLabels(labelSet(cluster, false))).
			WithPodManagementPolicy(appsv1.ParallelPodManagement).
			WithUpdateStrategy(appsv1ac.StatefulSetUpdateStrategy().
				WithType(appsv1.RollingUpdateStatefulSetStrategyType)).
			WithServiceName(cluster.HeadlessServiceName()))

	volumeClaimTemplates := make([]*corev1ac.PersistentVolumeClaimApplyConfiguration, 0, len(cluster.Spec.VolumeClaimTemplates))
	for _, v := range cluster.Spec.VolumeClaimTemplates {
		pvc := v.ToCoreV1()

		var origPVC *corev1.PersistentVolumeClaim
		for _, origV := range orig.Spec.VolumeClaimTemplates {
			if pvc.Name != nil && *pvc.Name == origV.Name {
				origPVC = origV.DeepCopy()
				break
			}
		}

		if err := setControllerReferenceWithPVC(cluster, pvc, origPVC, r.Scheme); err != nil {
			return fmt.Errorf("failed to set ownerReference to PVC %s/%s: %w", cluster.Namespace, *pvc.Name, err)
		}

		volumeClaimTemplates = append(volumeClaimTemplates, pvc)
	}
	sts.Spec.WithVolumeClaimTemplates(volumeClaimTemplates...)

	sts.Spec.WithTemplate(corev1ac.PodTemplateSpec().
		WithAnnotations(cluster.Spec.PodTemplate.Annotations).
		WithLabels(cluster.Spec.PodTemplate.Labels).
		WithLabels(labelSet(cluster, false)))

	podSpec := corev1ac.PodSpecApplyConfiguration(*cluster.Spec.PodTemplate.Spec.DeepCopy())
	podSpec.WithServiceAccountName(cluster.PrefixedName())

	if podSpec.TerminationGracePeriodSeconds == nil {
		podSpec.WithTerminationGracePeriodSeconds(defaultTerminationGracePeriodSeconds)
	}

	if mycnf.Name == nil {
		return errors.New("unexpected error: my.conf ConfigMap name is nil")
	}

	podSpec.WithVolumes(
		corev1ac.Volume().
			WithName(constants.TmpVolumeName).
			// If you use this, the EmptyDir will not be nil and will not match for "equality.Semantic.DeepEqual".
			// WithEmptyDir(corev1ac.EmptyDirVolumeSource()),
			WithEmptyDir(nil),
		corev1ac.Volume().
			WithName(constants.RunVolumeName).
			WithEmptyDir(nil),
		corev1ac.Volume().
			WithName(constants.VarLogVolumeName).
			WithEmptyDir(nil),
		corev1ac.Volume().
			WithName(constants.MySQLInitConfVolumeName).
			WithEmptyDir(nil),
		corev1ac.Volume().
			WithName(constants.SharedVolumeName).
			WithEmptyDir(nil),
		corev1ac.Volume().
			WithName(constants.MySQLConfVolumeName).
			WithConfigMap(corev1ac.ConfigMapVolumeSource().
				WithName(*mycnf.Name).WithDefaultMode(0644)),
		corev1ac.Volume().
			WithName(constants.MySQLConfSecretVolumeName).
			WithSecret(corev1ac.SecretVolumeSource().
				WithSecretName(cluster.MyCnfSecretName()).
				WithDefaultMode(0644)),
		corev1ac.Volume().
			WithName(constants.GRPCSecretVolumeName).
			WithSecret(corev1ac.SecretVolumeSource().
				WithSecretName(cluster.GRPCSecretName()).
				WithDefaultMode(0644)),
	)

	if !cluster.Spec.DisableSlowQueryLogContainer {
		podSpec.WithVolumes(
			corev1ac.Volume().
				WithName(constants.SlowQueryLogAgentConfigVolumeName).
				WithConfigMap(corev1ac.ConfigMapVolumeSource().
					WithName(cluster.SlowQueryLogAgentConfigMapName()).
					WithDefaultMode(0644)),
		)
	}

	containers := make([]*corev1ac.ContainerApplyConfiguration, 0, 4)

	mysqldContainer, err := r.makeV1MySQLDContainer(cluster)
	if err != nil {
		return err
	}
	containers = append(containers, mysqldContainer)
	containers = append(containers, r.makeV1AgentContainer(cluster))

	if !cluster.Spec.DisableSlowQueryLogContainer {
		force := cluster.Status.ReconcileInfo.Generation != cluster.Generation
		sts, err := appsv1ac.ExtractStatefulSet(&orig, fieldManager)
		if err != nil {
			return fmt.Errorf("failed to extract StatefulSet: %w", err)
		}

		containers = append(containers, r.makeV1SlowQueryLogContainer(cluster, sts, force))
	}
	if len(cluster.Spec.Collectors) > 0 {
		containers = append(containers, r.makeV1ExporterContainer(cluster, cluster.Spec.Collectors))
	}
	containers = append(containers, r.makeV1OptionalContainers(cluster)...)

	if mysqldContainer.Image == nil {
		return fmt.Errorf("unexpected mysqld container definition with MySQLCluster %s/%s: image is nil", cluster.Namespace, cluster.Name)
	}
	initContainers, err := r.makeV1InitContainer(ctx, cluster, *mysqldContainer.Image)
	if err != nil {
		return err
	}

	podSpec.Containers = nil
	podSpec.InitContainers = nil
	podSpec.WithContainers(containers...)
	podSpec.WithInitContainers(initContainers...)

	if podSpec.SecurityContext == nil {
		podSpec.WithSecurityContext(corev1ac.PodSecurityContext())
	}
	if podSpec.SecurityContext.FSGroup == nil {
		podSpec.SecurityContext.WithFSGroup(constants.ContainerGID)
	}
	if podSpec.SecurityContext.FSGroupChangePolicy == nil {
		podSpec.SecurityContext.WithFSGroupChangePolicy(corev1.FSGroupChangeOnRootMismatch)
	}

	sts.Spec.Template.WithSpec(&podSpec)

	if err := setControllerReferenceWithStatefulSet(cluster, sts, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to StatefulSet %s/%s: %w", cluster.Namespace, cluster.PrefixedName(), err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sts)
	if err != nil {
		return fmt.Errorf("failed to convert StatefulSet %s/%s to unstructured: %w", cluster.Namespace, cluster.PrefixedName(), err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	origApplyConfig, err := appsv1ac.ExtractStatefulSet(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract StatefulSet %s/%s: %w", cluster.Namespace, cluster.PrefixedName(), err)
	}

	if equality.Semantic.DeepEqual(sts, origApplyConfig) {
		return nil
	}

	needRecreate := false

	// Recreate StatefulSet if VolumeClaimTemplates has differences.
	// sts will never be nil.
	if origApplyConfig != nil && origApplyConfig.Spec != nil && origApplyConfig.Spec.VolumeClaimTemplates != nil {
		if !equality.Semantic.DeepEqual(sts.Spec.VolumeClaimTemplates, origApplyConfig.Spec.VolumeClaimTemplates) {
			needRecreate = true

			// Don’t delete the Pod, only delete the StatefulSet.
			// Same behavior as `kubectl delete sts moco-xxx --cascade=orphan`
			opt := metav1.DeletePropagationOrphan
			if err := r.Delete(ctx, &orig, &client.DeleteOptions{
				PropagationPolicy: &opt,
			}); err != nil {
				metrics.StatefulSetRecreateErrorTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				return err
			}

			log.Info("volumeClaimTemplates has changed, delete StatefulSet and try to recreate it", "statefulSetName", cluster.PrefixedName())

			// When DeletePropagationOrphan is used to delete, it waits because it is not deleted immediately.
			if err := wait.PollImmediate(time.Millisecond*500, time.Second*5, func() (bool, error) {
				err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.PrefixedName()}, &appsv1.StatefulSet{})
				if err != nil {
					if apierrors.IsNotFound(err) {
						return true, nil
					}
					return false, err
				}

				return false, nil
			}); err != nil {
				return fmt.Errorf("re-creation failed the StatefulSet %s/%s has not been deleted: %w", cluster.Namespace, cluster.PrefixedName(), err)
			}
		}
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		if needRecreate {
			metrics.StatefulSetRecreateErrorTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		}
		return fmt.Errorf("failed to reconcile stateful set: %w", err)
	}

	if needRecreate {
		metrics.StatefulSetRecreateTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
	}

	if debugController {
		var updated appsv1.StatefulSet
		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.PrefixedName()}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get StatefulSet %s/%s: %w", cluster.Namespace, cluster.PrefixedName(), err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled StatefulSet", "statefulSetName", cluster.PrefixedName())

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1PDB(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	pdb := &policyv1.PodDisruptionBudget{}
	pdb.Namespace = cluster.Namespace
	pdb.Name = cluster.PrefixedName()

	if cluster.Spec.Replicas < 3 {
		err := r.Delete(ctx, pdb)
		if err == nil {
			log.Info("removed pod disruption budget")
		}
		return client.IgnoreNotFound(err)
	}

	maxUnavailable := intstr.FromInt(int(cluster.Spec.Replicas / 2))

	pdbApplyConfig := policyv1ac.PodDisruptionBudget(pdb.Name, pdb.Namespace).
		WithLabels(labelSet(cluster, false)).
		WithSpec(policyv1ac.PodDisruptionBudgetSpec().
			WithMaxUnavailable(maxUnavailable).
			WithSelector(metav1ac.LabelSelector().
				WithMatchLabels(labelSet(cluster, false)),
			),
		)

	if err := setControllerReferenceWithPDB(cluster, pdbApplyConfig, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to PDB %s/%s: %w", pdb.Namespace, pdb.Name, err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pdbApplyConfig)
	if err != nil {
		return fmt.Errorf("failed to convert PDB %s/%s to unstructured: %w", pdb.Namespace, pdb.Name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	var orig policyv1.PodDisruptionBudget
	err = r.Get(ctx, client.ObjectKey{Namespace: pdb.Namespace, Name: pdb.Name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get PDB %s/%s: %w", pdb.Namespace, pdb.Name, err)
	}

	origApplyConfig, err := policyv1ac.ExtractPodDisruptionBudget(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract PDB %s/%s: %w", pdb.Namespace, pdb.Name, err)
	}

	if equality.Semantic.DeepEqual(pdbApplyConfig, origApplyConfig) {
		return nil
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s PDB: %w", pdb.Name, err)
	}

	if debugController {
		var updated policyv1.PodDisruptionBudget

		if err := r.Get(ctx, client.ObjectKey{Namespace: pdb.Namespace, Name: pdb.Name}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get PDB %s/%s: %w", pdb.Namespace, pdb.Name, err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled PDB", "pdbName", pdb.Name)

	return nil
}

func bucketArgs(bc mocov1beta2.BucketConfig) []string {
	var args []string
	if bc.Region != "" {
		args = append(args, "--region="+bc.Region)
	}
	if bc.EndpointURL != "" {
		args = append(args, "--endpoint="+bc.EndpointURL)
	}
	if bc.UsePathStyle {
		args = append(args, "--use-path-style")
	}
	return append(args, bc.BucketName)
}

func (r *MySQLClusterReconciler) reconcileV1BackupJob(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	if cluster.Spec.BackupPolicyName == nil {
		cj := &batchv1.CronJob{}
		err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.BackupCronJobName()}, cj)
		if err == nil {
			if err := r.Delete(ctx, cj); err != nil {
				log.Error(err, "failed to delete CronJob")
				return err
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}

		role := &rbacv1.Role{}
		err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.BackupRoleName()}, role)
		if err == nil {
			if err := r.Delete(ctx, role); err != nil {
				log.Error(err, "failed to delete Role")
				return err
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		rolebinding := &rbacv1.RoleBinding{}
		err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.BackupRoleName()}, rolebinding)
		if err == nil {
			if err := r.Delete(ctx, rolebinding); err != nil {
				log.Error(err, "failed to delete RoleBinding")
				return err
			}
		} else if !apierrors.IsNotFound(err) {
			return err
		}

		return nil
	}

	bpName := *cluster.Spec.BackupPolicyName
	bp := &mocov1beta2.BackupPolicy{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: bpName}, bp); err != nil {
		return fmt.Errorf("failed to get backup policy %s/%s: %w", cluster.Namespace, bpName, err)
	}

	jc := &bp.Spec.JobConfig

	args := []string{constants.BackupSubcommand, fmt.Sprintf("--threads=%d", jc.Threads)}
	args = append(args, bucketArgs(jc.BucketConfig)...)
	args = append(args, cluster.Namespace, cluster.Name)

	resources := corev1ac.ResourceRequirements()
	if jc.Memory != nil {
		resources.WithRequests(corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(int64(jc.Threads), resource.DecimalSI),
			corev1.ResourceMemory: *jc.Memory,
		})
	} else {
		resources.WithRequests(
			corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewQuantity(int64(jc.Threads), resource.DecimalSI),
			},
		)
	}
	if jc.MaxMemory != nil {
		resources.WithLimits(corev1.ResourceList{
			corev1.ResourceMemory: *jc.MaxMemory,
		})
	}
	if noJobResource {
		resources = corev1ac.ResourceRequirements()
	}

	container := corev1ac.Container().
		WithName("backup").
		WithImage(r.BackupImage).
		WithArgs(args...).
		WithEnv(corev1ac.EnvVar().
			WithName("MYSQL_PASSWORD").
			WithValueFrom(corev1ac.EnvVarSource().
				WithSecretKeyRef(corev1ac.SecretKeySelector().
					WithKey(password.BackupPasswordKey).
					WithName(cluster.UserSecretName()),
				),
			),
		).
		WithEnv(func() []*corev1ac.EnvVarApplyConfiguration {
			envFrom := make([]*corev1ac.EnvVarApplyConfiguration, 0, len(jc.Env))
			for _, e := range jc.Env {
				e := e
				envFrom = append(envFrom, (*corev1ac.EnvVarApplyConfiguration)(&e))
			}
			return envFrom
		}()...).
		WithEnvFrom(func() []*corev1ac.EnvFromSourceApplyConfiguration {
			envFrom := make([]*corev1ac.EnvFromSourceApplyConfiguration, 0, len(jc.EnvFrom))
			for _, e := range jc.EnvFrom {
				e := e
				envFrom = append(envFrom, (*corev1ac.EnvFromSourceApplyConfiguration)(&e))
			}
			return envFrom
		}()...).
		WithVolumeMounts(corev1ac.VolumeMount().
			WithName("work").
			WithMountPath("/work"),
		).
		WithSecurityContext(corev1ac.SecurityContext().WithReadOnlyRootFilesystem(true)).
		WithResources(resources)

	updateContainerWithSecurityContext(container)

	cronJobName := cluster.BackupCronJobName()
	cronJob := batchv1ac.CronJob(cronJobName, cluster.Namespace).
		WithLabels(labelSetForJob(cluster)).
		WithSpec(batchv1ac.CronJobSpec().
			WithSchedule(bp.Spec.Schedule).
			WithConcurrencyPolicy(bp.Spec.ConcurrencyPolicy).
			WithJobTemplate(batchv1ac.JobTemplateSpec().
				WithLabels(labelSetForJob(cluster)).
				WithSpec(batchv1ac.JobSpec().
					WithTemplate(corev1ac.PodTemplateSpec().
						WithLabels(labelSetForJob(cluster)).
						WithSpec(corev1ac.PodSpec().
							WithRestartPolicy(corev1.RestartPolicyNever).
							WithServiceAccountName(bp.Spec.JobConfig.ServiceAccountName).
							WithVolumes(&corev1ac.VolumeApplyConfiguration{
								Name:                           pointer.String("work"),
								VolumeSourceApplyConfiguration: corev1ac.VolumeSourceApplyConfiguration(*jc.WorkVolume.DeepCopy()),
							}).
							WithContainers(container).
							WithSecurityContext(corev1ac.PodSecurityContext().
								WithFSGroup(constants.ContainerGID).
								WithFSGroupChangePolicy(corev1.FSGroupChangeOnRootMismatch),
							),
						),
					),
				),
			),
		)

	if bp.Spec.StartingDeadlineSeconds != nil {
		cronJob.Spec.WithStartingDeadlineSeconds(*bp.Spec.StartingDeadlineSeconds)
	}
	if bp.Spec.SuccessfulJobsHistoryLimit != nil {
		cronJob.Spec.WithSuccessfulJobsHistoryLimit(*bp.Spec.SuccessfulJobsHistoryLimit)
	}
	if bp.Spec.FailedJobsHistoryLimit != nil {
		cronJob.Spec.WithFailedJobsHistoryLimit(*bp.Spec.FailedJobsHistoryLimit)
	}
	if bp.Spec.ActiveDeadlineSeconds != nil {
		cronJob.Spec.JobTemplate.Spec.WithActiveDeadlineSeconds(*bp.Spec.ActiveDeadlineSeconds)
	}
	if bp.Spec.BackoffLimit != nil {
		cronJob.Spec.JobTemplate.Spec.WithBackoffLimit(*bp.Spec.BackoffLimit)
	}

	if err := setControllerReferenceWithCronJob(cluster, cronJob, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to CronJob %s/%s: %w", cluster.Namespace, cronJobName, err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cronJob)
	if err != nil {
		return fmt.Errorf("failed to convert CronJob %s/%s to unstructured: %w", cluster.Namespace, cronJobName, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	var orig batchv1.CronJob
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cronJobName}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get CronJob %s/%s: %w", cluster.Namespace, cronJobName, err)
	}

	origApplyConfig, err := batchv1ac.ExtractCronJob(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract CronJob %s/%s: %w", cluster.Namespace, cronJobName, err)
	}

	if equality.Semantic.DeepEqual(cronJob, origApplyConfig) {
		return nil
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s CronJob for backup: %w", cronJobName, err)
	}

	if debugController {
		var updated batchv1.CronJob

		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cronJobName}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get CronJob %s/%s: %w", cluster.Namespace, cronJobName, err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled CronJob for backup", "cronJobName", cronJobName)

	if err := r.reconcileV1BackupJobRole(ctx, req, cluster); err != nil {
		return err
	}

	if err := r.reconcileV1BackupJobRoleBinding(ctx, req, cluster, bp); err != nil {
		return err
	}

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1BackupJobRole(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	name := cluster.BackupRoleName()
	role := rbacv1ac.Role(name, cluster.Namespace).
		WithLabels(labelSetForJob(cluster)).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups(mocov1beta2.GroupVersion.Group).
				WithResources("mysqlclusters", "mysqlclusters/status").
				WithVerbs("get", "update").
				WithResourceNames(cluster.Name),
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("pods").
				WithVerbs("get", "list", "watch"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("events").
				WithVerbs("create", "update", "patch"),
		)

	if err := setControllerReferenceWithRole(cluster, role, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to Role %s/%s: %w", cluster.Namespace, name, err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(role)
	if err != nil {
		return fmt.Errorf("failed to convert Role %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	var orig rbacv1.Role
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Role %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := rbacv1ac.ExtractRole(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract Role %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(role, origApplyConfig) {
		return nil
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s Role for backup: %w", name, err)
	}

	if debugController {
		var updated rbacv1.Role

		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get Role %s/%s: %w", cluster.Namespace, name, err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled Role for backup", "roleName", name)

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1BackupJobRoleBinding(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster, bp *mocov1beta2.BackupPolicy) error {
	log := crlog.FromContext(ctx)

	name := cluster.BackupRoleName()
	roleBinding := rbacv1ac.RoleBinding(name, cluster.Namespace).
		WithLabels(labelSetForJob(cluster)).
		WithRoleRef(rbacv1ac.RoleRef().
			WithAPIGroup(rbacv1.SchemeGroupVersion.Group).
			WithKind("Role").
			WithName(cluster.BackupRoleName())).
		WithSubjects(rbacv1ac.Subject().
			WithKind("ServiceAccount").
			WithName(bp.Spec.JobConfig.ServiceAccountName).
			WithNamespace(cluster.Namespace))

	if err := setControllerReferenceWithRoleBinding(cluster, roleBinding, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to RoleBinding %s/%s: %w", cluster.Namespace, name, err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(roleBinding)
	if err != nil {
		return fmt.Errorf("failed to convert RoleBinding %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	var orig rbacv1.RoleBinding
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get RoleBinding %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := rbacv1ac.ExtractRoleBinding(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract RoleBinding %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(roleBinding, origApplyConfig) {
		return nil
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s RoleBinding for backup: %w", name, err)
	}

	if debugController {
		var updated rbacv1.RoleBinding

		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get RoleBinding %s/%s: %w", cluster.Namespace, name, err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled RoleBinding for backup", "roleBindingName", name)

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1RestoreJob(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	// `spec.restore` is not editable, so we can safely return early if it is nil.
	if cluster.Spec.Restore == nil {
		return nil
	}
	// the restoration has already finished successfully.
	if cluster.Status.RestoredTime != nil {
		return nil
	}

	log := crlog.FromContext(ctx)

	job := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.RestoreJobName()}, job)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		jc := &cluster.Spec.Restore.JobConfig

		args := []string{constants.RestoreSubcommand, fmt.Sprintf("--threads=%d", jc.Threads)}
		args = append(args, bucketArgs(jc.BucketConfig)...)
		args = append(args, cluster.Spec.Restore.SourceNamespace, cluster.Spec.Restore.SourceName)
		args = append(args, cluster.Namespace, cluster.Name)
		args = append(args, cluster.Spec.Restore.RestorePoint.UTC().Format(constants.BackupTimeFormat))

		resources := corev1ac.ResourceRequirements()
		if jc.Memory != nil {
			resources.WithRequests(corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(int64(jc.Threads), resource.DecimalSI),
				corev1.ResourceMemory: *jc.Memory,
			})
		} else {
			resources.WithRequests(
				corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(int64(jc.Threads), resource.DecimalSI),
				},
			)
		}
		if jc.MaxMemory != nil {
			resources.WithLimits(corev1.ResourceList{
				corev1.ResourceMemory: *jc.MaxMemory,
			})
		}
		if noJobResource {
			resources = corev1ac.ResourceRequirements()
		}

		container := corev1ac.Container().
			WithName("restore").
			WithImage(r.BackupImage).
			WithArgs(args...).
			WithEnv(corev1ac.EnvVar().
				WithName("MYSQL_PASSWORD").
				WithValueFrom(corev1ac.EnvVarSource().
					WithSecretKeyRef(corev1ac.SecretKeySelector().
						WithKey(password.AdminPasswordKey).
						WithName(cluster.UserSecretName()),
					),
				),
			).
			WithEnv(func() []*corev1ac.EnvVarApplyConfiguration {
				envFrom := make([]*corev1ac.EnvVarApplyConfiguration, 0, len(jc.Env))
				for _, e := range jc.Env {
					e := e
					envFrom = append(envFrom, (*corev1ac.EnvVarApplyConfiguration)(&e))
				}
				return envFrom
			}()...).
			WithEnvFrom(func() []*corev1ac.EnvFromSourceApplyConfiguration {
				envFrom := make([]*corev1ac.EnvFromSourceApplyConfiguration, 0, len(jc.EnvFrom))
				for _, e := range jc.EnvFrom {
					e := e
					envFrom = append(envFrom, (*corev1ac.EnvFromSourceApplyConfiguration)(&e))
				}
				return envFrom
			}()...).
			WithVolumeMounts(corev1ac.VolumeMount().
				WithName("work").
				WithMountPath("/work")).
			WithSecurityContext(corev1ac.SecurityContext().WithReadOnlyRootFilesystem(true)).
			WithResources(resources)

		jobName := cluster.RestoreJobName()
		job := batchv1ac.Job(jobName, cluster.Namespace).
			WithLabels(labelSetForJob(cluster)).
			WithSpec(batchv1ac.JobSpec().
				WithBackoffLimit(0).
				WithTemplate(corev1ac.PodTemplateSpec().
					WithLabels(labelSetForJob(cluster)).
					WithSpec(corev1ac.PodSpec().
						WithRestartPolicy(corev1.RestartPolicyNever).
						WithServiceAccountName(cluster.Spec.Restore.JobConfig.ServiceAccountName).
						WithVolumes(&corev1ac.VolumeApplyConfiguration{
							Name:                           pointer.String("work"),
							VolumeSourceApplyConfiguration: corev1ac.VolumeSourceApplyConfiguration(*cluster.Spec.Restore.JobConfig.WorkVolume.DeepCopy()),
						}).
						WithContainers(container).
						WithSecurityContext(corev1ac.PodSecurityContext().
							WithFSGroup(constants.ContainerGID).
							WithFSGroupChangePolicy(corev1.FSGroupChangeOnRootMismatch),
						),
					),
				),
			)

		if err := setControllerReferenceWithJob(cluster, job, r.Scheme); err != nil {
			return fmt.Errorf("failed to set ownerReference to Job %s/%s: %w", cluster.Namespace, jobName, err)
		}

		obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(job)
		if err != nil {
			return fmt.Errorf("failed to convert Job %s/%s to unstructured: %w", cluster.Namespace, jobName, err)
		}
		patch := &unstructured.Unstructured{
			Object: obj,
		}

		var orig batchv1.Job
		err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: jobName}, &orig)
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get Job %s/%s: %w", cluster.Namespace, jobName, err)
		}

		origApplyConfig, err := batchv1ac.ExtractJob(&orig, fieldManager)
		if err != nil {
			return fmt.Errorf("failed to extract Job %s/%s: %w", cluster.Namespace, jobName, err)
		}

		if equality.Semantic.DeepEqual(job, origApplyConfig) {
			return nil
		}

		err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
			FieldManager: fieldManager,
			Force:        pointer.Bool(true),
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile %s Job for backup: %w", jobName, err)
		}

		if debugController {
			var updated batchv1.Job

			if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: jobName}, &updated); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get Job %s/%s: %w", cluster.Namespace, jobName, err)
			}

			if diff := cmp.Diff(orig, updated); len(diff) > 0 {
				fmt.Println(diff)
			}
		}

		log.Info("reconciled Job for restore", "jobName", jobName)
	}

	if err := r.reconcileV1RestoreJobRole(ctx, req, cluster); err != nil {
		return err
	}

	if err := r.reconcileV1RestoreJobRoleBinding(ctx, req, cluster); err != nil {
		return err
	}

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1RestoreJobRole(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	name := cluster.RestoreRoleName()
	role := rbacv1ac.Role(name, cluster.Namespace).
		WithLabels(labelSetForJob(cluster)).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups(mocov1beta2.GroupVersion.Group).
				WithResources("mysqlclusters", "mysqlclusters/status").
				WithVerbs("get", "update").
				WithResourceNames(cluster.Name),
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("pods").
				WithVerbs("get"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("events").
				WithVerbs("create"),
		)

	if err := setControllerReferenceWithRole(cluster, role, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to Role %s/%s: %w", cluster.Namespace, name, err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(role)
	if err != nil {
		return fmt.Errorf("failed to convert Role %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	var orig rbacv1.Role
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get Role %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := rbacv1ac.ExtractRole(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract Role %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(role, origApplyConfig) {
		return nil
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s Role for backup: %w", name, err)
	}

	if debugController {
		var updated rbacv1.Role

		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get Role %s/%s: %w", cluster.Namespace, name, err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled Role for backup", "roleName", name)

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1RestoreJobRoleBinding(ctx context.Context, req ctrl.Request, cluster *mocov1beta2.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	name := cluster.RestoreRoleName()
	roleBinding := rbacv1ac.RoleBinding(name, cluster.Namespace).
		WithLabels(labelSetForJob(cluster)).
		WithRoleRef(rbacv1ac.RoleRef().
			WithAPIGroup(rbacv1.SchemeGroupVersion.Group).
			WithKind("Role").
			WithName(cluster.RestoreRoleName())).
		WithSubjects(rbacv1ac.Subject().
			WithKind("ServiceAccount").
			WithName(cluster.Spec.Restore.JobConfig.ServiceAccountName).
			WithNamespace(cluster.Namespace))

	if err := setControllerReferenceWithRoleBinding(cluster, roleBinding, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerReference to RoleBinding %s/%s: %w", cluster.Namespace, name, err)
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(roleBinding)
	if err != nil {
		return fmt.Errorf("failed to convert RoleBinding %s/%s to unstructured: %w", cluster.Namespace, name, err)
	}
	patch := &unstructured.Unstructured{
		Object: obj,
	}

	var orig rbacv1.RoleBinding
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &orig)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get RoleBinding %s/%s: %w", cluster.Namespace, name, err)
	}

	origApplyConfig, err := rbacv1ac.ExtractRoleBinding(&orig, fieldManager)
	if err != nil {
		return fmt.Errorf("failed to extract RoleBinding %s/%s: %w", cluster.Namespace, name, err)
	}

	if equality.Semantic.DeepEqual(roleBinding, origApplyConfig) {
		return nil
	}

	err = r.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        pointer.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s RoleBinding for backup: %w", name, err)
	}

	if debugController {
		var updated rbacv1.RoleBinding

		if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: name}, &updated); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get RoleBinding %s/%s: %w", cluster.Namespace, name, err)
		}

		if diff := cmp.Diff(orig, updated); len(diff) > 0 {
			fmt.Println(diff)
		}
	}

	log.Info("reconciled RoleBinding for restore", "roleBindingName", name)

	return nil
}

func (r *MySQLClusterReconciler) finalizeV1(ctx context.Context, cluster *mocov1beta2.MySQLCluster) error {
	secretName := cluster.ControllerSecretName()
	secret := &corev1.Secret{}
	secret.SetNamespace(r.SystemNamespace)
	secret.SetName(secretName)
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete controller secret %s: %w", secretName, err)
	}

	certName := cluster.CertificateName()
	cert := certificateObj.DeepCopy()
	cert.SetNamespace(r.SystemNamespace)
	cert.SetName(certName)
	if err := r.Delete(ctx, cert); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete certificate %s: %w", certName, err)
	}

	return nil
}

func setControllerReferenceWithConfigMap(cluster *mocov1beta2.MySQLCluster, cm *corev1ac.ConfigMapApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	cm.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithSecret(cluster *mocov1beta2.MySQLCluster, secret *corev1ac.SecretApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	secret.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithService(cluster *mocov1beta2.MySQLCluster, svc *corev1ac.ServiceApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	svc.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithStatefulSet(cluster *mocov1beta2.MySQLCluster, sts *appsv1ac.StatefulSetApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	sts.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithPVC(cluster *mocov1beta2.MySQLCluster, pvc *corev1ac.PersistentVolumeClaimApplyConfiguration, origPVC *corev1.PersistentVolumeClaim, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}

	// Apply to StatefulSet fails if API version of PVC 'ownerReferences' set in 'volumeClaimTemplates' of StatefulSet is different.
	// This is because StatefulSet does not allow updates except for 'replicas', 'template', and 'updateStrategy'.
	// If origPVC exists, set apiVersion to match.
	apiVersion := gvk.GroupVersion().String()
	if origPVC != nil {
		for _, owner := range origPVC.OwnerReferences {
			if owner.UID == cluster.GetUID() && apiVersion != owner.APIVersion {
				apiVersion = owner.APIVersion
			}
		}
	}

	pvc.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(apiVersion).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))

	return nil
}

func setControllerReferenceWithServiceAccount(cluster *mocov1beta2.MySQLCluster, sa *corev1ac.ServiceAccountApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	sa.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithPDB(cluster *mocov1beta2.MySQLCluster, pdb *policyv1ac.PodDisruptionBudgetApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	pdb.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithRole(cluster *mocov1beta2.MySQLCluster, role *rbacv1ac.RoleApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	role.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithRoleBinding(cluster *mocov1beta2.MySQLCluster, roleBinding *rbacv1ac.RoleBindingApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	roleBinding.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithJob(cluster *mocov1beta2.MySQLCluster, job *batchv1ac.JobApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	job.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

func setControllerReferenceWithCronJob(cluster *mocov1beta2.MySQLCluster, cronJob *batchv1ac.CronJobApplyConfiguration, scheme *runtime.Scheme) error {
	gvk, err := apiutil.GVKForObject(cluster, scheme)
	if err != nil {
		return err
	}
	cronJob.WithOwnerReferences(metav1ac.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(cluster.Name).
		WithUID(cluster.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true))
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	certHandler := handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
		// the certificate name is formatted as "moco-agent-<cluster.Namespace>.<cluster.Name>"
		if a.GetNamespace() != r.SystemNamespace {
			return nil
		}

		name := a.GetName()
		if !strings.HasPrefix(name, "moco-agent-") {
			return nil
		}
		fields := strings.SplitN(name[len("moco-agent-"):], ".", 2)
		if len(fields) != 2 {
			return nil
		}
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Namespace: fields[0], Name: fields[1]}},
		}
	})

	configMapHandler := handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
		clusters := &mocov1beta2.MySQLClusterList{}
		if err := r.List(context.Background(), clusters, client.InNamespace(a.GetNamespace())); err != nil {
			return nil
		}
		var req []reconcile.Request
		for _, c := range clusters.Items {
			if c.Spec.MySQLConfigMapName == nil {
				continue
			}
			if *c.Spec.MySQLConfigMapName == a.GetName() {
				req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&c)})
			}
		}
		return req
	})

	backupPolicyHandler := handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
		clusters := &mocov1beta2.MySQLClusterList{}
		if err := r.List(context.Background(), clusters, client.InNamespace(a.GetNamespace())); err != nil {
			return nil
		}
		var req []reconcile.Request
		for _, c := range clusters.Items {
			if c.Spec.BackupPolicyName == nil {
				continue
			}
			if *c.Spec.BackupPolicyName == a.GetName() {
				req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&c)})
			}
		}
		return req
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1beta2.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&batchv1.CronJob{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&batchv1.Job{}).
		Watches(&source.Kind{Type: certificateObj}, certHandler).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, configMapHandler).
		Watches(&source.Kind{Type: &mocov1beta2.BackupPolicy{}}, backupPolicyHandler).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: 8},
		).
		Complete(r)
}
