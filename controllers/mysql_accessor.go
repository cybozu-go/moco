package controllers

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type MySQLAccessorConfig struct {
	ConnMaxLifeTime   time.Duration
	ConnectionTimeout time.Duration
	ReadTimeout       time.Duration
	// WriteTimeout      *time.Duration ... unused
}

type mySQLAccessor struct {
	config *MySQLAccessorConfig
	dbs    map[string]*sql.DB
	mutex  *sync.Mutex
}

func NewMySQLAccessor(config *MySQLAccessorConfig) *mySQLAccessor {
	return &mySQLAccessor{
		config: config,
		dbs:    make(map[string]*sql.DB),
		mutex:  &sync.Mutex{},
	}
}

func (acc *mySQLAccessor) Get(uri string) (*sql.DB, error) {
	// TODO uri
	acc.mutex.Lock()
	defer acc.mutex.Unlock()

	if _, exists := acc.dbs[uri]; !exists {
		if db, err := acc.connect(uri); err == nil {
			acc.dbs[uri] = db
		} else {
			return db, err
		}
	}

	return acc.dbs[uri], nil
}

func (acc *mySQLAccessor) getURI(host string) string {
	return fmt.Sprintf("%s?timeout=%.0fs&readTimeout=%.0fs", host, acc.config.ConnectionTimeout.Seconds(), acc.config.ReadTimeout.Seconds())
}

func (acc *mySQLAccessor) connect(uri string) (*sql.DB, error) {
	db, err := sql.Open("mysql", uri)
	if err != nil {
		return nil, err
	}

	db.SetConnMaxLifetime(acc.config.ConnMaxLifeTime)

	return db, nil
}

// TODO run on background
func (acc *mySQLAccessor) Cleanup() {
	acc.mutex.Lock()
	defer acc.mutex.Unlock()

	for uri, db := range acc.dbs {
		err := db.Ping()
		if err != nil {
			delete(acc.dbs, uri)
		}
	}
}
