package cmd

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// discardOldCredentialCmd triggers the discard phase of a credential rotation.
var discardOldCredentialCmd = &cobra.Command{
	Use:   "discard-old-credential CLUSTER_NAME",
	Short: "Discard old passwords after rotation",
	Long:  "Discard old passwords after a successful credential rotation. Sets spec.discard to true. Requires the verification window to be open (DiscardReady=True).",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return discardOldCredential(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func discardOldCredential(ctx context.Context, clusterName string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cluster); err != nil {
		return fmt.Errorf("failed to get MySQLCluster: %w", err)
	}
	if err := checkClusterSafeForCredentialOps(cluster, false); err != nil {
		return err
	}

	cr := &mocov1beta2.CredentialRotation{}
	if err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cr); err != nil {
		return fmt.Errorf("failed to get CredentialRotation: %w", err)
	}

	if cr.IsStaleFor(cluster) {
		return fmt.Errorf("the CredentialRotation %s/%s is a leftover from a previous MySQLCluster. Delete it before discarding", namespace, clusterName)
	}

	if cr.Spec.Discard {
		fmt.Printf("The discard was already requested on CredentialRotation %s/%s (phase: %q)\n", namespace, clusterName, cr.Status.Phase)
		return nil
	}

	if !cr.IsDiscardReady() {
		return fmt.Errorf("cannot discard: the verification window is not open (DiscardReady must be True); current phase: %q", cr.Status.Phase)
	}

	// Use Update instead of Patch so the write carries the resourceVersion
	// of the object the pre-checks above ran against; a concurrent
	// modification surfaces as a Conflict instead of a lost update.
	cr.Spec.Discard = true
	if err := kubeClient.Update(ctx, cr); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("the CredentialRotation was modified concurrently; re-run the command: %w", err)
		}
		return fmt.Errorf("failed to update CredentialRotation: %w", err)
	}
	fmt.Printf("Requested the discard on CredentialRotation %s/%s\n", namespace, clusterName)
	fmt.Printf("Wait for completion with: kubectl -n %s wait credentialrotation %s --for=condition=Finished --timeout=30m\n", namespace, clusterName)
	return nil
}

func init() {
	rootCmd.AddCommand(discardOldCredentialCmd)
}
