package cmd

import (
	"flag"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	defaultAgentImage  string
	defaultBackupImage = "ghcr.io/cybozu-go/moco-backup:" + moco.Version
)

var config struct {
	metricsAddr             string
	probeAddr               string
	pprofAddr               string
	leaderElectionID        string
	webhookAddr             string
	certDir                 string
	grpcCertDir             string
	agentImage              string
	backupImage             string
	fluentBitImage          string
	exporterImage           string
	interval                time.Duration
	maxConcurrentReconciles int
	qps                     int
	zapOpts                 zap.Options
}

func init() {
	info, _ := debug.ReadBuildInfo()
	if info == nil {
		return
	}
	for _, mod := range info.Deps {
		if mod.Path != "github.com/cybozu-go/moco-agent" {
			continue
		}
		if len(mod.Version) > 2 && strings.HasPrefix(mod.Version, "v") {
			defaultAgentImage = "ghcr.io/cybozu-go/moco-agent:" + mod.Version[1:]
			return
		}
	}
	panic("no module info for github.com/cybozu-go/moco-agent")
}

var rootCmd = &cobra.Command{
	Use:     "moco-controller",
	Version: moco.Version,
	Short:   "MOCO controller",
	Long:    `MOCO controller`,

	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		h, p, err := net.SplitHostPort(config.webhookAddr)
		if err != nil {
			return fmt.Errorf("invalid webhook address: %s, %v", config.webhookAddr, err)
		}
		numPort, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("invalid webhook address: %s, %v", config.webhookAddr, err)
		}
		ns := os.Getenv(constants.PodNamespaceEnvKey)
		if ns == "" {
			return fmt.Errorf("no environment variable %s", constants.PodNamespaceEnvKey)
		}
		return subMain(ns, h, numPort)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	fs := rootCmd.Flags()
	fs.StringVar(&config.metricsAddr, "metrics-addr", ":8080", "Listen address for metric endpoint")
	fs.StringVar(&config.probeAddr, "health-probe-addr", ":8081", "Listen address for health probes")
	fs.StringVar(&config.pprofAddr, "pprof-addr", "", "Listen address for pprof endpoints. pprof is disabled by default")
	fs.StringVar(&config.leaderElectionID, "leader-election-id", "moco", "ID for leader election by controller-runtime")
	fs.StringVar(&config.webhookAddr, "webhook-addr", ":9443", "Listen address for the webhook endpoint")
	fs.StringVar(&config.certDir, "cert-dir", "", "webhook certificate directory")
	fs.StringVar(&config.grpcCertDir, "grpc-cert-dir", "/grpc-cert", "gRPC certificate directory")
	fs.StringVar(&config.agentImage, "agent-image", defaultAgentImage, "The image of moco-agent sidecar container")
	fs.StringVar(&config.backupImage, "backup-image", defaultBackupImage, "The image of moco-backup container")
	fs.StringVar(&config.fluentBitImage, "fluent-bit-image", moco.FluentBitImage, "The image of fluent-bit sidecar container")
	fs.StringVar(&config.exporterImage, "mysqld-exporter-image", moco.ExporterImage, "The image of mysqld_exporter sidecar container")
	fs.DurationVar(&config.interval, "check-interval", 1*time.Minute, "Interval of cluster maintenance")
	fs.IntVar(&config.maxConcurrentReconciles, "max-concurrent-reconciles", 8, "The maximum number of concurrent reconciles which can be run")
	// The default QPS is 20.
	// https://github.com/kubernetes-sigs/controller-runtime/blob/a26de2d610c3cf4b2a02688534aaf5a65749c743/pkg/client/config/config.go#L84-L85
	fs.IntVar(&config.qps, "apiserver-qps-throttle", 20, "The maximum QPS to the API server.")

	goflags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(goflags)
	config.zapOpts.BindFlags(goflags)

	fs.AddGoFlagSet(goflags)
}
