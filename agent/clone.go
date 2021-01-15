package agent

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/initialize"
	"github.com/cybozu-go/moco/metrics"
	"github.com/cybozu-go/well"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

const timeoutDuration = 120 * time.Second

var (
	passwordFilePath = filepath.Join(moco.TmpPath, "moco-root-password")
	miscConfPath     = filepath.Join(moco.MySQLDataPath, "misc.cnf")
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

	var externalMode bool
	external := r.URL.Query().Get(moco.CloneParamExternal)
	switch external {
	case "true":
		externalMode = true
	case "":
		externalMode = false
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var donorHostName string
	var donorPort int
	var donorUser string
	var donorPassword string
	var initUser string
	var initPassword string
	if !externalMode {
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

		donorUser = moco.CloneDonorUser
		donorPassword = a.donorUserPassword
	} else {
		rawDonorHostName, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourcePrimaryHostKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		donorHostName = string(rawDonorHostName)

		rawDonorPort, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourcePrimaryPortKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		donorPort, err = strconv.Atoi(string(rawDonorPort))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		rawDonorUser, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourceCloneUserKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		donorUser = string(rawDonorUser)

		rawDonorPassword, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourceClonePasswordKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		donorPassword = string(rawDonorPassword)

		rawInitUser, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourceInitAfterCloneUserKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		initUser = string(rawInitUser)

		rawInitPassword, err := ioutil.ReadFile(a.replicationSourceSecretPath + "/" + moco.ReplicationSourceInitAfterClonePasswordKey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		initPassword = string(rawInitPassword)
	}

	if !a.sem.TryAcquire(1) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	db, err := a.acc.Get(fmt.Sprintf("%s:%d", a.mysqlAdminHostname, a.mysqlAdminPort), moco.MiscUser, a.miscUserPassword)
	if err != nil {
		a.sem.Release(1)
		internalServerError(w, fmt.Errorf("failed to connect to database before getting MySQL primary status: %w", err))
		log.Error("failed to connect to database before getting MySQL primary status", map[string]interface{}{
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

	env := well.NewEnvironment(context.Background())
	env.Go(func(ctx context.Context) error {
		defer a.sem.Release(1)
		err := a.clone(ctx, donorUser, donorPassword, donorHostName, donorPort)
		if err != nil {
			return err
		}

		if externalMode {
			err := waitBootstrap(ctx, initUser, initPassword)
			if err != nil {
				log.Error("mysqld didn't boot up after cloning from external", map[string]interface{}{
					"hostname":  a.mysqlAdminHostname,
					"port":      a.mysqlAdminPort,
					log.FnError: err,
				})
				return err
			}
			err = initialize.RestoreUsers(ctx, passwordFilePath, miscConfPath, initUser, &initPassword, os.Getenv(moco.PodIPEnvName))
			if err != nil {
				log.Error("failed to initialize after clone", map[string]interface{}{
					"hostname":  a.mysqlAdminHostname,
					"port":      a.mysqlAdminPort,
					log.FnError: err,
				})
				return err
			}
			err = initialize.ShutdownInstance(ctx, passwordFilePath)
			if err != nil {
				log.Error("failed to shutdown mysqld after clone", map[string]interface{}{
					"hostname":  a.mysqlAdminHostname,
					"port":      a.mysqlAdminPort,
					log.FnError: err,
				})
				return err
			}
		}
		return nil
	})
}

func (a *Agent) clone(ctx context.Context, donorUser, donorPassword, donorHostName string, donorPort int) error {
	db, err := a.acc.Get(fmt.Sprintf("%s:%d", a.mysqlAdminHostname, a.mysqlAdminPort), moco.MiscUser, a.miscUserPassword)
	if err != nil {
		log.Error("failed to connect to database before clone", map[string]interface{}{
			"hostname":  a.mysqlAdminHostname,
			"port":      a.mysqlAdminPort,
			log.FnError: err,
		})
		return err
	}

	metrics.IncrementCloneCountMetrics()

	startTime := time.Now()
	_, err = db.ExecContext(ctx, `CLONE INSTANCE FROM ?@?:? IDENTIFIED BY ?`, donorUser, donorHostName, donorPort, donorPassword)
	durationSeconds := time.Since(startTime).Seconds()

	// After cloning, the recipient MySQL server instance is restarted (stopped and started) automatically.
	// And then the "ERROR 3707" (Restart server failed) occurs. This error does not indicate a cloning failure.
	// So checking the error number here.
	if err != nil && !strings.HasPrefix(err.Error(), "Error 3707") {
		metrics.IncrementCloneFailureCountMetrics()

		log.Error("failed to exec mysql CLONE", map[string]interface{}{
			"donor_hostname": donorHostName,
			"donor_port":     donorPort,
			"hostname":       a.mysqlAdminHostname,
			"port":           a.mysqlAdminPort,
			log.FnError:      err,
		})
		return err
	}

	metrics.UpdateCloneDurationSecondsMetrics(durationSeconds)

	log.Info("success to exec mysql CLONE", map[string]interface{}{
		"donor_hostname": donorHostName,
		"donor_port":     donorPort,
		"hostname":       a.mysqlAdminHostname,
		"port":           a.mysqlAdminPort,
		log.FnError:      err,
	})
	return nil
}

func waitBootstrap(ctx context.Context, user, password string) error {
	conf := mysql.NewConfig()
	conf.User = user
	conf.Passwd = password
	conf.Net = "unix"
	conf.Addr = "/var/run/mysqld/mysqld.sock"
	conf.InterpolateParams = true
	uri := conf.FormatDSN()

	ctx, cancel := context.WithTimeout(ctx, timeoutDuration)
	defer cancel()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			_, err := sqlx.Connect("mysql", uri)
			if err == nil {
				return nil
			}
		}
	}
}
