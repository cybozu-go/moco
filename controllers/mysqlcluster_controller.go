package controllers

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/clustering"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/mycnf"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/google/go-cmp/cmp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

const defaultTerminationGracePeriodSeconds = 300

var debugController = os.Getenv("DEBUG_CONTROLLER") == "1"

// `controller` should be true only if the resource is created in the same namespace as moco-controller.
func labelSet(cluster *mocov1beta1.MySQLCluster, controller bool) map[string]string {
	labels := map[string]string{
		constants.LabelAppName:     constants.AppName,
		constants.LabelAppInstance: cluster.Name,
	}
	if controller {
		labels[constants.LabelAppNamespace] = cluster.Namespace
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
	Scheme              *runtime.Scheme
	AgentContainerImage string
	FluentBitImage      string
	SystemNamespace     string
	ClusterManager      clustering.ClusterManager
}

//+kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters/finalizers,verbs=update
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
//+kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete

// Reconcile implements Reconciler interface.
// See https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile#Reconciler
func (r *MySQLClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	cluster := &mocov1beta1.MySQLCluster{}
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

func (r *MySQLClusterReconciler) reconcileV1(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cluster.DeletionTimestamp != nil {
		if !controllerutil.ContainsFinalizer(cluster, constants.MySQLClusterFinalizer) {
			return ctrl.Result{}, nil
		}

		log.Info("start finalizing MySQLCluster")

		r.ClusterManager.Stop(req.NamespacedName)

		if err := r.finalizeV1(ctx, req, cluster); err != nil {
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

	if err := r.reconcileV1StatefulSet(ctx, req, cluster, mycnf); err != nil {
		log.Error(err, "failed to reconcile stateful set")
		return ctrl.Result{}, err
	}

	if err := r.reconcileV1PDB(ctx, req, cluster); err != nil {
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

	r.ClusterManager.Update(ctx, client.ObjectKeyFromObject(cluster))
	return ctrl.Result{}, nil
}

func (r *MySQLClusterReconciler) reconcileV1Secret(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	secretName := cluster.ControllerSecretName()
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.SystemNamespace, Name: secretName}, secret)
	if err == nil {
		passwd, err := password.NewMySQLPasswordFromSecret(secret)
		if err != nil {
			return fmt.Errorf("failed to create password from secret %s/%s: %w", secret.Namespace, secret.Name, err)
		}
		userSecret := &corev1.Secret{}
		userSecret.Namespace = cluster.Namespace
		userSecret.Name = cluster.UserSecretName()
		result, err := ctrl.CreateOrUpdate(ctx, r.Client, userSecret, func() error {
			newSecret := passwd.ToSecret()
			userSecret.Annotations = mergeMap(userSecret.Annotations, newSecret.Annotations)
			userSecret.Labels = mergeMap(userSecret.Labels, labelSet(cluster, false))
			userSecret.Data = newSecret.Data
			return ctrl.SetControllerReference(cluster, userSecret, r.Scheme)
		})
		if err != nil {
			return err
		}
		if result != controllerutil.OperationResultNone {
			log.Info("reconciled user secret", "operation", string(result))
		}

		mycnfSecret := &corev1.Secret{}
		mycnfSecret.Namespace = cluster.Namespace
		mycnfSecret.Name = cluster.MyCnfSecretName()
		result, err = ctrl.CreateOrUpdate(ctx, r.Client, mycnfSecret, func() error {
			newSecret := passwd.ToMyCnfSecret()
			mycnfSecret.Annotations = mergeMap(mycnfSecret.Annotations, newSecret.Annotations)
			mycnfSecret.Labels = mergeMap(mycnfSecret.Labels, labelSet(cluster, false))
			mycnfSecret.Data = newSecret.Data
			return ctrl.SetControllerReference(cluster, mycnfSecret, r.Scheme)
		})
		if err != nil {
			return err
		}
		if result != controllerutil.OperationResultNone {
			log.Info("reconciled my.cnf secret", "operation", string(result))
		}

		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	passwd, err := password.NewMySQLPassword()
	if err != nil {
		return err
	}

	secret = passwd.ToSecret()
	secret.Namespace = r.SystemNamespace
	secret.Name = secretName
	secret.Labels = labelSet(cluster, true)
	if err := r.Client.Create(ctx, secret); err != nil {
		return err
	}

	userSecret := passwd.ToSecret()
	userSecret.Namespace = cluster.Namespace
	userSecret.Name = cluster.UserSecretName()
	userSecret.Labels = labelSet(cluster, false)
	if err := ctrl.SetControllerReference(cluster, userSecret, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, userSecret); err != nil {
		return err
	}

	mycnfSecret := passwd.ToMyCnfSecret()
	mycnfSecret.Namespace = cluster.Namespace
	mycnfSecret.Name = cluster.MyCnfSecretName()
	mycnfSecret.Labels = labelSet(cluster, false)
	if err := ctrl.SetControllerReference(cluster, mycnfSecret, r.Scheme); err != nil {
		return err
	}
	if err := r.Client.Create(ctx, mycnfSecret); err != nil {
		return err
	}

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1MyCnf(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) (*corev1.ConfigMap, error) {
	log := crlog.FromContext(ctx)

	var mysqldContainer *corev1.Container
	for i, c := range cluster.Spec.PodTemplate.Spec.Containers {
		if c.Name == constants.MysqldContainerName {
			mysqldContainer = &cluster.Spec.PodTemplate.Spec.Containers[i]
			break
		}
	}
	if mysqldContainer == nil {
		return nil, fmt.Errorf("MySQLD container not found")
	}

	// resources.requests.memory takes precedence over resources.limits.memory.
	var totalMem int64
	if res := mysqldContainer.Resources.Limits.Memory(); !res.IsZero() {
		totalMem = res.Value()
	}
	if res := mysqldContainer.Resources.Requests.Memory(); !res.IsZero() {
		totalMem = res.Value()
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

	cm := &corev1.ConfigMap{}
	cm.Namespace = cluster.Namespace
	cm.Name = prefix + suffix
	result, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = mergeMap(cm.Labels, labelSet(cluster, false))
		cm.Data = map[string]string{
			constants.MySQLConfName: conf,
		}
		return ctrl.SetControllerReference(cluster, cm, r.Scheme)
	})
	if err != nil {
		return nil, err
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled my.cnf configmap", "operation", string(result))
	}

	cms := &corev1.ConfigMapList{}
	if err := r.List(ctx, cms, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}
	for _, old := range cms.Items {
		if strings.HasPrefix(old.Name, prefix) && old.Name != cm.Name {
			if err := r.Delete(ctx, &old); err != nil {
				return nil, fmt.Errorf("failed to delete old my.cnf configmap %s/%s: %w", old.Namespace, old.Name, err)
			}
		}
	}

	return cm, nil
}

func (r *MySQLClusterReconciler) reconcileV1FluentBitConfigMap(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) error {
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
		cm := &corev1.ConfigMap{}
		cm.Namespace = cluster.Namespace
		cm.Name = cluster.SlowQueryLogAgentConfigMapName()
		result, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
			cm.Labels = mergeMap(cm.Labels, labelSet(cluster, false))
			confVal := fmt.Sprintf(configTmpl, filepath.Join(constants.LogDirPath, constants.MySQLSlowLogName))
			cm.Data = map[string]string{
				constants.FluentBitConfigName: confVal,
			}
			return ctrl.SetControllerReference(cluster, cm, r.Scheme)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile configmap for slow logs: %w", err)
		}
		if result != controllerutil.OperationResultNone {
			log.Info("reconciled configmap for slow logs", "operation", string(result))
		}
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

func (r *MySQLClusterReconciler) reconcileV1ServiceAccount(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	sa := &corev1.ServiceAccount{}
	sa.Namespace = cluster.Namespace
	sa.Name = cluster.PrefixedName()

	result, err := ctrl.CreateOrUpdate(ctx, r.Client, sa, func() error {
		sa.Labels = mergeMap(sa.Labels, labelSet(cluster, false))
		return ctrl.SetControllerReference(cluster, sa, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile service account: %w", err)
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled service account", "operation", string(result))
	}

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1Service(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) error {
	if err := r.reconcileV1Service1(ctx, cluster, cluster.HeadlessServiceName(), true, labelSet(cluster, false)); err != nil {
		return err
	}

	primarySelector := labelSet(cluster, false)
	primarySelector[constants.LabelMocoRole] = constants.RolePrimary
	if err := r.reconcileV1Service1(ctx, cluster, cluster.PrimaryServiceName(), false, primarySelector); err != nil {
		return err
	}

	replicaSelector := labelSet(cluster, false)
	replicaSelector[constants.LabelMocoRole] = constants.RoleReplica
	if err := r.reconcileV1Service1(ctx, cluster, cluster.ReplicaServiceName(), false, replicaSelector); err != nil {
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) reconcileV1Service1(ctx context.Context, cluster *mocov1beta1.MySQLCluster, name string, headless bool, selector map[string]string) error {
	log := crlog.FromContext(ctx)

	svc := &corev1.Service{}
	svc.Namespace = cluster.Namespace
	svc.Name = name
	var orig, updated *corev1.ServiceSpec
	result, err := ctrl.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if debugController {
			orig = svc.Spec.DeepCopy()
		}

		saSpec := &corev1.ServiceSpec{}
		tmpl := cluster.Spec.ServiceTemplate
		if tmpl != nil {
			svc.Annotations = mergeMap(svc.Annotations, tmpl.Annotations)
			svc.Labels = mergeMap(svc.Labels, tmpl.Labels)
			svc.Labels = mergeMap(svc.Labels, labelSet(cluster, false))

			if tmpl.Spec != nil {
				tmpl.Spec.DeepCopyInto(saSpec)
			}
		} else {
			svc.Labels = mergeMap(svc.Labels, labelSet(cluster, false))
		}

		if headless {
			saSpec.ClusterIP = corev1.ClusterIPNone
			saSpec.Type = corev1.ServiceTypeClusterIP
			saSpec.PublishNotReadyAddresses = true
		} else {
			saSpec.ClusterIP = svc.Spec.ClusterIP
			if len(saSpec.Type) == 0 {
				saSpec.Type = svc.Spec.Type
			}
		}
		if len(saSpec.SessionAffinity) == 0 {
			saSpec.SessionAffinity = svc.Spec.SessionAffinity
		}
		if len(saSpec.ExternalTrafficPolicy) == 0 {
			saSpec.ExternalTrafficPolicy = svc.Spec.ExternalTrafficPolicy
		}
		saSpec.Selector = selector

		var mysqlNodePort, mysqlXNodePort int32
		for _, p := range svc.Spec.Ports {
			switch p.Name {
			case constants.MySQLPortName:
				mysqlNodePort = p.NodePort
			case constants.MySQLXPortName:
				mysqlXNodePort = p.NodePort
			}
		}
		saSpec.Ports = []corev1.ServicePort{
			{
				Name:       constants.MySQLPortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       constants.MySQLPort,
				TargetPort: intstr.FromString(constants.MySQLPortName),
				NodePort:   mysqlNodePort,
			},
			{
				Name:       constants.MySQLXPortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       constants.MySQLXPort,
				TargetPort: intstr.FromString(constants.MySQLXPortName),
				NodePort:   mysqlXNodePort,
			},
		}

		saSpec.DeepCopyInto(&svc.Spec)

		if debugController {
			updated = svc.Spec.DeepCopy()
		}

		return ctrl.SetControllerReference(cluster, svc, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile %s service: %w", name, err)
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled service", "headless", headless, "operation", string(result))
	}
	if result == controllerutil.OperationResultUpdated && debugController {
		fmt.Println(cmp.Diff(orig, updated))
	}

	return nil
}

func (r *MySQLClusterReconciler) reconcileV1StatefulSet(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster, mycnf *corev1.ConfigMap) error {
	log := crlog.FromContext(ctx)

	sts := &appsv1.StatefulSet{}
	sts.Namespace = cluster.Namespace
	sts.Name = cluster.PrefixedName()

	var orig, updated *appsv1.StatefulSetSpec
	result, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		if debugController {
			orig = sts.Spec.DeepCopy()
		}

		sts.Labels = mergeMap(sts.Labels, labelSet(cluster, false))

		sts.Spec.Replicas = pointer.Int32(cluster.Spec.Replicas)
		sts.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: labelSet(cluster, false),
		}
		sts.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
		sts.Spec.ServiceName = cluster.HeadlessServiceName()

		sts.Spec.VolumeClaimTemplates = make([]corev1.PersistentVolumeClaim, len(cluster.Spec.VolumeClaimTemplates))
		for i, v := range cluster.Spec.VolumeClaimTemplates {
			pvc := v.ToCoreV1()
			pvc.Namespace = cluster.Namespace
			if err := ctrl.SetControllerReference(cluster, &pvc, r.Scheme); err != nil {
				panic(err)
			}
			pvc.Namespace = ""
			sts.Spec.VolumeClaimTemplates[i] = pvc
		}

		sts.Spec.Template.Annotations = mergeMap(sts.Spec.Template.Annotations, cluster.Spec.PodTemplate.Annotations)
		sts.Spec.Template.Labels = mergeMap(sts.Spec.Template.Labels, cluster.Spec.PodTemplate.Labels)
		sts.Spec.Template.Labels = mergeMap(sts.Spec.Template.Labels, labelSet(cluster, false))

		podSpec := cluster.Spec.PodTemplate.Spec.DeepCopy()
		podSpec.ServiceAccountName = cluster.PrefixedName()

		podSpec.DeprecatedServiceAccount = sts.Spec.Template.Spec.DeprecatedServiceAccount
		if len(podSpec.RestartPolicy) == 0 {
			podSpec.RestartPolicy = sts.Spec.Template.Spec.RestartPolicy
		}
		if podSpec.TerminationGracePeriodSeconds == nil {
			podSpec.TerminationGracePeriodSeconds = pointer.Int64(defaultTerminationGracePeriodSeconds)
		}
		if len(podSpec.DNSPolicy) == 0 {
			podSpec.DNSPolicy = sts.Spec.Template.Spec.DNSPolicy
		}
		if podSpec.SecurityContext == nil {
			podSpec.SecurityContext = sts.Spec.Template.Spec.SecurityContext
		}
		if len(podSpec.SchedulerName) == 0 {
			podSpec.SchedulerName = sts.Spec.Template.Spec.SchedulerName
		}

		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{
				Name: constants.TmpVolumeName, VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			corev1.Volume{
				Name: constants.RunVolumeName, VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			corev1.Volume{
				Name: constants.VarLogVolumeName, VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			corev1.Volume{
				Name: constants.MySQLInitConfVolumeName, VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			corev1.Volume{
				Name: constants.MOCOBinVolumeName, VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			corev1.Volume{
				Name: constants.MySQLConfVolumeName, VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: mycnf.Name,
						},
						DefaultMode: pointer.Int32(0644),
					},
				}},
			corev1.Volume{
				Name: constants.MySQLConfSecretVolumeName, VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  cluster.MyCnfSecretName(),
						DefaultMode: pointer.Int32(0644),
					},
				}},
		)
		if !cluster.Spec.DisableSlowQueryLogContainer {
			podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
				Name: constants.SlowQueryLogAgentConfigVolumeName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cluster.SlowQueryLogAgentConfigMapName(),
						},
						DefaultMode: pointer.Int32(0644),
					},
				},
			})
		}

		containers := make([]corev1.Container, 0, 4)
		mysqldContainer, err := r.makeV1MySQLDContainer(podSpec.Containers, sts.Spec.Template.Spec.Containers)
		if err != nil {
			return err
		}
		containers = append(containers, mysqldContainer)
		containers = append(containers, r.makeV1AgentContainer(cluster, sts.Spec.Template.Spec.Containers))
		if !cluster.Spec.DisableSlowQueryLogContainer {
			force := cluster.Status.ReconcileInfo.Generation != cluster.Generation
			containers = append(containers, r.makeV1SlowQueryLogContainer(sts, force))
		}
		containers = append(containers, r.makeV1OptionalContainers(cluster, sts.Spec.Template.Spec.Containers)...)
		podSpec.Containers = containers
		podSpec.InitContainers = r.makeV1InitContainer(cluster, mysqldContainer.Image, sts.Spec.Template.Spec.InitContainers)

		podSpec.DeepCopyInto(&sts.Spec.Template.Spec)

		if debugController {
			updated = sts.Spec.DeepCopy()
		}
		return ctrl.SetControllerReference(cluster, sts, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to reconcile stateful set")
		return err
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled stateful set", "operation", string(result))
	}
	if result == controllerutil.OperationResultUpdated && debugController {
		fmt.Println(cmp.Diff(orig, updated))
	}
	return nil
}

func (r *MySQLClusterReconciler) reconcileV1PDB(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) error {
	log := crlog.FromContext(ctx)

	pdb := &policyv1beta1.PodDisruptionBudget{}
	pdb.Namespace = cluster.Namespace
	pdb.Name = cluster.PrefixedName()
	if cluster.Spec.Replicas < 3 {
		err := r.Delete(ctx, pdb)
		if err == nil {
			log.Info("removed pod disruption budget")
		}
		return client.IgnoreNotFound(err)
	}

	result, err := ctrl.CreateOrUpdate(ctx, r.Client, pdb, func() error {
		pdb.Labels = mergeMap(pdb.Labels, labelSet(cluster, false))
		maxUnavailable := intstr.FromInt(int(cluster.Spec.Replicas / 2))
		pdb.Spec.MaxUnavailable = &maxUnavailable
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: labelSet(cluster, false),
		}
		return ctrl.SetControllerReference(cluster, pdb, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to reconcile pod disruption budget")
		return err
	}
	if result != controllerutil.OperationResultNone {
		log.Info("reconciled pod disruption budget", "operation", string(result))
	}

	return nil
}

func (r *MySQLClusterReconciler) finalizeV1(ctx context.Context, req ctrl.Request, cluster *mocov1beta1.MySQLCluster) error {
	secretName := cluster.ControllerSecretName()
	secret := &corev1.Secret{}
	secret.SetNamespace(r.SystemNamespace)
	secret.SetName(secretName)
	return r.Delete(ctx, secret)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1beta1.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1beta1.PodDisruptionBudget{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: 8},
		).
		Complete(r)
}
