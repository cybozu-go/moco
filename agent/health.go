package agent

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
)

func (a *Agent) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	buf, err := ioutil.ReadFile(moco.MiscPasswordPath)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to read misc passsword file: %w", err))
		return
	}

	miscPassword := strings.TrimSpace(string(buf))
	db, err := a.acc.Get(fmt.Sprintf("%s:%d", a.mysqlAdminHostname, moco.MySQLAdminPort), moco.MiscUser, miscPassword)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to get database: %w", err))
		log.Error("failed to get database", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      moco.MySQLAdminPort,
			log.FnError: err,
		})
		return
	}

	cloneStatus, err := accessor.GetMySQLCloneStateStatus(r.Context(), db)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to get clone status: %w", err))
		log.Error("failed to get clone status", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      moco.MySQLAdminPort,
			log.FnError: err,
		})
		return
	}

	if cloneStatus.State.Valid && cloneStatus.State.String != moco.CloneStatusCompleted {
		internalServerError(w, fmt.Errorf("clone is processing: %s", cloneStatus.State.String))
		return
	}

	w.WriteHeader(http.StatusOK)
	return
}
