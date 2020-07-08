package cmd

import (
	"fmt"
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

var rotateServerCmd = &cobra.Command{
	Use:   "rotate-server",
	Short: "Start HTTP server to rotate MySQL log files",
	Long:  `Start HTTP server to rotate MySQL log files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		serv := &well.HTTPServer{
			Server: &http.Server{
				Addr:    viper.GetString(addressFlag),
				Handler: http.HandlerFunc(handler),
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

func handler(w http.ResponseWriter, r *http.Request) {
	err := rotateLog(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func rotateLog(w http.ResponseWriter, r *http.Request) error {
	errFile := filepath.Join(moco.VarLogPath, moco.MySQLErrorLogName)
	_, err := os.Stat(errFile)
	if err == nil {
		err := os.Rename(errFile, errFile+".0")
		if err != nil {
			log.Error("failed to rotate err log file", map[string]interface{}{
				log.FnError: err,
			})
			return fmt.Errorf("failed to rotate err log file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat err log file", map[string]interface{}{
			log.FnError: err,
		})
		return fmt.Errorf("failed to stat err log file: %w", err)
	}

	slowFile := filepath.Join(moco.VarLogPath, moco.MySQLSlowLogName)
	_, err = os.Stat(slowFile)
	if err == nil {
		err := os.Rename(slowFile, slowFile+".0")
		if err != nil {
			log.Error("failed to rotate slow query log file", map[string]interface{}{
				log.FnError: err,
			})
			return fmt.Errorf("failed to rotate slow query log file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat slow query log file", map[string]interface{}{
			log.FnError: err,
		})
		return fmt.Errorf("failed to stat slow query log file: %w", err)
	}

	cmd := well.CommandContext(r.Context(), "mysql", "--defaults-extra-file="+filepath.Join(moco.MySQLDataPath, "misc.cnf"))
	cmd.Stdin = strings.NewReader("FLUSH ERROR LOGS;\nFLUSH SLOW LOGS;\n")
	err = cmd.Run()
	if err != nil {
		log.Error("failed to exec mysql FLUSH", map[string]interface{}{
			log.FnError: err,
		})
		return fmt.Errorf("failed to exec mysql FLUSH: %w", err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(rotateServerCmd)

	rotateServerCmd.Flags().String(addressFlag, ":8080", "Listening address and port.")

	err := viper.BindPFlags(rotateServerCmd.Flags())
	if err != nil {
		panic(err)
	}
}
