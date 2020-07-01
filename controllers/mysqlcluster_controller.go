package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

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
	myName          = "moco"
	appNameKey      = "app.kubernetes.io/name"
	appManagedByKey = "app.kubernetes.io/managed-by"

	mysqldContainerName = "mysqld"
	mysqlPort           = 3306
	mysqlAdminPort      = 33062
	mysqlxPort          = 33060

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

	rootPasswordSecretPrefix = "root-password-"
	serviceAccountPrefix     = "mysqld-sa-"
)

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Log                    logr.Logger
	Scheme                 *runtime.Scheme
	ConfInitContainerImage string
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

	err = r.createPasswordSecretForUser(ctx, cluster, operatorPass, replicatorPass, donorPass)
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

func (r *MySQLClusterReconciler) createPasswordSecretForUser(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, operatorPass, replicatorPass, donorPass []byte) error {
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
	secretName := rootPasswordSecretPrefix + uniqueName(cluster)
	secret := &corev1.Secret{}
	secret.SetNamespace(cluster.Namespace)
	secret.SetName(secretName)

	setLabels(&secret.ObjectMeta)

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
	cm.SetName(uniqueName(cluster))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		setLabels(&cm.ObjectMeta)
		gen := mysqlConfGenerator{
			log: log,
		}
		gen.mergeSection("mysqld", defaultMycnf, false)

		// Set innodb_buffer_pool_size if resources.requests.memory is specified
		for _, orig := range cluster.Spec.PodTemplate.Spec.Containers {
			if orig.Name != mysqldContainerName {
				continue
			}
			if mem := orig.Resources.Requests.Memory(); mem != nil && mem.Value() != 0 {
				bufferSize := int64(float64(mem.Value()) * 0.7)
				gen.mergeSection("mysqld", map[string]string{"innodb_buffer_pool_size": string(bufferSize)}, false)
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
		return err
	}

	if op != controllerutil.OperationResultNone {
		log.Info("reconcile ConfigMap successfully", "op", op)
	}
	return nil
}

func (r *MySQLClusterReconciler) createOrUpdateHeadlessService(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	headless := &corev1.Service{}
	headless.SetNamespace(cluster.Namespace)
	headless.SetName(uniqueName(cluster))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, headless, func() error {
		setLabels(&headless.ObjectMeta)
		headless.Spec.ClusterIP = corev1.ClusterIPNone
		headless.Spec.Selector = map[string]string{
			appNameKey:      uniqueName(cluster),
			appManagedByKey: myName,
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
	if cluster.Spec.PodTemplate.Spec.ServiceAccountName != "" {
		return nil
	}

	saName := serviceAccountPrefix + uniqueName(cluster)
	sa := &corev1.ServiceAccount{}
	sa.SetNamespace(cluster.Namespace)
	sa.SetName(saName)

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sa, func() error {
		setLabels(&sa.ObjectMeta)
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
	sts.SetName(uniqueName(cluster))

	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		setLabels(&sts.ObjectMeta)
		sts.Spec.Replicas = &cluster.Spec.Replicas
		sts.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
		sts.Spec.ServiceName = uniqueName(cluster)
		sts.Spec.Selector = &metav1.LabelSelector{}
		sts.Spec.Selector.MatchLabels = map[string]string{
			appNameKey:      uniqueName(cluster),
			appManagedByKey: myName,
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
	if v, ok := newTemplate.Labels[appNameKey]; ok && v != myName {
		log.Info("overwriting Pod template's label", "label", appNameKey)
	}
	newTemplate.Labels[appNameKey] = uniqueName(cluster)
	newTemplate.Labels[appManagedByKey] = myName

	if newTemplate.Spec.ServiceAccountName != "" {
		log.Info("overwriting Pod template's serviceAccountName", "ServiceAccountName", newTemplate.Spec.ServiceAccountName)
	}
	newTemplate.Spec.ServiceAccountName = serviceAccountPrefix + uniqueName(cluster)

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
						Name: uniqueName(cluster),
					},
				},
			},
		},
	)

	// find "mysqld" container and update it
	var mysqldContainer *corev1.Container
	newTemplate.Spec.Containers = make([]corev1.Container, len(template.Spec.Containers))
	for i, orig := range template.Spec.Containers {
		if orig.Name != mysqldContainerName {
			newTemplate.Spec.Containers[i] = orig
			continue
		}

		c := orig.DeepCopy()
		c.Args = []string{"--defaults-file=" + filepath.Join(moco.MySQLConfPath, moco.MySQLConfName)}
		c.Ports = []corev1.ContainerPort{
			{
				ContainerPort: mysqlPort, Protocol: corev1.ProtocolTCP},
			{
				ContainerPort: mysqlxPort, Protocol: corev1.ProtocolTCP},
			{
				ContainerPort: mysqlAdminPort, Protocol: corev1.ProtocolTCP},
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
		return corev1.PodTemplateSpec{}, fmt.Errorf("container named %q not found in podTemplate", mysqldContainerName)
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
	secretName := rootPasswordSecretPrefix + uniqueName(cluster)
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

func uniqueName(cluster *mocov1alpha1.MySQLCluster) string {
	return fmt.Sprintf("%s-%s", cluster.GetName(), cluster.GetUID())
}

func setLabels(om *metav1.ObjectMeta) {
	om.Labels = map[string]string{
		appNameKey:      om.Name,
		appManagedByKey: myName,
	}
}
