package cmd

import (
	"fmt"

	"github.com/cybozu-go/moco/backup"
	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
)

var backupCmd = &cobra.Command{
	Use:   "backup BUCKET NAMESPACE NAME",
	Short: "backup a MySQLCluster's data to an object storage bucket",
	Long: `Backup a MySQLCluster's data.

BUCKET:    The bucket name.
NAMESPACE: The namespace of the MySQLCluster.
NAME:      The name of the MySQLCluster.`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucketName := args[0]
		namespace := args[1]
		name := args[2]

		b, err := makeBucket(bucketName)
		if err != nil {
			return fmt.Errorf("failed to create a bucket interface: %w", err)
		}

		cfg, err := ctrl.GetConfig()
		if err != nil {
			return fmt.Errorf("failed to get config for Kubernetes: %w", err)
		}

		bm, err := backup.NewBackupManager(cfg, b, commonArgs.workDir, namespace, name, mysqlPassword, commonArgs.threads)
		if err != nil {
			return fmt.Errorf("failed to create a backup manager: %w", err)
		}
		return bm.Backup(cmd.Context())
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
}
