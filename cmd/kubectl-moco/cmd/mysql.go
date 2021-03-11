package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	cmdexec "k8s.io/kubectl/pkg/cmd/exec"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

const (
	defaultPodExecTimeout = 60 * time.Second
	mysqldContainerName   = "mysqld"
)

var mysqlConfig struct {
	user  string
	index int
	stdin bool
	tty   bool
}

// mysqlCmd represents the mysql command
var mysqlCmd = &cobra.Command{
	Use:   "mysql <CLUSTER_NAME> [COMMANDS]",
	Short: "Run mysql command in a specified MySQL instance",
	Long:  "Run mysql command in a specified MySQL instance.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMySQLCommand(cmd.Context(), args[0], cmd, args[1:])
	},
}

func runMySQLCommand(ctx context.Context, clusterName string, cmd *cobra.Command, args []string) error {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	cluster := &mocov1alpha1.MySQLCluster{}
	err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cluster)
	if err != nil {
		return err
	}

	podName, err := getPodName(ctx, cluster, mysqlConfig.index)
	if err != nil {
		return err
	}

	myCnfPath := fmt.Sprintf("%s/%s-my.cnf", moco.MyCnfSecretPath, mysqlConfig.user)
	commands := append([]string{podName, "--", "mysql", "--defaults-extra-file=" + myCnfPath}, args...)
	argsLenAtDash := 2
	options := &cmdexec.ExecOptions{
		StreamOptions: cmdexec.StreamOptions{
			IOStreams: genericclioptions.IOStreams{
				In:     os.Stdin,
				Out:    os.Stdout,
				ErrOut: os.Stderr,
			},
			Stdin:         mysqlConfig.stdin,
			TTY:           mysqlConfig.tty,
			ContainerName: mysqldContainerName,
		},

		Executor: &cmdexec.DefaultRemoteExecutor{},
	}
	cmdutil.AddPodRunningTimeoutFlag(cmd, defaultPodExecTimeout)
	cmdutil.CheckErr(options.Complete(factory, cmd, commands, argsLenAtDash))
	cmdutil.CheckErr(options.Validate())
	cmdutil.CheckErr(options.Run())

	return nil
}

func init() {
	fs := mysqlCmd.Flags()
	fs.StringVarP(&mysqlConfig.user, "mysql-user", "u", "moco-readonly", "User for login to mysql [`moco-writable` or `moco-readonly`]")
	fs.IntVar(&mysqlConfig.index, "index", -1, "Index of a target mysql instance")
	fs.BoolVarP(&mysqlConfig.stdin, "stdin", "i", false, "Pass stdin to the mysql container")
	fs.BoolVarP(&mysqlConfig.tty, "tty", "t", false, "Stdin is a TTY")

	rootCmd.AddCommand(mysqlCmd)
}
