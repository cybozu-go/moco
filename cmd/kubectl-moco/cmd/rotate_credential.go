package cmd

import (
	"context"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	if err := checkClusterSafeForCredentialOps(cluster, true); err != nil {
		return err
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

	// Use Update instead of Patch so the write carries the resourceVersion
	// of the object the pre-checks above ran against. If another
	// kubectl-moco (or anything else) modified the CR in between, the
	// apiserver rejects this write with a Conflict instead of silently
	// applying a bump decided against stale state.
	cr.Spec.RotationGeneration++
	if err := kubeClient.Update(ctx, cr); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("the CredentialRotation was modified concurrently; re-run the command: %w", err)
		}
		return fmt.Errorf("failed to update CredentialRotation: %w", err)
	}
	fmt.Printf("Updated CredentialRotation %s/%s with rotationGeneration=%d\n", namespace, clusterName, cr.Spec.RotationGeneration)
	return nil
}

// checkClusterSafeForCredentialOps refuses to start a rotation or discard
// step when the cluster is in a state where the step cannot make progress
// or could act on an unhealthy cluster:
//
//   - spec.offline: no mysqld is running, so RETAIN/DISCARD SQL cannot run.
//   - clustering stopped: the ClusterManager that executes the rotation SQL
//     is suspended for this cluster.
//   - reconciliation stopped (only when needsReconciliation is true): the
//     rotation phase depends on MySQLClusterReconciler distributing the
//     promoted passwords, so it would park in AwaitingRollout. The discard
//     phase does not need it — distribution finished before the CR reached
//     AwaitingDiscard — so discard passes false and stays allowed.
//   - not Healthy: rotating credentials while the cluster is degraded would
//     pile a rolling restart on top of an existing failure.
//
// These are point-in-time checks for operator convenience (fail fast with a
// clear message); the controller and webhook remain the authority.
func checkClusterSafeForCredentialOps(cluster *mocov1beta2.MySQLCluster, needsReconciliation bool) error {
	if cluster.Spec.Offline {
		return errors.New("cannot proceed: the MySQLCluster is offline (spec.offline is true)")
	}
	if needsReconciliation && cluster.Annotations[constants.AnnReconciliationStopped] == "true" {
		return fmt.Errorf("cannot proceed: reconciliation is stopped (annotation %s=true)", constants.AnnReconciliationStopped)
	}
	if cluster.Annotations[constants.AnnClusteringStopped] == "true" {
		return fmt.Errorf("cannot proceed: clustering is stopped (annotation %s=true)", constants.AnnClusteringStopped)
	}
	if !meta.IsStatusConditionTrue(cluster.Status.Conditions, mocov1beta2.ConditionHealthy) {
		return errors.New("cannot proceed: the MySQLCluster is not Healthy")
	}
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
