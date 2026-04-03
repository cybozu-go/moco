package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialConfig struct {
	user   string
	format string
}

// credentialCmd represents the credential parent command.
// When called without a subcommand (e.g. "credential CLUSTER_NAME"),
// it falls back to "show" for backward compatibility.
var credentialCmd = &cobra.Command{
	Use:   "credential [CLUSTER_NAME]",
	Short: "Manage MySQL credentials",
	Long:  "Manage MySQL credentials for a MOCO cluster. When called with a cluster name and no subcommand, shows the credential (backward compatible).",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return credentialShow(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

// credentialShowCmd shows the credential of a specified user
var credentialShowCmd = &cobra.Command{
	Use:   "show CLUSTER_NAME",
	Short: "Show the credential of a specified user",
	Long:  "Show the credential of a specified user.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return credentialShow(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

// credentialRotateCmd triggers a credential rotation
var credentialRotateCmd = &cobra.Command{
	Use:   "rotate CLUSTER_NAME",
	Short: "Rotate system user passwords",
	Long:  "Rotate system user passwords for a MOCO cluster. Creates a CredentialRotation CR if it doesn't exist, or increments rotationGeneration.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return credentialRotate(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

// credentialDiscardCmd triggers the discard phase
var credentialDiscardCmd = &cobra.Command{
	Use:   "discard CLUSTER_NAME",
	Short: "Discard old passwords after rotation",
	Long:  "Discard old passwords after a successful credential rotation. Requires Phase=Rotated.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return credentialDiscard(cmd.Context(), args[0])
	},
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return mysqlClusterCandidates(cmd.Context(), cmd, args, toComplete)
	},
}

func credentialShow(ctx context.Context, clusterName string) error {
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

func credentialRotate(ctx context.Context, clusterName string) error {
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
				DiscardOldPassword: false,
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

	// CR exists - validate phase and bump generation
	phase := cr.Status.Phase
	if phase != "" && phase != mocov1beta2.RotationPhaseCompleted {
		return fmt.Errorf("cannot rotate: rotation is in progress (phase=%s)", phase)
	}

	newGen := cr.Spec.RotationGeneration + 1
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"rotationGeneration": newGen,
			"discardOldPassword": false,
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

func credentialDiscard(ctx context.Context, clusterName string) error {
	cr := &mocov1beta2.CredentialRotation{}
	if err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cr); err != nil {
		return fmt.Errorf("failed to get CredentialRotation: %w", err)
	}

	if cr.Status.Phase != mocov1beta2.RotationPhaseRotated {
		return fmt.Errorf("cannot discard: phase must be Rotated, currently %q", cr.Status.Phase)
	}

	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"discardOldPassword": true,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}
	if err := kubeClient.Patch(ctx, cr, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("failed to patch CredentialRotation: %w", err)
	}
	fmt.Printf("Set discardOldPassword=true on CredentialRotation %s/%s\n", namespace, clusterName)
	return nil
}

func init() {
	// Flags on the parent command for backward compatibility
	// ("kubectl moco credential -u moco-admin CLUSTER_NAME").
	fs := credentialCmd.Flags()
	fs.StringVarP(&credentialConfig.user, "mysql-user", "u", constants.ReadOnlyUser, "User for login to mysql")
	fs.StringVar(&credentialConfig.format, "format", "plain", "The format of output [`plain` or `mycnf`]")

	_ = credentialCmd.RegisterFlagCompletionFunc("mysql-user", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{constants.ReadOnlyUser, constants.WritableUser, constants.AdminUser}, cobra.ShellCompDirectiveDefault
	})
	_ = credentialCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"plain", "mycnf"}, cobra.ShellCompDirectiveDefault
	})

	// "show" subcommand shares the same flags via the parent.
	credentialShowCmd.Flags().AddFlagSet(credentialCmd.Flags())

	credentialCmd.AddCommand(credentialShowCmd)
	credentialCmd.AddCommand(credentialRotateCmd)
	credentialCmd.AddCommand(credentialDiscardCmd)
	rootCmd.AddCommand(credentialCmd)
}
