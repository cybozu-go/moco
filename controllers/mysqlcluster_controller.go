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
		log.Error(err, "unable to fetch MySQLCluster", "name", req.NamespacedName)
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
		log.Info("debug", "cluster.Spec.VolumeClaimTemplates", cluster.Spec.VolumeClaimTemplates)
		log.Info("debug", "cluster.Spec.VolumeClaimTemplates[0]", cluster.Spec.VolumeClaimTemplates[0])
		sts.Spec.VolumeClaimTemplates = r.getVolumeClaimTemplates(cluster.Spec.VolumeClaimTemplates, cluster)
		log.Info("debug", "sts.Spec.VolumeClaimTemplates", sts.Spec.VolumeClaimTemplates)
		return ctrl.SetControllerReference(cluster, sts, r.Scheme)
	})
	if err != nil {
		log.Error(err, "unable to create-or-update StatefulSet")
		return ctrl.Result{}, err
	}
	log.Info("reconcile successfully", "op", op)

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
	log.Info("reconcile successfully", "op", op)

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

func (r *MySQLClusterReconciler) getPodTemplate(template mysov1alpha1.PodTemplateSpec, cluster *mysov1alpha1.MySQLCluster) corev1.PodTemplateSpec {
	log := r.Log.WithValues("mysqlcluster", types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace})

	newTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:            template.Name,
			GenerateName:    template.GenerateName,
			Namespace:       cluster.Namespace,
			Labels:          template.Labels,
			Annotations:     template.Annotations,
			OwnerReferences: template.OwnerReferences,
		},
		Spec: template.Spec,
	}

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

	return newTemplate
}

func (r *MySQLClusterReconciler) getVolumeClaimTemplates(templates []mysov1alpha1.PersistentVolumeClaim, cluster *mysov1alpha1.MySQLCluster) []corev1.PersistentVolumeClaim {
	newTemplates := make([]corev1.PersistentVolumeClaim, len(templates))

	for i, template := range templates {
		newTemplates[i] = corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:            template.Name,
				GenerateName:    template.GenerateName,
				Namespace:       cluster.Namespace,
				Labels:          template.Labels,
				Annotations:     template.Annotations,
				OwnerReferences: template.OwnerReferences,
			},
			Spec: template.Spec,
		}
	}

	return newTemplates
}

func (r *MySQLClusterReconciler) createRootPasswordSecret(ctx context.Context, cluster *mysov1alpha1.MySQLCluster) error {
	//TBD
	return nil
}
