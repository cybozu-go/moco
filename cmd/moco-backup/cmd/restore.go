package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/cybozu-go/moco/backup"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
)

var restoreCmd = &cobra.Command{
	Use:   "restore BUCKET SOURCE_NAMESPACE SOURCE_NAME NAMESPACE NAME YYYYMMDD-hhmmss",
	Short: "restore MySQL data from a backup",
	Long: `Restore MySQL data from a backup.

BUCKET:           The bucket name.
SOURCE_NAMESPACE: The source MySQLCluster's namespace.
SOURCE_NAME:      The source MySQLCluster's name.
NAMESPACE:        The target MySQLCluster's namespace.
NAME:             The target MySQLCluster's name.
YYYYMMDD-hhmmss:  The point-in-time to restore data.  e.g. 20210523-150423`,
	Args: cobra.ExactArgs(6),
	RunE: func(cmd *cobra.Command, args []string) error {
		maxRetry := 3
		for i := 0; i < maxRetry; i++ {
			if err := runRestore(cmd, args); err != backup.ErrBadConnection {
				return err
			}

			fmt.Fprintf(os.Stderr, "bad connection: retrying...\n")
			time.Sleep(1 * time.Second)
		}

		return nil
	},
}

func runRestore(cmd *cobra.Command, args []string) (e error) {
	defer func() {
		if r := recover(); r != nil {
			if r == backup.ErrBadConnection {
				e = r.(error)
			} else {
				panic(r)
			}
		}
	}()

	bucketName := args[0]
	srcNamespace := args[1]
	srcName := args[2]
	namespace := args[3]
	name := args[4]

	restorePoint, err := time.Parse(constants.BackupTimeFormat, args[5])
	if err != nil {
		return fmt.Errorf("invalid restore point %s: %w", args[5], err)
	}

	b, err := makeBucket(bucketName)
	if err != nil {
		return fmt.Errorf("failed to create a bucket interface: %w", err)
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config for Kubernetes: %w", err)
	}

	rm, err := backup.NewRestoreManager(cfg, b, commonArgs.workDir,
		srcNamespace, srcName,
		namespace, name,
		mysqlPassword,
		commonArgs.threads,
		restorePoint)
	if err != nil {
		return fmt.Errorf("failed to create a restore manager: %w", err)
	}
	return rm.Restore(cmd.Context())
}

func init() {
	rootCmd.AddCommand(restoreCmd)
}
