package agent

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/metrics"
)

// RotateLog rotates log files
func (a *Agent) RotateLog(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get(moco.AgentTokenParam)
	if token != a.token {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	metrics.IncrementLogRotationCountMetrics()
	startTime := time.Now()

	errFile := filepath.Join(a.logDir, moco.MySQLErrorLogName)
	_, err := os.Stat(errFile)
	if err == nil {
		err := os.Rename(errFile, errFile+".0")
		if err != nil {
			internalServerError(w, fmt.Errorf("failed to rotate err log file: %w", err))
			log.Error("failed to rotate err log file", map[string]interface{}{
				log.FnError: err,
			})
			metrics.IncrementLogRotationFailureCountMetrics()
			return
		}
	} else if !os.IsNotExist(err) {
		internalServerError(w, fmt.Errorf("failed to stat err log file: %w", err))
		log.Error("failed to stat err log file", map[string]interface{}{
			log.FnError: err,
		})
		metrics.IncrementLogRotationFailureCountMetrics()
		return
	}

	slowFile := filepath.Join(a.logDir, moco.MySQLSlowLogName)
	_, err = os.Stat(slowFile)
	if err == nil {
		err := os.Rename(slowFile, slowFile+".0")
		if err != nil {
			internalServerError(w, fmt.Errorf("failed to rotate slow query log file: %w", err))
			log.Error("failed to rotate slow query log file", map[string]interface{}{
				log.FnError: err,
			})
			metrics.IncrementLogRotationFailureCountMetrics()
			return
		}
	} else if !os.IsNotExist(err) {
		internalServerError(w, fmt.Errorf("failed to stat slow query log file: %w", err))
		log.Error("failed to stat slow query log file", map[string]interface{}{
			log.FnError: err,
		})
		metrics.IncrementLogRotationFailureCountMetrics()
		return
	}

	podName := os.Getenv(moco.PodNameEnvName)

	db, err := a.acc.Get(fmt.Sprintf("%s:%d", podName, a.mysqlAdminPort), moco.MiscUser, a.miscUserPassword)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to get database: %w", err))
		log.Error("failed to get database", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		metrics.IncrementLogRotationFailureCountMetrics()
		return
	}

	if _, err := db.ExecContext(r.Context(), "FLUSH LOCAL ERROR LOGS, SLOW LOGS"); err != nil {
		internalServerError(w, fmt.Errorf("failed to exec mysql FLUSH: %w", err))
		log.Error("failed to exec mysql FLUSH", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		metrics.IncrementLogRotationFailureCountMetrics()
		return
	}

	durationSeconds := time.Since(startTime).Seconds()
	metrics.UpdateLogRotationDurationSecondsMetrics(durationSeconds)
}
