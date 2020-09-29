package agent

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
)

// RotateLog rotes log files
func (a *Agent) RotateLog(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get(moco.AgentTokenParam)
	if token != a.token {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	errFile := filepath.Join(a.logDir, moco.MySQLErrorLogName)
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

	slowFile := filepath.Join(a.logDir, moco.MySQLSlowLogName)
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

	podName := os.Getenv(moco.PodNameEnvName)

	db, err := a.acc.Get(fmt.Sprintf("%s:%d", podName, a.mysqlAdminPort), moco.MiscUser, a.miscUserPassword)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to get database: %w", err))
		return
	}

	if _, err := db.ExecContext(r.Context(), "FLUSH LOCAL ERROR LOGS, SLOW LOGS"); err != nil {
		internalServerError(w, fmt.Errorf("failed to exec mysql FLUSH: %w", err))
		return
	}
}
