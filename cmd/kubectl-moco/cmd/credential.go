package cmd

import (
	"context"
	"errors"
	"fmt"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
)

var credentialConfig struct {
	user   string
	format string
}

// credentialCmd represents the credential command
var credentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "Fetch the credential of a specified user",
	Long:  "Fetch the credential of a specified user.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fetchCredential(cmd.Context(), args[0])
	},
}

func fetchCredential(ctx context.Context, clusterName string) error {
	cluster := &mocov1alpha1.MySQLCluster{}
	err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      clusterName,
	}, cluster)
	if err != nil {
		return err
	}
	password, err := getPassword(ctx, cluster, credentialConfig.user)
	if err != nil {
		return err
	}
	switch credentialConfig.format {
	case "plain":
		fmt.Println(password)
	case "myconf":
		fmt.Printf(`[client]
user=%s
password="%s"
`, credentialConfig.user, password)
	default:
		return errors.New("unknown format: " + credentialConfig.format)
	}
	return nil
}

func init() {
	fs := credentialCmd.Flags()
	fs.StringVarP(&credentialConfig.user, "user", "u", "readonly", "User for login to mysql")
	fs.StringVar(&credentialConfig.format, "format", "plain", "The format of output [`plain` or `myconf`]")

	rootCmd.AddCommand(credentialCmd)
}
