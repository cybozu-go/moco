package cmd

import (
	"github.com/spf13/cobra"
)

// credentialCmd represents the credential command
var credentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "",
	Long:  "",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

func init() {
	rootCmd.AddCommand(credentialCmd)
}
