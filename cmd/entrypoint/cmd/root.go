package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	podIP                 string
	mysqlRootHost         string
	mysqlRootPassword     string
	mysqlOperatorPassword string

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
	viper.AutomaticEnv()

	rootCmd.PersistentFlags().StringVar(&podIP, "pod-ip", "", "IP address of the Pod")
	rootCmd.PersistentFlags().StringVar(&mysqlRootHost, "mysql-root-host", "", "Root host of MySQL instance")
	rootCmd.PersistentFlags().StringVar(&mysqlRootPassword, "mysql-root-password", "", "Password for root user")
	//rootCmd.MarkPersistentFlagRequired("mysql-root-password")
	rootCmd.PersistentFlags().StringVar(&mysqlOperatorPassword, "mysql-operator-password", "", "Password for operator user")
	rootCmd.AddCommand(initCmd)
}
