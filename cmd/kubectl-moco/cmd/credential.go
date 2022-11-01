package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var credentialConfig struct {
	user   string
	format string
}

// credentialCmd represents the credential command
var credentialCmd = &cobra.Command{
	Use:   "credential CLUSTER_NAME",
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

func init() {
	fs := credentialCmd.Flags()
	fs.StringVarP(&credentialConfig.user, "mysql-user", "u", "moco-readonly", "User for login to mysql")
	fs.StringVar(&credentialConfig.format, "format", "plain", "The format of output [`plain` or `mycnf`]")

	_ = credentialCmd.RegisterFlagCompletionFunc("mysql-user", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"moco-readonly", "moco-writable", "moco-admin"}, cobra.ShellCompDirectiveDefault
	})
	_ = credentialCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"plain", "mycnf"}, cobra.ShellCompDirectiveDefault
	})

	rootCmd.AddCommand(credentialCmd)
}
