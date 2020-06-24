package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	appName    = "moco"
	appNameKey = "app.kubernetes.io/name"

	containerName               = "mysqld"
	entrypointInitContainerName = "moco-init"
	confInitContainerName       = "moco-conf-gen"

	mysqlDataVolumeName         = "mysql-data"
	mysqlConfVolumeName         = "mysql-conf"
	varRunVolumeName            = "var-run"
	varLogVolumeName            = "var-log"
	tmpVolumeName               = "tmp"
	mysqlConfTemplateVolumeName = "mysql-conf-template"

	passwordBytes = 16

	defaultTerminationGracePeriodSeconds = 300

	mysqlClusterFinalizer = "moco.cybozu.com/mysqlcluster"
)

var (
	mycnfTemplate = map[string]string{
		"server-id":     "{{ .server_id }}",
		"admin-address": "{{ .admin_address }}",
	}
)

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Log                    logr.Logger
	Scheme                 *runtime.Scheme
	ConfInitContainerImage string
	DefaultConfConfigMap   string
	ConstantConfConfigMap  string
}

// +kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=moco.cybozu.com,resources=mysqlclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status,verbs=get
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services/status,verbs=get
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts/status,verbs=get
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps/status,verbs=get

// Reconcile reconciles MySQLCluster.
func (r *MySQLClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("mysqlcluster", req.NamespacedName)

	cluster := &mocov1alpha1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		log.Error(err, "unable to fetch MySQLCluster", "name", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cluster.DeletionTimestamp == nil {
		if !containsString(cluster.Finalizers, mysqlClusterFinalizer) {
			cluster2 := cluster.DeepCopy()
			cluster2.Finalizers = append(cluster2.Finalizers, mysqlClusterFinalizer)
			patch := client.MergeFrom(cluster)
			if err := r.Patch(ctx, cluster2, patch); err != nil {
				log.Error(err, "failed to add finalizer", "name", cluster.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		err := r.createSecretIfNotExist(ctx, log, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.createOrUpdateConfigMap(ctx, log, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.createOrUpdateHeadlessService(ctx, log, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.createOrUpdateRBAC(ctx, log, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.createOrUpdateStatefulSet(ctx, log, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// finalization
	if !containsString(cluster.Finalizers, mysqlClusterFinalizer) {
		// Our finalizer has finished, so the reconciler can do nothing.
		return ctrl.Result{}, nil
	}

	log.Info("start finalizing MySQLCluster", "name", cluster.Name)
	err := r.removePasswordSecretForController(ctx, log, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	cluster2 := cluster.DeepCopy()
	cluster2.Finalizers = removeString(cluster2.Finalizers, mysqlClusterFinalizer)
	patch := client.MergeFrom(cluster)
	if err := r.Patch(ctx, cluster2, patch); err != nil {
		log.Error(err, "failed to remove finalizer", "name", cluster.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller for reconciliation.
func (r *MySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1alpha1.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

func (r *MySQLClusterReconciler) createSecretIfNotExist(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	secret := &corev1.Secret{}
	myNS, mySecretName := r.getSecretNameForController(cluster)
	err := r.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		log.Error(err, "unable to get Secret")
		return err
	}

	rootPass, err := generateRandomBytes(passwordBytes)
	if err != nil {
		return err
	}
	operatorPass, err := generateRandomBytes(passwordBytes)
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

	err = r.createPasswordSecretForUser(ctx, cluster, rootPass, operatorPass, replicatorPass, donorPass)
	if err != nil {
		log.Error(err, "unable to create Secret for user")
		return err
	}

	// Secret for controller must be created lastly, because its existence is checked at the beginning of the process
	err = r.createPasswordSecretForController(ctx, myNS, mySecretName, operatorPass, replicatorPass, donorPass)
	if err != nil {
		log.Error(err, "unable to create Secret for Controller")
		return err
	}

	return nil
}

func (r *MySQLClusterReconciler) createPasswordSecretForUser(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, rootPass, operatorPass, replicatorPass, donorPass []byte) error {
	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(cluster.Spec.RootPasswordSecretName)

	secret.Labels = map[string]string{
		appNameKey: fmt.Sprintf("%s-%s", appName, cluster.Name),
	}

	secret.Data = map[string][]byte{
		moco.RootPasswordKey:        rootPass,
		moco.OperatorPasswordKey:    operatorPass,
		moco.ReplicationPasswordKey: replicatorPass,
		moco.DonorPasswordKey:       donorPass,
	}

	err := ctrl.SetControllerReference(cluster, secret, r.Scheme)
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
		moco.DonorPasswordKey:       donorPass,
	}

	return r.Client.Create(ctx, secret)
}

func (r *MySQLClusterReconciler) removePasswordSecretForController(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	secret := &corev1.Secret{}
	myNS, mySecretName := r.getSecretNameForController(cluster)
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

func (r *MySQLClusterReconciler) getSecretNameForController(cluster *mocov1alpha1.MySQLCluster) (string, string) {
	myNS := os.Getenv("POD_NAMESPACE")
	mySecretName := cluster.Namespace + "." + cluster.Name // TODO: clarify assumptions for length and charset
	return myNS, mySecretName
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

func (r *MySQLClusterReconciler) createOrUpdateConfigMap(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	cm := &corev1.ConfigMap{}
	cm.SetNamespace(cluster.Namespace)
	cm.SetName(cluster.Name)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = map[string]string{
			appNameKey: fmt.Sprintf("%s-%s", appName, cluster.Name),
		}

		conf := make(map[string]string)
		if r.DefaultConfConfigMap != "" {
			err := r.updateConfigMap(ctx, client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: r.DefaultConfConfigMap}, conf)
			if err != nil {
				return err
			}
		}
		if cluster.Spec.MySQLConfigMapName != nil {
			err := r.updateConfigMap(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: *cluster.Spec.MySQLConfigMapName}, conf)
			if err != nil {
				return err
			}
		}
		if r.ConstantConfConfigMap != "" {
			err := r.updateConfigMap(ctx, client.ObjectKey{Namespace: os.Getenv("POD_NAMESPACE"), Name: r.ConstantConfConfigMap}, conf)
			if err != nil {
				return err
			}
		}

		cm.Data = make(map[string]string)
		cm.Data["my.cnf"] = r.convertToMycnf(conf)

		return ctrl.SetControllerReference(cluster, cm, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update ConfigMap")
		return err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile ConfigMap successfully", "op", op)
	}
	return nil
}

func (r *MySQLClusterReconciler) updateConfigMap(ctx context.Context, objectKey client.ObjectKey, conf map[string]string) error {
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, objectKey, cm)
	if err != nil {
		return err
	}
	for k, v := range cm.Data {
		conf[k] = v
	}
	return nil
}

func (r *MySQLClusterReconciler) convertToMycnf(conf map[string]string) string {
	for k, v := range mycnfTemplate {
		conf[k] = v
	}

	// sort keys to generate reproducible my.cnf
	confKeys := make([]string, 0, len(conf))
	for k := range conf {
		confKeys = append(confKeys, k)
	}
	sort.Strings(confKeys)

	b := new(strings.Builder)
	b.WriteString("[mysqld]\n")
	for _, k := range confKeys {
		fmt.Fprintf(b, "%s = %s\n", k, conf[k])
	}
	return b.String()
}

func (r *MySQLClusterReconciler) createOrUpdateHeadlessService(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	headless := &corev1.Service{}
	headless.SetNamespace(cluster.Namespace)
	headless.SetName(cluster.Name)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, headless, func() error {
		headless.Labels = map[string]string{
			appNameKey: fmt.Sprintf("%s-%s", appName, cluster.Name),
		}
		headless.Spec.ClusterIP = corev1.ClusterIPNone
		headless.Spec.Selector = map[string]string{
			appNameKey: fmt.Sprintf("%s-%s", appName, cluster.Name),
		}
		return ctrl.SetControllerReference(cluster, headless, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update headless Service")
		return err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile headless Service successfully", "op", op)
	}
	return nil
}

func (r *MySQLClusterReconciler) createOrUpdateRBAC(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	sa := &corev1.ServiceAccount{}
	sa.SetNamespace(cluster.Namespace)
	sa.SetName(cluster.Name)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return ctrl.SetControllerReference(cluster, sa, r.Scheme)
	})

	if err != nil {
		log.Error(err, "unable to create-or-update ServiceAccount")
		return err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile ServiceAccount successfully", "op", op)
	}
	return nil
}

func (r *MySQLClusterReconciler) createOrUpdateStatefulSet(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(cluster.Namespace)
	sts.SetName(cluster.Name)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Labels = map[string]string{
			appNameKey: fmt.Sprintf("%s-%s", appName, cluster.Name),
		}
		sts.Spec.Replicas = &cluster.Spec.Replicas
		sts.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
		sts.Spec.ServiceName = cluster.Name
		sts.Spec.Selector = &metav1.LabelSelector{}
		sts.Spec.Selector.MatchLabels = map[string]string{
			appNameKey: fmt.Sprintf("%s-%s", appName, cluster.Name),
		}
		template, err := r.makePodTemplate(log, cluster)
		if err != nil {
			return err
		}
		sts.Spec.Template = template
		sts.Spec.VolumeClaimTemplates = append(
			r.makeVolumeClaimTemplates(cluster),
			r.makeDataVolumeClaimTemplate(cluster),
		)
		return ctrl.SetControllerReference(cluster, sts, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update StatefulSet")
		return err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile StatefulSet successfully", "op", op)
	}
	return nil
}

func (r *MySQLClusterReconciler) makePodTemplate(log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (corev1.PodTemplateSpec, error) {
	template := cluster.Spec.PodTemplate
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
	if v, ok := newTemplate.Labels[appNameKey]; ok && v != appName {
		log.Info("overwriting Pod template's label", "label", appNameKey)
	}
	newTemplate.Labels[appNameKey] = fmt.Sprintf("%s-%s", appName, cluster.Name)

	if newTemplate.Spec.ServiceAccountName != "" {
		log.Info("overwriting Pod template's serviceAccountName", "ServiceAccountName", newTemplate.Spec.ServiceAccountName)
	}
	newTemplate.Spec.ServiceAccountName = cluster.Name

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
						Name: cluster.Name,
					},
				},
			},
		},
	)

	// find "mysqld" container and update it
	var mysqldContainer *corev1.Container
	newTemplate.Spec.Containers = make([]corev1.Container, len(template.Spec.Containers))
	for i, orig := range template.Spec.Containers {
		if orig.Name != containerName {
			newTemplate.Spec.Containers[i] = orig
			continue
		}

		c := orig.DeepCopy()
		c.Args = []string{"--defaults-file=" + filepath.Join(moco.MySQLConfPath, moco.MySQLConfName)}
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
		return corev1.PodTemplateSpec{}, fmt.Errorf("container named %q not found in podTemplate", containerName)
	}

	// create init containers and append them to Pod
	newTemplate.Spec.InitContainers = append(newTemplate.Spec.InitContainers,
		r.makeConfInitContainer(log, cluster),
		r.makeEntrypointInitContainer(log, cluster, mysqldContainer.Image),
	)

	return newTemplate, nil
}

func (r *MySQLClusterReconciler) makeConfInitContainer(log logr.Logger, cluster *mocov1alpha1.MySQLCluster) corev1.Container {
	c := corev1.Container{}
	c.Name = confInitContainerName

	c.Image = r.ConfInitContainerImage

	c.Command = []string{"/moco-conf-gen"}
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
	c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: cluster.Spec.RootPasswordSecretName,
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

func (r *MySQLClusterReconciler) makeVolumeClaimTemplates(cluster *mocov1alpha1.MySQLCluster) []corev1.PersistentVolumeClaim {
	templates := cluster.Spec.VolumeClaimTemplates
	newTemplates := make([]corev1.PersistentVolumeClaim, len(templates))

	for i, template := range templates {
		newTemplates[i] = corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:        template.Name,
				Labels:      template.Labels,
				Annotations: template.Annotations,
			},
			Spec: template.Spec,
		}
	}

	return newTemplates
}

func (r *MySQLClusterReconciler) makeDataVolumeClaimTemplate(cluster *mocov1alpha1.MySQLCluster) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: mysqlDataVolumeName,
		},
		Spec: cluster.Spec.DataVolumeClaimTemplateSpec,
	}
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}
