package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

var credentialConfig struct {
	user   string
	format string
}

// credentialCmd represents the credential command
var credentialCmd = &cobra.Command{
	Use:   "credential <CLUSTER_NAME>",
	Short: "Fetch the credential of a specified user",
	Long:  "Fetch the credential of a specified user.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fetchCredential(cmd.Context(), args[0])
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
	fs.StringVarP(&credentialConfig.user, "mysql-user", "u", "moco-readonly", "User for login to mysql [`moco-writable` or `moco-readonly`]")
	fs.StringVar(&credentialConfig.format, "format", "plain", "The format of output [`plain` or `myconf`]")

	rootCmd.AddCommand(credentialCmd)
}
