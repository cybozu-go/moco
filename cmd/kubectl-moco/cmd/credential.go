package cmd

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialConfig struct {
	user   string
	format string
}

var credentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "Manage credentials for MySQLCluster",
	Long:  "The credential command is used to show, rotate, and discard credentials for MySQLCluster.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			fmt.Fprintln(cmd.ErrOrStderr(), "Warning: 'kubectl moco credential CLUSTER_NAME' is deprecated, use 'kubectl moco credential show CLUSTER_NAME' instead.")
			return fetchCredential(cmd.Context(), args[0])
		}
		return cmd.Help()
	},
}

var credentialShowCmd = &cobra.Command{
	Use:   "show CLUSTER_NAME",
	Short: "Fetch the credential of a specified user",
	Long:  "Fetch the credential of a specified user.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fetchCredential(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func fetchCredential(ctx context.Context, clusterName string) error {
	password, err := getPassword(ctx, clusterName, credentialConfig.user)
	if err != nil {
		return err
	}
	switch credentialConfig.format {
	case "plain":
		fmt.Println(password)
	case "mycnf":
		fmt.Printf(`[client]
user=%s
password="%s"
`, credentialConfig.user, password)
	default:
		return fmt.Errorf("unknown format: %s", credentialConfig.format)
	}
	return nil
}

var credentialRotateCmd = &cobra.Command{
	Use:   "rotate CLUSTER_NAME",
	Short: "Rotate system user passwords for the specified MySQLCluster",
	Long: `rotate triggers Phase 1 of password rotation for the specified MySQLCluster.
This generates new passwords, executes ALTER USER ... RETAIN CURRENT PASSWORD on all instances
(with sql_log_bin=0), and distributes the new passwords to per-namespace Secrets.
After this command, both old and new passwords are valid (MySQL dual password).
Run "kubectl moco credential discard" after verifying that applications work with the new credentials.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return rotatePassword(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

// rotatePassword sets the password-rotate annotation on the MySQLCluster.
// It uses Patch (not Update) to apply only the annotation change, avoiding
// conflicts with concurrent controller operations on spec/status fields.
// Annotations are one-shot triggers: the controller consumes and removes them.
func rotatePassword(ctx context.Context, name string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	rotationStatus := cluster.Status.SystemUserRotation

	// Block if a rotation is already in progress (any non-Idle phase).
	if rotationStatus.Phase != mocov1beta2.RotationPhaseIdle {
		if rotationStatus.Phase == mocov1beta2.RotationPhaseDistributed {
			return fmt.Errorf("password rotation is complete but old passwords have not been discarded yet (rotationID: %s). Run \"kubectl moco credential discard %s\" first", rotationStatus.RotationID, name)
		}
		return fmt.Errorf("password rotation is already in progress (rotationID: %s, phase: %s). Wait for it to complete before requesting a new rotation", rotationStatus.RotationID, rotationPhaseDisplay(rotationStatus.Phase))
	}

	if ann := cluster.Annotations[constants.AnnPasswordRotate]; ann != "" {
		// If the annotation matches the last completed rotation and Phase is Idle,
		// the annotation is stale (best-effort removal failed). Allow overwriting
		// with a new rotation request.
		if rotationStatus.LastRotationID == ann {
			// Stale annotation; proceed to overwrite with new rotation.
		} else {
			fmt.Printf("Password rotation is already requested (rotationID: %s). Waiting for the controller to process it.\n", ann)
			return nil
		}
	}

	rotationID := uuid.NewString()
	base := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}
	cluster.Annotations[constants.AnnPasswordRotate] = rotationID

	if err := kubeClient.Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("failed to request password rotation for MySQLCluster: %w", err)
	}

	fmt.Printf("requested password rotation for MySQLCluster %q (rotationID: %s)\n", fmt.Sprintf("%s/%s", namespace, name), rotationID)
	return nil
}

var credentialDiscardCmd = &cobra.Command{
	Use:   "discard CLUSTER_NAME",
	Short: "Discard old system user passwords for the specified MySQLCluster",
	Long: `discard triggers Phase 2 of password rotation for the specified MySQLCluster.
This executes ALTER USER ... DISCARD OLD PASSWORD on all instances (with sql_log_bin=0),
confirms the new passwords in the source Secret, and resets the rotation status to Idle.
This command requires that "kubectl moco credential rotate" has been run first and completed successfully.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return discardPassword(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

// discardPassword sets the password-discard annotation on the MySQLCluster.
// It uses Patch (not Update) to apply only the annotation change.
// See rotatePassword for rationale.
func discardPassword(ctx context.Context, name string) error {
	cluster := &mocov1beta2.MySQLCluster{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}

	rotationStatus := cluster.Status.SystemUserRotation

	if rotationStatus.Phase != mocov1beta2.RotationPhaseDistributed {
		return fmt.Errorf("cannot discard: password rotation is not in Distributed phase (current phase: %s). Run \"kubectl moco credential rotate %s\" first", rotationPhaseDisplay(rotationStatus.Phase), name)
	}

	if ann := cluster.Annotations[constants.AnnPasswordDiscard]; ann != "" {
		if ann == rotationStatus.RotationID {
			fmt.Printf("Password discard is already requested (rotationID: %s).\n", ann)
			return nil
		}
		return fmt.Errorf("password-discard annotation already exists with unexpected rotationID %q (expected %q). "+
			"Remove it manually and retry: kubectl -n %s annotate mysqlcluster %s %s-",
			ann, rotationStatus.RotationID, namespace, name, constants.AnnPasswordDiscard)
	}

	base := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}
	cluster.Annotations[constants.AnnPasswordDiscard] = rotationStatus.RotationID

	if err := kubeClient.Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("failed to request password discard for MySQLCluster: %w", err)
	}

	fmt.Printf("requested password discard for MySQLCluster %q (rotationID: %s)\n", fmt.Sprintf("%s/%s", namespace, name), rotationStatus.RotationID)

	// Best-effort rollout status hint. Non-fatal if it fails.
	sts := &appsv1.StatefulSet{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cluster.PrefixedName()}, sts); err == nil {
		replicas := int32(1)
		if sts.Spec.Replicas != nil {
			replicas = *sts.Spec.Replicas
		}
		if sts.Status.ReadyReplicas < replicas || sts.Status.UpdatedReplicas < replicas {
			fmt.Printf("Note: StatefulSet rollout is still in progress (%d/%d ready, %d/%d updated). "+
				"The controller will wait for rollout completion before discarding.\n",
				sts.Status.ReadyReplicas, replicas, sts.Status.UpdatedReplicas, replicas)
		}
	}

	return nil
}

func rotationPhaseDisplay(phase mocov1beta2.RotationPhase) string {
	if phase == mocov1beta2.RotationPhaseIdle {
		return "Idle"
	}
	return string(phase)
}

func init() {
	fs := credentialShowCmd.Flags()
	fs.StringVarP(&credentialConfig.user, "mysql-user", "u", constants.ReadOnlyUser, "User for login to mysql")
	fs.StringVar(&credentialConfig.format, "format", "plain", "The format of output [`plain` or `mycnf`]")

	_ = credentialShowCmd.RegisterFlagCompletionFunc("mysql-user", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{constants.ReadOnlyUser, constants.WritableUser, constants.AdminUser}, cobra.ShellCompDirectiveDefault
	})
	_ = credentialShowCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"plain", "mycnf"}, cobra.ShellCompDirectiveDefault
	})

	credentialCmd.AddCommand(credentialShowCmd)
	credentialCmd.AddCommand(credentialRotateCmd)
	credentialCmd.AddCommand(credentialDiscardCmd)
	rootCmd.AddCommand(credentialCmd)
}
