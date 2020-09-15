package agent

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/well"
	"k8s.io/apimachinery/pkg/util/validation"
)

func (a *Agent) Clone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var (
		donorHostName = r.URL.Query().Get(moco.CloneParamDonorHostName)
		rawDonorPort  = r.URL.Query().Get(moco.CloneParamDonorPort)
	)

	if len(validation.IsFullyQualifiedDomainName(nil, donorHostName)) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var donorPort int
	if rawDonorPort == "" {
		donorPort = moco.MySQLPort
	} else {
		var err error
		donorPort, err = strconv.Atoi(rawDonorPort)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	buf, err := ioutil.ReadFile(moco.DonorPasswordPath)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to read donor passsword file: %w", err))
		return
	}
	donorPassword := strings.TrimSpace(string(buf))

	buf, err = ioutil.ReadFile(moco.MiscPasswordPath)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to read misc passsword file: %w", err))
		return
	}
	miscPassword := strings.TrimSpace(string(buf))

	if !a.sem.TryAcquire(1) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	db, err := a.acc.Get(fmt.Sprintf("localhost:%d", moco.MySQLAdminPort), moco.MiscUser, miscPassword)
	if err != nil {
		a.sem.Release(1)
		internalServerError(w, fmt.Errorf("failed to get database: %w", err))
		return
	}

	primaryStatus, err := accessor.GetMySQLPrimaryStatus(r.Context(), db)
	if err != nil {
		a.sem.Release(1)
		internalServerError(w, fmt.Errorf("failed to get MySQL primary status: %w", err))
		return
	}

	if primaryStatus.ExecutedGtidSet != "" {
		a.sem.Release(1)
		w.WriteHeader(http.StatusForbidden)
		log.Error("recipient is not empty", map[string]interface{}{
			"hostname": donorHostName,
			"port":     donorPort,
		})
		return
	}

	well.Go(func(ctx context.Context) error {
		defer a.sem.Release(1)
		a.clone(ctx, miscPassword, donorPassword, donorHostName, donorPort)
		return nil
	})
}

func (a *Agent) clone(ctx context.Context, miscPassword, donorPassword, donorHostName string, donorPort int) {
	db, err := a.acc.Get(fmt.Sprintf("localhost:%d", moco.MySQLAdminPort), moco.MiscUser, miscPassword)
	if err != nil {
		log.Error("failed to get database", map[string]interface{}{
			"hostname":  donorHostName,
			"port":      donorPort,
			log.FnError: err,
		})
		return
	}

	if _, err := db.ExecContext(ctx, `CLONE INSTANCE FROM ?@?:? IDENTIFIED BY ?`, moco.DonorUser, donorHostName, donorPort, donorPassword); err != nil {
		if strings.HasPrefix(err.Error(), "ERROR 3707") {
			log.Info("success to exec mysql CLONE", map[string]interface{}{"hostname": donorHostName, "port": donorPort, log.FnError: err})
			return
		}

		log.Error("failed to exec mysql CLONE", map[string]interface{}{
			"hostname":  donorHostName,
			"port":      donorPort,
			log.FnError: err,
		})
		return
	}
	log.Info("success to exec mysql CLONE", map[string]interface{}{"hostname": donorHostName, "port": donorPort})
}
