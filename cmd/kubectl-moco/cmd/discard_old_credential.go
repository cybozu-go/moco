package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// discardOldCredentialCmd triggers the discard phase of a credential rotation.
var discardOldCredentialCmd = &cobra.Command{
	Use:   "discard-old-credential CLUSTER_NAME",
	Short: "Discard old passwords after rotation",
	Long:  "Discard old passwords after a successful credential rotation. Bumps discardGeneration to match rotationGeneration. Requires the CR to be awaiting discard (DiscardReady=True, DualPassword=True; post-distribute rollout has settled).",
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

	cr := &mocov1beta2.CredentialRotation{}
	if err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cr); err != nil {
		return fmt.Errorf("failed to get CredentialRotation: %w", err)
	}

	if credentialRotationIsStale(cr, cluster) {
		return fmt.Errorf("CredentialRotation %s/%s is stale (ownerReference UID does not match the current cluster). Delete it before discarding", namespace, clusterName)
	}

	if !cr.IsAwaitingDiscard() {
		return fmt.Errorf("cannot discard: the CR must be awaiting discard (DiscardReady=True, DualPassword=True; post-distribute rollout has settled); current step: %q", cr.Step())
	}

	// Bump discardGeneration to match rotationGeneration, signaling that the
	// retained old password from the current rotation should be discarded.
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"discardGeneration": cr.Spec.RotationGeneration,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}
	if err := kubeClient.Patch(ctx, cr, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("failed to patch CredentialRotation: %w", err)
	}
	fmt.Printf("Set discardGeneration=%d on CredentialRotation %s/%s\n", cr.Spec.RotationGeneration, namespace, clusterName)
	return nil
}

func init() {
	rootCmd.AddCommand(discardOldCredentialCmd)
}
