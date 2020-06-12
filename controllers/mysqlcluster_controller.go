package controllers

import (
	"context"
	"crypto/rand"
	"path/filepath"

	"github.com/cybozu-go/myso"
	mysov1alpha1 "github.com/cybozu-go/myso/api/v1alpha1"
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
	appName         = "myso"
	appNameKey      = "app.kubernetes.io/name"
	instanceNameKey = "app.kubernetes.io/instance"

	containerName               = "mysqld"
	entrypointInitContainerName = "myso-init"
	configInitContainerName     = "myso-config"

	mysqlDataVolumeName = "mysql-data"
	mysqlConfVolumeName = "mysql-conf"
	varRunVolumeName    = "var-run"
	varLogVolumeName    = "var-log"
	tmpVolumeName       = "tmp"

	//
	passwordBytes = 32
)

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Log                      logr.Logger
	Scheme                   *runtime.Scheme
	ConfigInitContainerImage string
}

// +kubebuilder:rbac:groups=myso.cybozu.com,resources=mysqlclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=myso.cybozu.com,resources=mysqlclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status,verbs=get
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services/status,verbs=get

// Reconcile reconciles MySQLCluster.
func (r *MySQLClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("mysqlcluster", req.NamespacedName)

	cluster := &mysov1alpha1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		log.Error(err, "unable to fetch MySQLCluster", "name", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := r.createSecretIfNotExist(ctx, log, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.createOrUpdateHeadlessService(ctx, log, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.createOrUpdateStatefulSet(ctx, log, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller for reconciliation.
func (r *MySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mysov1alpha1.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

func (r *MySQLClusterReconciler) createSecretIfNotExist(ctx context.Context, log logr.Logger, cluster *mysov1alpha1.MySQLCluster) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.RootPasswordSecretName}, secret)
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		log.Error(err, "unable to get Secret")
		return err
	}

	err = r.createRootPasswordSecret(ctx, cluster)
	if err != nil {
		log.Error(err, "unable to create Secret")
		return err
	}
	return nil
}

func (r *MySQLClusterReconciler) createRootPasswordSecret(ctx context.Context, cluster *mysov1alpha1.MySQLCluster) error {
	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(cluster.Spec.RootPasswordSecretName)

	secret.Labels = map[string]string{
		appNameKey:      appName,
		instanceNameKey: cluster.Name,
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

	secret.Data = map[string][]byte{
		myso.RootPasswordKey:        rootPass,
		myso.OperatorPasswordKey:    operatorPass,
		myso.ReplicationPasswordKey: replicatorPass,
		myso.DonorPasswordKey:       donorPass,
	}

	err = ctrl.SetControllerReference(cluster, secret, r.Scheme)
	if err != nil {
		return err
	}

	return r.Client.Create(ctx, secret)
}

func generateRandomBytes(n int) ([]byte, error) {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-"
	bytes := make([]byte, n)
	_, err := rand.Read(bytes)
	// Note that err == nil only if we read len(bytes) bytes.
	if err != nil {
		return nil, err
	}
	for i, b := range bytes {
		bytes[i] = letters[b%byte(len(letters))]
	}
	return bytes, nil
}

func (r *MySQLClusterReconciler) createOrUpdateStatefulSet(ctx context.Context, log logr.Logger, cluster *mysov1alpha1.MySQLCluster) error {
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(cluster.Namespace)
	sts.SetName(cluster.Name)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Labels = map[string]string{
			appNameKey:      appName,
			instanceNameKey: cluster.Name,
		}
		sts.Spec.Replicas = &cluster.Spec.Replicas
		sts.Spec.ServiceName = cluster.Name
		sts.Spec.Selector = &metav1.LabelSelector{}
		sts.Spec.Selector.MatchLabels = map[string]string{
			appNameKey:      appName,
			instanceNameKey: cluster.Name,
		}
		sts.Spec.Template = r.makePodTemplate(log, cluster)
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

func (r *MySQLClusterReconciler) createOrUpdateHeadlessService(ctx context.Context, log logr.Logger, cluster *mysov1alpha1.MySQLCluster) error {
	headless := &corev1.Service{}
	headless.SetNamespace(cluster.Namespace)
	headless.SetName(cluster.Name)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, headless, func() error {
		headless.Labels = map[string]string{
			appNameKey:      appName,
			instanceNameKey: cluster.Name,
		}
		headless.Spec.ClusterIP = corev1.ClusterIPNone
		headless.Spec.Selector = map[string]string{
			appNameKey:      appName,
			instanceNameKey: cluster.Name,
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

func (r *MySQLClusterReconciler) makePodTemplate(log logr.Logger, cluster *mysov1alpha1.MySQLCluster) corev1.PodTemplateSpec {
	template := cluster.Spec.PodTemplate
	newTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      template.Labels,
			Annotations: template.Annotations,
		},
		Spec: template.Spec,
	}

	// add labels to describe application
	if newTemplate.Labels == nil {
		newTemplate.Labels = make(map[string]string)
	}
	if v, ok := newTemplate.Labels[appNameKey]; ok && v != appName {
		log.Info("overwriting Pod template's label", "label", appNameKey)
	}
	newTemplate.Labels[appNameKey] = appName
	if v, ok := newTemplate.Labels[instanceNameKey]; ok && v != cluster.Name {
		log.Info("overwriting Pod template's label", "label", instanceNameKey)
	}
	newTemplate.Labels[instanceNameKey] = cluster.Name

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
	)

	// find "mysqld" container and update it
	var mysqldContainer *corev1.Container
	for i := range newTemplate.Spec.Containers {
		c := &newTemplate.Spec.Containers[i]
		if c.Name != containerName {
			continue
		}

		mysqldContainer = c

		c.Args = []string{"--defaults-file=" + filepath.Join(myso.MySQLConfPath, myso.MySQLConfName)}
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name: myso.RootPasswordEnvName,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cluster.Spec.RootPasswordSecretName,
						},
						Key: myso.RootPasswordKey,
					},
				},
			},
			corev1.EnvVar{
				Name: myso.PodIPEnvName,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
		)
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				MountPath: myso.MySQLDataPath,
				Name:      mysqlDataVolumeName,
			},
			corev1.VolumeMount{
				MountPath: myso.MySQLConfPath,
				Name:      mysqlConfVolumeName,
			},
			corev1.VolumeMount{
				MountPath: myso.VarRunPath,
				Name:      varRunVolumeName,
			},
			corev1.VolumeMount{
				MountPath: myso.VarLogPath,
				Name:      varLogVolumeName,
			},
			corev1.VolumeMount{
				MountPath: myso.TmpPath,
				Name:      tmpVolumeName,
			},
		)
	}

	// create init containers and append them to Pod
	newTemplate.Spec.InitContainers = append(newTemplate.Spec.InitContainers,
		r.makeConfigInitContainer(log, cluster),
		r.makeEntrypointInitContainer(log, cluster, mysqldContainer.Image),
	)

	return newTemplate
}

func (r *MySQLClusterReconciler) makeConfigInitContainer(log logr.Logger, cluster *mysov1alpha1.MySQLCluster) corev1.Container {
	c := corev1.Container{}
	c.Name = configInitContainerName

	c.Image = r.ConfigInitContainerImage

	c.Command = []string{"/moco-conf-gen"}
	c.Env = append(c.Env,
		corev1.EnvVar{
			Name: myso.PodIPEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		corev1.EnvVar{
			Name: myso.PodNameEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
	)
	c.VolumeMounts = append(c.VolumeMounts,
		corev1.VolumeMount{
			MountPath: myso.MySQLConfPath,
			Name:      mysqlConfVolumeName,
		},
		corev1.VolumeMount{
			MountPath: myso.VarRunPath,
			Name:      varRunVolumeName,
		},
		corev1.VolumeMount{
			MountPath: myso.VarLogPath,
			Name:      varLogVolumeName,
		},
		corev1.VolumeMount{
			MountPath: myso.TmpPath,
			Name:      tmpVolumeName,
		},
	)

	return c
}

func (r *MySQLClusterReconciler) makeEntrypointInitContainer(log logr.Logger, cluster *mysov1alpha1.MySQLCluster, mysqldContainerImage string) corev1.Container {
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
			Name: myso.PodIPEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
	)
	c.VolumeMounts = append(c.VolumeMounts,
		corev1.VolumeMount{
			MountPath: myso.MySQLDataPath,
			Name:      mysqlDataVolumeName,
		},
		corev1.VolumeMount{
			MountPath: myso.MySQLConfPath,
			Name:      mysqlConfVolumeName,
		},
		corev1.VolumeMount{
			MountPath: myso.VarRunPath,
			Name:      varRunVolumeName,
		},
		corev1.VolumeMount{
			MountPath: myso.VarLogPath,
			Name:      varLogVolumeName,
		},
		corev1.VolumeMount{
			MountPath: myso.TmpPath,
			Name:      tmpVolumeName,
		},
	)

	return c
}

func (r *MySQLClusterReconciler) makeDataVolumeClaimTemplate(cluster *mysov1alpha1.MySQLCluster) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: mysqlDataVolumeName,
		},
		Spec: cluster.Spec.DataVolumeClaimTemplateSpec,
	}
}

func (r *MySQLClusterReconciler) makeVolumeClaimTemplates(cluster *mysov1alpha1.MySQLCluster) []corev1.PersistentVolumeClaim {
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
