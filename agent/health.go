package agent

import (
	"fmt"
	"net/http"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
)

// Health returns the health check result of own MySQL
func (a *Agent) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	db, err := a.acc.Get(fmt.Sprintf("%s:%d", a.mysqlAdminHostname, a.mysqlAdminPort), moco.MiscUser, a.miscUserPassword)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to connect to database before health check: %w", err))
		log.Error("failed to connect to database before health check", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return
	}

	replicaStatus, err := accessor.GetMySQLReplicaStatus(r.Context(), db)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to get replica status: %w", err))
		log.Error("failed to get replica status", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return
	}

	if replicaStatus != nil && replicaStatus.LastIoErrno != 0 {
		internalServerError(w, fmt.Errorf("replica is out of sync: %d", replicaStatus.LastIoErrno))
		return
	}

	cloneStatus, err := accessor.GetMySQLCloneStateStatus(r.Context(), db)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to get clone status: %w", err))
		log.Error("failed to get clone status", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return
	}

	if cloneStatus.State.Valid && cloneStatus.State.String != moco.CloneStatusCompleted {
		internalServerError(w, fmt.Errorf("clone is processing: %s", cloneStatus.State.String))
		return
	}

	w.WriteHeader(http.StatusOK)
}
