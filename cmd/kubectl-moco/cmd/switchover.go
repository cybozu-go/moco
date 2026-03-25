package cmd

import (
	"context"
	"errors"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var switchoverCmd = &cobra.Command{
	Use:   "switchover CLUSTER_NAME",
	Short: "Switch the primary instance",
	Long:  "Switch the primary instance to one of the replicas.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return switchover(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func switchover(ctx context.Context, name string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	if cluster.Spec.Offline {
		return errors.New("offline cluster is not able to switch")
	}

	if cluster.Spec.Replicas == 1 {
		return errors.New("single-instance cluster is not able to switch")
	}

	podName := cluster.PodName(cluster.Status.CurrentPrimaryIndex)
	pod := &corev1.Pod{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: podName}, pod); err != nil {
		return err
	}

	if pod.Annotations[constants.AnnDemote] == "true" {
		return nil
	}

	newPod := pod.DeepCopy()
	if newPod.Annotations == nil {
		newPod.Annotations = make(map[string]string)
	}
	newPod.Annotations[constants.AnnDemote] = "true"

	return kubeClient.Patch(ctx, newPod, client.MergeFrom(pod))
}

func init() {
	rootCmd.AddCommand(switchoverCmd)
}
