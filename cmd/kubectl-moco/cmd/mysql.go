package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	dockerterm "github.com/docker/docker/pkg/term"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
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
		return runMySQLCommand(cmd.Context(), args[0], args[1:])
	},
}

func runMySQLCommand(ctx context.Context, clusterName string, args []string) error {
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

	index := mysqlConfig.index
	if mysqlConfig.index < 0 {
		if cluster.Status.CurrentPrimaryIndex != nil {
			index = *cluster.Status.CurrentPrimaryIndex
		} else {
			return errors.New("primary instance not found")
		}
	}

	podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), index)
	password, err := getPassword(ctx, cluster, mysqlConfig.user)
	if err != nil {
		return err
	}

	commands := append([]string{"mysql", "-u", mysqlConfig.user, "-p" + password}, args...)
	err = execCommand(restConfig, rawClient, mysqlConfig.stdin, mysqlConfig.tty, cluster.Namespace, podName, commands)

	return err
}

func execCommand(config *rest.Config, client *kubernetes.Clientset, in, tty bool, namespace, pod string, commands []string) error {
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: moco.MysqldContainerName,
		Command:   commands,
		Stdin:     in,
		Stdout:    true,
		Stderr:    true,
		TTY:       in && tty,
	}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return err
	}
	stdin, stdout, stderr := dockerterm.StdStreams()
	opt := remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    in && tty,
	}
	if !in {
		opt.Stdin = nil
	}

	return exec.Stream(opt)
}

func init() {
	fs := mysqlCmd.Flags()
	fs.StringVarP(&mysqlConfig.user, "user", "u", "readonly", "User for login to mysql")
	fs.IntVar(&mysqlConfig.index, "index", -1, "Index of a target mysql instance")
	fs.BoolVarP(&mysqlConfig.stdin, "stdin", "i", false, "Pass stdin to the mysql container")
	fs.BoolVarP(&mysqlConfig.tty, "tty", "t", false, "Stdin is a TTY")

	rootCmd.AddCommand(mysqlCmd)
}
