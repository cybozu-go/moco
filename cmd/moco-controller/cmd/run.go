package cmd

import (
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/clustering"
	"github.com/cybozu-go/moco/controllers"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/metrics"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
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
	// +kubebuilder:scaffold:scheme
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

	clusterMgr := clustering.NewClusterManager(config.interval, mgr, dbop.DefaultOperatorFactory, clusterLog)
	defer dbop.DefaultOperatorFactory.Cleanup()

	if err = (&controllers.MySQLClusterReconciler{
		Client:              mgr.GetClient(),
		Scheme:              mgr.GetScheme(),
		AgentContainerImage: config.agentContainerImage,
		FluentBitImage:      config.fluentBitImage,
		SystemNamespace:     ns,
		ClusterManager:      clusterMgr,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MySQLCluster")
		return err
	}

	if err = (&mocov1beta1.MySQLCluster{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup webhook", "webhook", "MySQLCluster")
		return err
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	metrics.Register(k8smetrics.Registry)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}
	return nil
}
