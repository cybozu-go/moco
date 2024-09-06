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
	rootCmd.AddCommand(startCmd)
	startCmd.AddCommand(startClusteringCmd)
	startCmd.AddCommand(startReconciliationCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Starts the MySQLCluster reconciliation or clustering",
	Long:  "The start command is used to start the reconciliation or clustering of MySQLCluster",
}

var startClusteringCmd = &cobra.Command{
	Use:   "clustering CLUSTER_NAME",
	Short: "Start the specified MySQLCluster's clustering",
	Long:  "start clustering is a command to start the clustering of the specified MySQLCluster. It requires the cluster name as the parameter.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return startClustering(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func startClustering(ctx context.Context, name string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	orig := cluster.DeepCopy()

	if ann, ok := cluster.Annotations[constants.AnnClusteringStopped]; ok && ann == "true" {
		delete(cluster.Annotations, constants.AnnClusteringStopped)
	}

	if equality.Semantic.DeepEqual(orig, cluster) {
		fmt.Fprintf(os.Stdout, "The clustering is already running.\n")
		return nil
	}

	if err := kubeClient.Update(ctx, cluster); err != nil {
		return fmt.Errorf("failed to start clustering of MySQLCluster: %w", err)
	}

	fmt.Fprintf(os.Stdout, "started clustering of MySQLCluster %q\n", fmt.Sprintf("%s/%s", namespace, name))

	return nil
}

var startReconciliationCmd = &cobra.Command{
	Use:   "reconciliation CLUSTER_NAME",
	Short: "Start the specified MySQLCluster's reconciliation",
	Long:  "start reconciliation is a command to start the reconciliation process for the specified MySQLCluster. This requires the cluster name as the parameter.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return startReconciliation(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func startReconciliation(ctx context.Context, name string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	orig := cluster.DeepCopy()

	if ann, ok := cluster.Annotations[constants.AnnReconciliationStopped]; ok && ann == "true" {
		delete(cluster.Annotations, constants.AnnReconciliationStopped)
	}

	if equality.Semantic.DeepEqual(orig, cluster) {
		fmt.Fprintf(os.Stdout, "The reconciliation is already running.\n")
		return nil
	}

	if err := kubeClient.Update(ctx, cluster); err != nil {
		return fmt.Errorf("failed to start reconciliation of MySQLCluster: %w", err)
	}

	fmt.Fprintf(os.Stdout, "started reconciliation of MySQLCluster %q\n", fmt.Sprintf("%s/%s", namespace, name))

	return nil
}
