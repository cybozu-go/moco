package agent

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
	"golang.org/x/sync/semaphore"
	"k8s.io/apimachinery/pkg/util/validation"
)

const maxCloneWorkers = 1

func NewCloneAgent() *CloneAgent {
	return &CloneAgent{
		sem: semaphore.NewWeighted(int64(maxCloneWorkers)),
	}
}

type CloneAgent struct {
	sem *semaphore.Weighted
}

func (a *CloneAgent) Clone(w http.ResponseWriter, r *http.Request) {
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
	password := strings.TrimSpace(string(buf))

	if !a.sem.TryAcquire(1) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	go func() {
		defer a.sem.Release(1)
		clone(r.Context(), password, donorHostName, donorPort)
	}()
}

func clone(ctx context.Context, password, donorHostName string, donorPort int) {
	cmd := well.CommandContext(ctx, "mysql", "--defaults-extra-file="+filepath.Join(moco.MySQLDataPath, "misc.cnf"))
	query := fmt.Sprintf("CLONE INSTANCE FROM '%s'@'%s':%d IDENTIFIED BY '%s';\n", moco.DonorUser, donorHostName, donorPort, password)
	cmd.Stdin = strings.NewReader(query)
	err := cmd.Run()
	if err != nil {
		log.Error("failed to exec mysql CLONE", map[string]interface{}{
			log.FnError: err,
		})
		return
	}
}
