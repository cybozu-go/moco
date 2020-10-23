package agent

import (
	"github.com/cybozu-go/moco/accessor"
	"golang.org/x/sync/semaphore"
)

const maxCloneWorkers = 1

// New returns a Agent
func New(podName, token, miscUserPassword, donorUserPassword, replicationSourceSecretPath, logDir string, mysqlAdminPort int, config *accessor.MySQLAccessorConfig) *Agent {
	return &Agent{
		sem:                         semaphore.NewWeighted(int64(maxCloneWorkers)),
		acc:                         accessor.NewMySQLAccessor(config),
		mysqlAdminHostname:          podName,
		mysqlAdminPort:              mysqlAdminPort,
		miscUserPassword:            miscUserPassword,
		donorUserPassword:           donorUserPassword,
		replicationSourceSecretPath: replicationSourceSecretPath,
		token:                       token,
		logDir:                      logDir,
	}
}

// Agent is the agent to executes some MySQL commands of the own Pod
type Agent struct {
	sem                         *semaphore.Weighted
	acc                         *accessor.MySQLAccessor
	mysqlAdminHostname          string
	mysqlAdminPort              int
	miscUserPassword            string
	donorUserPassword           string
	replicationSourceSecretPath string
	token                       string
	logDir                      string
}
