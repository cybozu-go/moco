package controllers

import (
	"bytes"
	"context"
	"fmt"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/cybozu-go/moco/runners"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type ClusteringReconciler struct {
	client.Client
	Log logr.Logger
}

func (r *ClusteringReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("mysqlcluster", req.NamespacedName)

	cluster := &mocov1alpha1.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		log.Error(err, "unable to fetch MySQLCluster", "name", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := r.reconcileMySQLCluster(ctx, log, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller for reconciliation.
func (r *ClusteringReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := mgr.GetFieldIndexer().IndexField(&mocov1alpha1.MySQLCluster{}, ".status.ready", selectReadyCluster)
	if err != nil {
		return err
	}
	pred := predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return true },
	}

	ch := make(chan event.GenericEvent)
	watcher := runners.NewMySQLClusterWatcher(mgr.GetClient(), ch)
	err = mgr.Add(watcher)
	if err != nil {
		return err
	}
	src := source.Channel{
		Source: ch,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1alpha1.MySQLCluster{}).
		Watches(&src, &handler.EnqueueRequestForObject{}).
		WithEventFilter(pred).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: 8},
		).
		Complete(r)
}

func selectReadyCluster(obj runtime.Object) []string {
	cluster := obj.(*mocov1alpha1.MySQLCluster)
	return []string{string(cluster.Status.Ready)}
}

// reconcileMySQLCluster recoclies MySQL cluster
func (r *ClusteringReconciler) reconcileMySQLCluster(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) error {
	_, err := r.getMySQLClusterStatus(ctx, log, cluster)
	return err
}

// MySQLClusterStatus contains MySQLCluster status
type MySQLClusterStatus struct {
}

func (r *ClusteringReconciler) getMySQLClusterStatus(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (*MySQLClusterStatus, error) {
	podName := uniqueName(cluster) + "-0"
	var pod corev1.Pod
	err := r.Get(ctx, types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      podName,
	}, &pod)
	if err != nil {
		return nil, err
	}

	stdout, stderr, err := r.ExecuteRemoteCommand(&pod, "mysql --version")
	if err != nil {
		return nil, err
	}
	log.Info("RemoteCommand", "stdout", stdout, "stderr", stderr)
	return nil, nil
}

// ExecuteRemoteCommand executes a remote shell command on the given pod
// returns the output from stdout and stderr
func (r *ClusteringReconciler) ExecuteRemoteCommand(pod *corev1.Pod, command string) (string, string, error) {

	restCfg, err := ctrl.GetConfig()
	if err != nil {
		return "", "", err
	}
	coreClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return "", "", err
	}

	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	request := coreClient.RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"/bin/sh", "-c", command},
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
			TTY:     true,
		}, scheme.ParameterCodec)
	spdyExec, err := remotecommand.NewSPDYExecutor(restCfg, "POST", request.URL())
	if err != nil {
		return "", "", err
	}
	err = spdyExec.Stream(remotecommand.StreamOptions{
		Stdout: buf,
		Stderr: errBuf,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed executing command %s on %v/%v: %w", command, pod.Namespace, pod.Name, err)
	}

	return buf.String(), errBuf.String(), nil
}
