package cmd

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	rootCmd = &cobra.Command{
		Use:   "entrypoint",
		Short: "Entrypoint for MySQL instances managed by MySO",
		Long:  `Entrypoint for MySQL instances managed by MySO.`,
	}
)

// Execute executes the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.AddCommand(initCmd)
}

func initConfig() {
	viper.SetEnvPrefix("mysql")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
}
