package cmd

import (
	"context"
	"errors"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

var switchoverCmd = &cobra.Command{
	Use:   "switchover CLUSTER_NAME",
	Short: "Switch the primary instance",
	Long:  "Switch the primary instance to one of the replicas.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return switchover(cmd.Context(), args[0])
	},
}

func switchover(ctx context.Context, name string) error {
	cluster := &mocov1beta1.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	if cluster.Spec.Replicas == 1 {
		return errors.New("single-instance cluster is not able to switch")
	}

	podName := cluster.PodName(cluster.Status.CurrentPrimaryIndex)
	pod := &corev1.Pod{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: podName}, pod); err != nil {
		return err
	}

	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[constants.AnnDemote] = "true"

	return kubeClient.Update(ctx, pod)
}

func init() {
	rootCmd.AddCommand(switchoverCmd)
}
