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
	Long:  "Rotate system user passwords for a MOCO cluster by creating a single-use CredentialRotation CR.",
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

	// A leftover CR occupies the name; explain its state instead of a bare
	// AlreadyExists.
	existing := &mocov1beta2.CredentialRotation{}
	err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, existing)
	switch {
	case err == nil:
		if existing.IsStaleFor(cluster) {
			return fmt.Errorf("a CredentialRotation left over from a previous MySQLCluster occupies the name %s/%s. Delete it, then re-run this command", namespace, clusterName)
		}
		switch existing.Status.Phase {
		case mocov1beta2.PhaseSucceeded:
			return fmt.Errorf("the previous rotation succeeded and its CredentialRotation is waiting for automatic deletion (%s). Delete it to start the next rotation immediately: kubectl -n %s delete credentialrotation %s", existing.Status.Message, namespace, clusterName)
		case mocov1beta2.PhaseFailed:
			return fmt.Errorf("the previous rotation failed: %s. Follow the recovery procedure, then delete the CredentialRotation and re-run this command", existing.Status.Message)
		default:
			return fmt.Errorf("a rotation is already in flight (phase: %q). Wait for it to complete, or delete the CredentialRotation to abandon it", existing.Status.Phase)
		}
	case !apierrors.IsNotFound(err):
		return fmt.Errorf("failed to get CredentialRotation: %w", err)
	}

	cr := &mocov1beta2.CredentialRotation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Spec: mocov1beta2.CredentialRotationSpec{
			Discard: false,
		},
	}
	// ownerReference is set by the controller on first reconcile.
	if err := kubeClient.Create(ctx, cr); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("a CredentialRotation was created concurrently; re-run the command to see its state: %w", err)
		}
		return fmt.Errorf("failed to create CredentialRotation: %w", err)
	}
	fmt.Printf("Created CredentialRotation %s/%s; the rotation is starting\n", namespace, clusterName)
	fmt.Printf("Follow it with: kubectl -n %s get credentialrotation %s -w\n", namespace, clusterName)
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

func init() {
	rootCmd.AddCommand(rotateCredentialCmd)
}
