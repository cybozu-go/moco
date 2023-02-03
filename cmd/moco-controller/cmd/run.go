package cmd

import (
	"context"
	"fmt"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/clustering"
	"github.com/cybozu-go/moco/controllers"
	"github.com/cybozu-go/moco/pkg/cert"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/cybozu-go/moco/pkg/pprof"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	k8smetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	// +kubebuilder:scaffold:imports
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(mocov1beta1.AddToScheme(scheme))
	utilruntime.Must(mocov1beta2.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

type resolver struct {
	reader client.Reader
}

var _ dbop.Resolver = resolver{}

func (r resolver) Resolve(ctx context.Context, cluster *mocov1beta2.MySQLCluster, index int) (string, error) {
	pod := &corev1.Pod{}
	err := r.reader.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.PodName(index)}, pod)
	if err != nil {
		return "", err
	}
	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("pod %s/%s has not been assigned an IP address", pod.Namespace, pod.Name)
	}
	return pod.Status.PodIP, nil
}

func subMain(ns, addr string, port int) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&config.zapOpts)))
	setupLog := ctrl.Log.WithName("setup")
	clusterLog := ctrl.Log.WithName("cluster-manager")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		MetricsBindAddress:      config.metricsAddr,
		HealthProbeBindAddress:  config.probeAddr,
		LeaderElection:          true,
		LeaderElectionID:        config.leaderElectionID,
		LeaderElectionNamespace: ns,
		Host:                    addr,
		Port:                    port,
		CertDir:                 config.certDir,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	r := resolver{reader: mgr.GetClient()}
	opf := dbop.NewFactory(r)
	defer opf.Cleanup()
	reloader, err := cert.NewReloader(config.grpcCertDir, ctrl.Log.WithName("agent-client"))
	if err != nil {
		setupLog.Error(err, "failed to initialize gRPC certificate loader")
		return err
	}
	af := clustering.NewAgentFactory(r, reloader)
	clusterMgr := clustering.NewClusterManager(config.interval, mgr, opf, af, clusterLog)
	defer clusterMgr.StopAll()

	if err = (&controllers.MySQLClusterReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Recorder:                mgr.GetEventRecorderFor("moco-controller"),
		AgentImage:              config.agentImage,
		BackupImage:             config.backupImage,
		FluentBitImage:          config.fluentBitImage,
		ExporterImage:           config.exporterImage,
		SystemNamespace:         ns,
		ClusterManager:          clusterMgr,
		MaxConcurrentReconciles: config.maxConcurrentReconciles,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MySQLCluster")
		return err
	}

	if err = (&controllers.PodWatcher{
		Client:                  mgr.GetClient(),
		ClusterManager:          clusterMgr,
		MaxConcurrentReconciles: config.maxConcurrentReconciles,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PodWatcher")
		return err
	}

	if err = (&mocov1beta1.MySQLCluster{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup webhook", "webhook", "MySQLCluster")
		return err
	}

	if err = (&mocov1beta2.MySQLCluster{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup webhook", "webhook", "MySQLCluster")
		return err
	}

	if err = (&mocov1beta2.BackupPolicy{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup webhook", "webhook", "BackupPolicy")
		return err
	}

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	if config.pprofAddr != "" && config.pprofAddr != "0" {
		if err := mgr.Add(pprof.NewHandler(ctrl.Log.WithName("pprof"), config.pprofAddr)); err != nil {
			setupLog.Error(err, "unable to set pprof handler")
			return err
		}
	}

	metrics.Register(k8smetrics.Registry)

	setupLog.Info("starting manager")
	ctx := ctrl.SetupSignalHandler()
	go reloader.Run(ctx, 1*time.Hour)
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}
	return nil
}
