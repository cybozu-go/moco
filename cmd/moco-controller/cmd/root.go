package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/cybozu-go/myso"
	"github.com/spf13/cobra"
	"k8s.io/klog"
)

var config struct {
	metricsAddr      string
	leaderElectionID string
}

var rootCmd = &cobra.Command{
	Use:     "moco-controller",
	Version: myso.Version,
	Short:   "MOCO controller",
	Long:    `MOCO controller manages MySQL cluster with binlog-based semi-sync replication.`,

	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return subMain()
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
	fs.StringVar(&config.metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	fs.StringVar(&config.leaderElectionID, "leader-election-id", "moco", "ID for leader election by controller-runtime")

	goflags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(goflags)
	fs.AddGoFlagSet(goflags)
}
