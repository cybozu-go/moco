package cmd

import (
	"context"
	"fmt"
	"os"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
)

func init() {
	rootCmd.AddCommand(stopCmd)
	stopCmd.AddCommand(stopClusteringCmd)
	stopCmd.AddCommand(stopReconciliationCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stops the MySQLCluster reconciliation or clustering",
	Long:  "The stop command is used to halt the reconciliation or clustering of MySQLCluster",
}

var stopClusteringCmd = &cobra.Command{
	Use:   "clustering CLUSTER_NAME",
	Short: "Stop the specified MySQLCluster's clustering",
	Long:  "stop clustering is a command to stop the clustering of the specified MySQLCluster. It requires the cluster name as the parameter.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return stopClustering(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func stopClustering(ctx context.Context, name string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	orig := cluster.DeepCopy()

	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}
	cluster.Annotations[constants.AnnClusteringStopped] = "true"

	if equality.Semantic.DeepEqual(orig, cluster) {
		fmt.Fprintf(os.Stdout, "The clustering is already stopped.")
		return nil
	}

	if err := kubeClient.Update(ctx, cluster); err != nil {
		return fmt.Errorf("failed to stop clustering of MySQLCluster: %w", err)
	}

	fmt.Fprintf(os.Stdout, "stopped clustering of MySQLCluster %q\n", fmt.Sprintf("%s/%s", namespace, name))

	return nil
}

var stopReconciliationCmd = &cobra.Command{
	Use:   "reconciliation CLUSTER_NAME",
	Short: "Stop the specified MySQLCluster's reconciliation",
	Long:  "stop reconciliation is a command to stop the reconciliation process for the specified MySQLCluster. This requires the cluster name as the parameter.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return stopReconciliation(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func stopReconciliation(ctx context.Context, name string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	orig := cluster.DeepCopy()

	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}
	cluster.Annotations[constants.AnnReconciliationStopped] = "true"

	if equality.Semantic.DeepEqual(orig, cluster) {
		fmt.Fprintf(os.Stdout, "The reconciliation is already stopped.")
		return nil
	}

	if err := kubeClient.Update(ctx, cluster); err != nil {
		return fmt.Errorf("failed to stop reconciliation of MySQLCluster: %w", err)
	}

	fmt.Fprintf(os.Stdout, "stopped reconciliation of MySQLCluster %q\n", fmt.Sprintf("%s/%s", namespace, name))

	return nil
}
