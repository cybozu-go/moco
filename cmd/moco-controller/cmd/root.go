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
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var defaultAgentContainerImage string

var config struct {
	metricsAddr         string
	probeAddr           string
	leaderElectionID    string
	webhookAddr         string
	certDir             string
	agentContainerImage string
	fluentBitImage      string
	exporterImage       string
	interval            time.Duration
	zapOpts             zap.Options
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
			defaultAgentContainerImage = "ghcr.io/cybozu-go/moco-agent:" + mod.Version[1:]
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
	fs.StringVar(&config.metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to")
	fs.StringVar(&config.probeAddr, "health-probe-addr", ":8081", "Listen address for health probes")
	fs.StringVar(&config.leaderElectionID, "leader-election-id", "moco", "ID for leader election by controller-runtime")
	fs.StringVar(&config.webhookAddr, "webhook-addr", ":9443", "Listen address for the webhook endpoint")
	fs.StringVar(&config.certDir, "cert-dir", "", "webhook certificate directory")
	fs.StringVar(&config.agentContainerImage, "agent-container-image", defaultAgentContainerImage, "The container image name that includes moco-agent")
	fs.StringVar(&config.fluentBitImage, "fluent-bit-image", moco.FluentBitImage, "The image of fluent-bit sidecar container")
	fs.StringVar(&config.exporterImage, "mysqld-exporter-image", moco.ExporterImage, "The image of mysqld_exporter sidecar container")
	fs.DurationVar(&config.interval, "check-interval", 1*time.Minute, "Interval of cluster maintenance")

	goflags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(goflags)
	config.zapOpts.BindFlags(goflags)

	fs.AddGoFlagSet(goflags)
}
