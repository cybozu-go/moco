package controllers

import (
	"fmt"
	"sync"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/jmoiron/sqlx"

	// MySQL Driver
	_ "github.com/go-sql-driver/mysql"
)

// MySQLAccessorConfig contains MySQL connection configurations
type MySQLAccessorConfig struct {
	ConnMaxLifeTime   time.Duration
	ConnectionTimeout time.Duration
	ReadTimeout       time.Duration
}

// MySQLAccessor contains MySQL connection configurations and sqlx.db
type MySQLAccessor struct {
	config *MySQLAccessorConfig
	dbs    map[string]*sqlx.DB
	mutex  *sync.Mutex
}

// NewMySQLAccessor creates new MySQLAccessor
func NewMySQLAccessor(config *MySQLAccessorConfig) *MySQLAccessor {
	return &MySQLAccessor{
		config: config,
		dbs:    make(map[string]*sqlx.DB),
		mutex:  &sync.Mutex{},
	}
}

// Get connects a database with specified parameters
func (acc *MySQLAccessor) Get(host, user, password string) (*sqlx.DB, error) {
	uri := acc.getURI(host, user, password)
	fmt.Println("uri = " + uri)

	acc.mutex.Lock()
	defer acc.mutex.Unlock()

	if _, exists := acc.dbs[uri]; !exists {
		if db, err := acc.connect(uri); err == nil {
			acc.dbs[uri] = db
		} else {
			return nil, err
		}
	}

	db := acc.dbs[uri]
	err := db.Ping()
	if err != nil {
		delete(acc.dbs, uri)
		return nil, err
	}
	return db, nil
}

func (acc *MySQLAccessor) getURI(host, user, password string) string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/?timeout=%.0fs&readTimeout=%.0fs", user, password, host,
		moco.MySQLAdminPort, acc.config.ConnectionTimeout.Seconds(), acc.config.ReadTimeout.Seconds())
}

func (acc *MySQLAccessor) connect(uri string) (*sqlx.DB, error) {
	db, err := sqlx.Connect("mysql", uri)
	if err != nil {
		return nil, err
	}

	db.SetConnMaxLifetime(acc.config.ConnMaxLifeTime)

	return db, nil
}

// Cleanup cleans staled connections
// TODO run on background
func (acc *MySQLAccessor) Cleanup() {
	acc.mutex.Lock()
	defer acc.mutex.Unlock()

	for uri, db := range acc.dbs {
		err := db.Ping()
		if err != nil {
			db.Close()
			delete(acc.dbs, uri)
		}
	}
}
