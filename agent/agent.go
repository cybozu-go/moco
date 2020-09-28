package agent

import (
	"github.com/cybozu-go/moco/accessor"
	"golang.org/x/sync/semaphore"
)

const maxCloneWorkers = 1

func New(podName, token string, config *accessor.MySQLAccessorConfig) *Agent {
	return &Agent{
		sem:                semaphore.NewWeighted(int64(maxCloneWorkers)),
		acc:                accessor.NewMySQLAccessor(config),
		mysqlAdminHostname: podName,
		token:              token,
	}
}

type Agent struct {
	sem                *semaphore.Weighted
	acc                *accessor.MySQLAccessor
	mysqlAdminHostname string
	token              string
}
