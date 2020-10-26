package agent

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/initialize"
)

var (
	passwordFilePath = filepath.Join(moco.TmpPath, "moco-root-password")
	miscConfPath     = filepath.Join(moco.MySQLDataPath, "misc.cnf")
)

// InitAfterClone executes initialization after clone
func (a *Agent) InitAfterClone(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	token := r.URL.Query().Get(moco.AgentTokenParam)
	if token != a.token {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !a.sem.TryAcquire(1) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	defer a.sem.Release(1)

	rawInitUser, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourceInitAfterCloneUserKey)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	initUser := string(rawInitUser)

	rawInitPassword, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourceInitAfterClonePasswordKey)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	initPassword := string(rawInitPassword)

	err = initialize.RestoreUsers(ctx, passwordFilePath, miscConfPath, initUser, &initPassword, os.Getenv(moco.PodIPEnvName))
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to initialize after clone: %w", err))
		log.Error("failed to initialize after clone", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return
	}
}
