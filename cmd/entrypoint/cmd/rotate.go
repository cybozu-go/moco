package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	addressFlag = "address"
)

var rotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Rotate MySQL log files",
	Long:  `Rotate MySQL log files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		serv := &well.HTTPServer{
			Server: &http.Server{
				Addr:    viper.GetString(addressFlag),
				Handler: http.HandlerFunc(rotateLog),
			},
		}

		err := serv.ListenAndServe()
		if err != nil {
			return err
		}
		err = well.Wait()

		if err != nil && !well.IsSignaled(err) {
			return err
		}

		return nil
	},
}

func rotateLog(w http.ResponseWriter, r *http.Request) {
	errFile := filepath.Join(moco.VarLogPath, "mysql.err")
	_, err := os.Stat(errFile)
	if err == nil {
		os.Rename(errFile, errFile+".0")
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat err log file", map[string]interface{}{
			log.FnError: err,
		})
	}

	slowFile := filepath.Join(moco.VarLogPath, "mysql.slow")
	_, err = os.Stat(errFile)
	if err == nil {
		os.Rename(slowFile, slowFile+".0")
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat slow query log file", map[string]interface{}{
			log.FnError: err,
		})
	}

	cmd := well.CommandContext(r.Context(), "mysql", "--defaults-file", filepath.Join(moco.MySQLConfPath, moco.MySQLConfName), "-u", moco.OperatorUser, "-p"+viper.GetString(moco.OperatorPasswordFlag))
	cmd.Stdin = strings.NewReader("FLUSH ERROR LOGS\nFLUSH SLOW LOGS\n")
	err = cmd.Run()
	if err != nil {
		log.Error("failed to exec mysql FLUSH", map[string]interface{}{
			log.FnError: err,
		})
	}

	return
}

func init() {
	rootCmd.AddCommand(rotateCmd)

	rotateCmd.Flags().String(addressFlag, ":8080", "Listening address and port.")
	rotateCmd.Flags().String(moco.OperatorPasswordFlag, "", "Password for operator user")

	err := viper.BindPFlags(rotateCmd.Flags())
	if err != nil {
		panic(err)
	}
}
