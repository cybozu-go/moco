package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

var config struct {
	metricsAddr              string
	leaderElectionID         string
	binaryCopyContainerImage string
	confInitContainerImage   string
	curlContainerImage       string
	connMaxLifeTime          time.Duration
	connectionTimeout        time.Duration
	readTimeout              time.Duration
	waitTime                 time.Duration
}

var rootCmd = &cobra.Command{
	Use:     "moco-controller",
	Version: moco.Version,
	Short:   "MOCO controller",
	Long:    `MOCO controller manages MySQL cluster with binlog-based semi-sync replication.`,

	PreRunE: func(cmd *cobra.Command, args []string) error {
		if config.confInitContainerImage == "" {
			return errors.New("conf-init-container-image is mandatory")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return subMain()
	},
}

const (
	defaultBinaryCopyContainerImage = "ghcr.io/cybozu-go/moco-agent:0.1.0"
	defaultInitContainerImage       = "quay.io/cybozu/moco-conf-gen:0.3.0"
	defaultCurlContainerImage       = "quay.io/cybozu/ubuntu:20.04"
)

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
	fs.StringVar(&config.leaderElectionID, "leader-election-id", "moco", "ID for leader election by controller-runtime")
	fs.StringVar(&config.binaryCopyContainerImage, "binary-copy-container-image", defaultBinaryCopyContainerImage, "The container image name that includes moco's binaries")
	fs.StringVar(&config.confInitContainerImage, "conf-init-container-image", defaultInitContainerImage, "The container image name of moco-conf-gen")
	fs.StringVar(&config.curlContainerImage, "curl-container-image", defaultCurlContainerImage, "The container image name of curl")
	fs.DurationVar(&config.connMaxLifeTime, connMaxLifetimeFlag, 30*time.Minute, "The maximum amount of time a connection may be reused")
	fs.DurationVar(&config.connectionTimeout, connectionTimeoutFlag, 3*time.Second, "Dial timeout")
	fs.DurationVar(&config.readTimeout, readTimeoutFlag, 30*time.Second, "I/O read timeout")
	fs.DurationVar(&config.waitTime, waitTimeFlag, 10*time.Second, "The waiting time which some tasks are under processing")

	goflags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(goflags)
	fs.AddGoFlagSet(goflags)
}
