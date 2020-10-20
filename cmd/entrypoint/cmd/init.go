package cmd

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/initialize"
	"github.com/cybozu-go/well"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	initOnceCompletedPath = filepath.Join(moco.MySQLDataPath, "init-once-completed")
	passwordFilePath      = filepath.Join(moco.TmpPath, "moco-root-password")
	miscConfPath          = filepath.Join(moco.MySQLDataPath, "misc.cnf")
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize MySQL instance",
	Long: fmt.Sprintf(`Initialize MySQL instance managed by MOCO.
	If %s already exists, this command does nothing.
	`, initOnceCompletedPath),
	RunE: func(cmd *cobra.Command, args []string) error {
		well.Go(func(ctx context.Context) error {
			log.Info("start initialization", nil)
			err := initialize.InitializeOnce(ctx, initOnceCompletedPath, passwordFilePath, miscConfPath)
			if err != nil {
				f, err := ioutil.ReadFile("/var/log/mysql/mysql.err")
				if err != nil {
					return err
				}

				fmt.Println(string(f))
				return err
			}

			// Put preparation steps which should be executed at every startup.

			return nil
		})

		well.Stop()
		err := well.Wait()
		if err != nil {
			log.ErrorExit(err)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().String(moco.PodNameFlag, "", "Pod Name created by StatefulSet")
	initCmd.Flags().String(moco.PodIPFlag, "", "Pod IP address")
	err := viper.BindPFlags(initCmd.Flags())
	if err != nil {
		panic(err)
	}
}
