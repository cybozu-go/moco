package controllers

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/clustering"
	"github.com/cybozu-go/moco/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// PodWatcher watches MySQL pods and informs the cluster manager of the event.
type PodWatcher struct {
	client.Client
	ClusterManager          clustering.ClusterManager
	MaxConcurrentReconciles int
}

//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch

// Reconcile implements Reconciler interface.
// See https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile#Reconciler
func (r *PodWatcher) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	pod := &corev1.Pod{}
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if pod.DeletionTimestamp == nil && pod.Annotations[constants.AnnDemote] != "true" {
		return ctrl.Result{}, nil
	}

	ref := metav1.GetControllerOfNoCopy(pod)
	if ref == nil {
		return ctrl.Result{}, nil
	}
	refGV, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		//lint:ignore nilerr intentional
		return ctrl.Result{}, nil
	}
	if ref.Kind != "StatefulSet" || refGV.Group != appsv1.GroupName {
		return ctrl.Result{}, nil
	}

	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: ref.Name}, sts); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ref = metav1.GetControllerOfNoCopy(sts)
	if ref == nil {
		return ctrl.Result{}, nil
	}
	refGV, err = schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		//lint:ignore nilerr intentional
		return ctrl.Result{}, nil
	}
	if ref.Kind != "MySQLCluster" || refGV.Group != mocov1beta2.GroupVersion.Group {
		return ctrl.Result{}, nil
	}

	log.Info("detected mysql pod deletion", "name", pod.Name)
	r.ClusterManager.UpdateNoStart(types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, string(controller.ReconcileIDFromContext(ctx)))
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodWatcher) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}
