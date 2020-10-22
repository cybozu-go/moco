package agent

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/metrics"
	"github.com/cybozu-go/well"
)

// Clone executes MySQL CLONE command
func (a *Agent) Clone(w http.ResponseWriter, r *http.Request) {
	var err error

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	token := r.URL.Query().Get(moco.AgentTokenParam)
	if token != a.token {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var donorHostName string
	var donorPort int
	var donorUser string
	var donorPassword string

	external := r.URL.Query().Get(moco.CloneParamExternal)
	if external == "" {
		donorHostName = r.URL.Query().Get(moco.CloneParamDonorHostName)
		if len(donorHostName) <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		rawDonorPort := r.URL.Query().Get(moco.CloneParamDonorPort)
		if rawDonorPort == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		donorPort, err = strconv.Atoi(rawDonorPort)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		donorUser = moco.DonorUser
		donorPassword = a.donorUserPassword
	} else {
		rawDonorHostName, err := ioutil.ReadFile(moco.ReplicationSourceSecretPath + "/" + moco.ReplicationSourcePrimaryHostKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		donorHostName = string(rawDonorHostName)

		rawDonorPort, err := ioutil.ReadFile(moco.ReplicationSourceSecretPath + "/" + moco.ReplicationSourcePrimaryHostKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		donorPort, err = strconv.Atoi(string(rawDonorPort))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		rawDonorUser, err := ioutil.ReadFile(moco.ReplicationSourceSecretPath + "/" + moco.ReplicationSourcePrimaryUserKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		donorUser = string(rawDonorUser)

		rawDonorPassword, err := ioutil.ReadFile(moco.ReplicationSourceSecretPath + "/" + moco.ReplicationSourcePrimaryPasswordKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		donorPassword = string(rawDonorPassword)
	}

	if !a.sem.TryAcquire(1) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	db, err := a.acc.Get(fmt.Sprintf("%s:%d", a.mysqlAdminHostname, a.mysqlAdminPort), moco.MiscUser, a.miscUserPassword)
	if err != nil {
		a.sem.Release(1)
		internalServerError(w, fmt.Errorf("failed to get database: %w", err))
		log.Error("failed to get database", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return
	}

	primaryStatus, err := accessor.GetMySQLPrimaryStatus(r.Context(), db)
	if err != nil {
		a.sem.Release(1)
		internalServerError(w, fmt.Errorf("failed to get MySQL primary status: %w", err))
		log.Error("failed to get MySQL primary status", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return
	}

	gtid := primaryStatus.ExecutedGtidSet
	if gtid != "" {
		a.sem.Release(1)
		w.WriteHeader(http.StatusForbidden)
		log.Error("recipient is not empty", map[string]interface{}{
			"gtid": gtid,
		})
		return
	}

	well.Go(func(ctx context.Context) error {
		defer a.sem.Release(1)
		a.clone(ctx, a.miscUserPassword, donorUser, donorPassword, donorHostName, donorPort)
		return nil
	})
}

func (a *Agent) clone(ctx context.Context, miscPassword, donorUser, donorPassword, donorHostName string, donorPort int) {
	db, err := a.acc.Get(fmt.Sprintf("%s:%d", a.mysqlAdminHostname, a.mysqlAdminPort), moco.MiscUser, miscPassword)
	if err != nil {
		log.Error("failed to get database", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return
	}

	metrics.IncrementCloneCountMetrics()

	startTime := time.Now()
	_, err = db.ExecContext(ctx, `CLONE INSTANCE FROM ?@?:? IDENTIFIED BY ?`, donorUser, donorHostName, donorPort, donorPassword)
	durationSeconds := time.Since(startTime).Seconds()

	if err != nil {
		if strings.HasPrefix(err.Error(), "Error 3707") {
			metrics.UpdateCloneDurationSecondsMetrics(durationSeconds)

			log.Info("success to exec mysql CLONE", map[string]interface{}{
				"donor_hostname": donorHostName,
				"donor_port":     donorPort,
				"hostname":       a.mysqlAdminHostname,
				"port":           a.mysqlAdminPort,
				log.FnError:      err,
			})
			return
		}

		metrics.IncrementCloneFailureCountMetrics()

		log.Error("failed to exec mysql CLONE", map[string]interface{}{
			"donor_hostname": donorHostName,
			"donor_port":     donorPort,
			"hostname":       a.mysqlAdminHostname,
			"port":           a.mysqlAdminPort,
			log.FnError:      err,
		})
		return
	}

	metrics.UpdateCloneDurationSecondsMetrics(durationSeconds)

	log.Info("success to exec mysql CLONE", map[string]interface{}{
		"donor_hostname": donorHostName,
		"donor_port":     donorPort,
		"hostname":       a.mysqlAdminHostname,
		"port":           a.mysqlAdminPort,
	})
}
