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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (r *MySQLClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("mysqlcluster", req.NamespacedName)

	cluster := &mysov1alpha1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		log.Error(err, "unable to fetch MySQLCluster")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// CreateOrUpdate MySQL StatefulSet
	sts := &appsv1.StatefulSet{}
	sts.SetNamespace(req.Namespace)
	sts.SetName(req.Name)
	op, err := ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Spec.Replicas = cluster.Spec.Replicas
		sts.Spec.Template = cluster.Spec.PodTemplate
		sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{cluster.Spec.VolumeClaimTemplate}
		return nil
	})
	if err != nil {
		log.Error(err, "unable to create-or-update StatefulSet")
		return ctrl.Result{}, err
	} else {
		log.Info("reconcile successfully", op)
	}

	// Initialize MySQL if the corresponding Secret does not exist
	secret := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: cluster.Spec.RootPasswordSecretName}, secret)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "unable to get Secret")
			return ctrl.Result{}, err
		}
		err = r.SetupMySQLCluster(ctx, req)
		if err != nil {
			log.Error(err, "unable to setup cluster")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *MySQLClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mysov1alpha1.MySQLCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
}

func (r *MySQLClusterReconciler) SetupMySQLCluster(ctx context.Context, req ctrl.Request) error {
	//TBD
	return nil
}
