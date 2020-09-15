package agent

import (
	"time"

	"github.com/cybozu-go/moco/accessor"
	"golang.org/x/sync/semaphore"
)

const maxCloneWorkers = 1

func NewAgent() *Agent {
	return &Agent{
		sem: semaphore.NewWeighted(int64(maxCloneWorkers)),
		acc: accessor.NewMySQLAccessor(&accessor.MySQLAccessorConfig{
			ConnMaxLifeTime:   30 * time.Minute,
			ConnectionTimeout: 3 * time.Second,
			ReadTimeout:       30 * time.Second,
		}),
	}
}

type Agent struct {
	sem *semaphore.Weighted
	acc *accessor.MySQLAccessor
}
