package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cybozu-go/moco"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

var config struct {
	metricsAddr            string
	leaderElectionID       string
	confInitContainerImage string
	curlContainerImage     string
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
	defaultInitContainerImage = "quay.io/cybozu/moco-conf-gen:1.0.0"
	defaultCurlContainerImage = "quay.io/cybozu/ubuntu:18.04"
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
	fs.StringVar(&config.confInitContainerImage, "conf-init-container-image", defaultInitContainerImage, "The container image name of moco-conf-gen")
	fs.StringVar(&config.curlContainerImage, "curl-container-image", defaultCurlContainerImage, "The container image name of curl")

	goflags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(goflags)
	fs.AddGoFlagSet(goflags)
}
