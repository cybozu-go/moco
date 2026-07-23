package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// rotateCredentialCmd triggers a credential rotation.
var rotateCredentialCmd = &cobra.Command{
	Use:   "rotate-credential CLUSTER_NAME",
	Short: "Rotate system user passwords",
	Long:  "Rotate system user passwords for a MOCO cluster. Creates a CredentialRotation CR if it doesn't exist, or increments rotationGeneration.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return rotateCredential(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func rotateCredential(ctx context.Context, clusterName string) error {
	// Check that the MySQLCluster exists and has replicas > 0
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cluster); err != nil {
		return fmt.Errorf("failed to get MySQLCluster: %w", err)
	}
	if cluster.Spec.Replicas <= 0 {
		return errors.New("cannot rotate: MySQLCluster has 0 replicas")
	}

	// Check if CR already exists
	cr := &mocov1beta2.CredentialRotation{}
	err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cr)

	if apierrors.IsNotFound(err) {
		// Create new CR with rotationGeneration: 1
		cr = &mocov1beta2.CredentialRotation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: mocov1beta2.CredentialRotationSpec{
				RotationGeneration: 1,
			},
		}
		// ownerReference is set by the controller on first reconcile.
		if err := kubeClient.Create(ctx, cr); err != nil {
			return fmt.Errorf("failed to create CredentialRotation: %w", err)
		}
		fmt.Printf("Created CredentialRotation %s/%s with rotationGeneration=1\n", namespace, clusterName)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get CredentialRotation: %w", err)
	}

	// CR exists - reject stale CRs (owned by a previously deleted cluster of
	// the same name). The reconciler ignores such CRs, so bumping
	// rotationGeneration here would silently succeed without starting a
	// rotation. The user must delete the stale CR first.
	if credentialRotationIsStale(cr, cluster) {
		return fmt.Errorf("CredentialRotation %s/%s is stale (ownerReference UID does not match the current cluster). Delete it before rotating", namespace, clusterName)
	}

	// CR exists - require it to be idle before bumping rotationGeneration.
	if !cr.IsIdle() {
		return fmt.Errorf("cannot rotate: a rotation cycle is in flight (current step: %q). Wait for it to complete or follow the recovery procedure", cr.Step())
	}

	newGen := cr.Spec.RotationGeneration + 1
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"rotationGeneration": newGen,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}
	if err := kubeClient.Patch(ctx, cr, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("failed to patch CredentialRotation: %w", err)
	}
	fmt.Printf("Updated CredentialRotation %s/%s with rotationGeneration=%d\n", namespace, clusterName, newGen)
	return nil
}

// credentialRotationIsStale reports whether cr carries a MySQLCluster owner
// reference whose UID does not match the live cluster, with no matching
// reference. That signals a CR left over after a cluster was deleted and
// another recreated under the same name; the reconciler ignores such CRs, so
// the CLI must refuse to act on them. A CR with no MySQLCluster ownerRef yet
// is treated as fresh (the controller has not adopted it yet).
func credentialRotationIsStale(cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) bool {
	hasStale := false
	for _, ref := range cr.OwnerReferences {
		if ref.Kind != "MySQLCluster" {
			continue
		}
		if ref.UID == cluster.UID {
			return false
		}
		hasStale = true
	}
	return hasStale
}

func init() {
	rootCmd.AddCommand(rotateCredentialCmd)
}
