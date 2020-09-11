package agent

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
)

func internalServerError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func RotateLog(w http.ResponseWriter, r *http.Request) {
	errFile := filepath.Join(moco.VarLogPath, moco.MySQLErrorLogName)
	_, err := os.Stat(errFile)
	if err == nil {
		err := os.Rename(errFile, errFile+".0")
		if err != nil {
			log.Error("failed to rotate err log file", map[string]interface{}{
				log.FnError: err,
			})
			internalServerError(w, fmt.Errorf("failed to rotate err log file: %w", err))
			return
		}
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat err log file", map[string]interface{}{
			log.FnError: err,
		})
		internalServerError(w, fmt.Errorf("failed to stat err log file: %w", err))
		return
	}

	slowFile := filepath.Join(moco.VarLogPath, moco.MySQLSlowLogName)
	_, err = os.Stat(slowFile)
	if err == nil {
		err := os.Rename(slowFile, slowFile+".0")
		if err != nil {
			log.Error("failed to rotate slow query log file", map[string]interface{}{
				log.FnError: err,
			})
			internalServerError(w, fmt.Errorf("failed to rotate slow query log file: %w", err))
			return
		}
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat slow query log file", map[string]interface{}{
			log.FnError: err,
		})
		internalServerError(w, fmt.Errorf("failed to stat slow query log file: %w", err))
		return
	}

	cmd := well.CommandContext(r.Context(), "mysql", "--defaults-extra-file="+filepath.Join(moco.MySQLDataPath, "misc.cnf"))
	cmd.Stdin = strings.NewReader("FLUSH LOCAL ERROR LOGS;\nFLUSH LOCAL SLOW LOGS;\n")
	err = cmd.Run()
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to exec mysql FLUSH: %w", err))
		return
	}
}
