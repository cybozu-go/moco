/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"strconv"

	mysov1alpha1 "github.com/cybozu-go/myso/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	appNameKey      = "app.kubernetes.io/name"
	appName         = "myso"
	instanceNameKey = "app.kubernetes.io/instance"
)

// MySQLClusterReconciler reconciles a MySQLCluster object
type MySQLClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=myso.cybozu.com,resources=mysqlclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=myso.cybozu.com,resources=mysqlclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services/status,verbs=get;update;patch

// Reconcile reconciles MySQLCluster.
func (r *MySQLClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("mysqlcluster", req.NamespacedName)

	cluster := &mysov1alpha1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		log.Error(err, "unable to fetch MySQLCluster")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Create root password secret if does not exist
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: cluster.Spec.RootPasswordSecretName}, secret)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "unable to get Secret")
			return ctrl.Result{}, err
		}
		err = r.createRootPasswordSecret(ctx, cluster)
		if err != nil {
			log.Error(err, "unable to create cluster")
			return ctrl.Result{}, err
		}
	}

	// CreateOrUpdate MySQL StatefulSet
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(req.Namespace)
	sts.SetName(req.Name)
	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Labels = map[string]string{
			appNameKey:      appName,
			instanceNameKey: cluster.Name,
		}
		sts.Spec.Replicas = cluster.Spec.Replicas
		sts.Spec.Selector = &metav1.LabelSelector{}
		sts.Spec.Selector.MatchLabels = map[string]string{
			appNameKey:      appName,
			instanceNameKey: cluster.Name,
		}
		sts.Spec.Template = r.getPodTemplate(cluster.Spec.PodTemplate, cluster)
		return ctrl.SetControllerReference(cluster, sts, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update StatefulSet")
		return ctrl.Result{}, err
	}
	log.Info("reconcile successfully", op)

	// CreateOrUpdate headless Service corresponding to StatefulSet
	headless := &corev1.Service{}
	headless.SetNamespace(req.Namespace)
	headless.SetName(req.Name)
	op, err = ctrl.CreateOrUpdate(ctx, r.Client, headless, func() error {
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
		return ctrl.Result{}, err
	}
	log.Info("reconcile successfully", op)

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

func (r *MySQLClusterReconciler) getPodTemplate(template corev1.PodTemplateSpec, cluster *mysov1alpha1.MySQLCluster) corev1.PodTemplateSpec {
	log := r.Log.WithValues("mysqlcluster", types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace})

	if template.Labels == nil {
		template.Labels = make(map[string]string)
	}
	if v, ok := template.Labels[appNameKey]; ok && v != appName {
		log.Info("overwriting Pod template's label", "label", appNameKey)
	}
	template.Labels[appNameKey] = appName
	if v, ok := template.Labels[instanceNameKey]; ok && v != cluster.Name {
		log.Info("overwriting Pod template's label", "label", instanceNameKey)
	}
	template.Labels[instanceNameKey] = cluster.Name

	c := corev1.Container{}
	c.Name = r.uniqueInitContainerName(template)

	//TBD
	c.Image = "quay.io/cybozu/mysql:latest"

	c.Command = []string{
		//TBD: just a example
		"entrypoint",
		"--server_id=$(SERVER_ID)",
		"--user=mysql",
		"--relay-log=$(SERVER_ID)-relay-bin",
		"--report-host=$(SERVER_ID).$(STS_NAME)",
	}
	c.EnvFrom = []corev1.EnvFromSource{{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Spec.RootPasswordSecretName},
		}},
	}

	template.Spec.InitContainers = append(template.Spec.InitContainers, c)
	return template
}

func (r *MySQLClusterReconciler) uniqueInitContainerName(template corev1.PodTemplateSpec) string {
	i := 0
OUTER:
	for {
		candidate := "init-" + strconv.Itoa(i)
		for _, c := range template.Spec.InitContainers {
			if c.Name == candidate {
				i++
				continue OUTER
			}
		}
		return candidate
	}
}

func (r *MySQLClusterReconciler) createRootPasswordSecret(ctx context.Context, cluster *mysov1alpha1.MySQLCluster) error {
	//TBD
	return nil
}
