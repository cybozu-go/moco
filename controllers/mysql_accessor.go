package controllers

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"

	// MySQL Driver
	"github.com/go-sql-driver/mysql"
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
	mu     sync.Mutex
	dbs    map[string]*sqlx.DB
}

// NewMySQLAccessor creates new MySQLAccessor
func NewMySQLAccessor(config *MySQLAccessorConfig) *MySQLAccessor {
	return &MySQLAccessor{
		config: config,
		mu:     sync.Mutex{},
		dbs:    make(map[string]*sqlx.DB),
	}
}

// Get connects a database with specified parameters
func (acc *MySQLAccessor) Get(host, user, password string) (*sqlx.DB, error) {
	uri := acc.getURI(host, user, password)
	fmt.Println("uri = " + uri)

	acc.mu.Lock()
	defer acc.mu.Unlock()

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
	conf := mysql.NewConfig()
	conf.User = user
	conf.Passwd = password
	conf.Net = "tcp"
	conf.Addr = host + ":" + strconv.Itoa(moco.MySQLAdminPort)
	conf.Timeout = acc.config.ConnectionTimeout
	conf.ReadTimeout = acc.config.ReadTimeout
	conf.InterpolateParams = true

	return conf.FormatDSN()
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
func (acc *MySQLAccessor) Cleanup(clusters []*mocov1alpha1.MySQLCluster) {
	// TODO construct uri to close acc.dbs
	var uriPostfixs []string
	for _, c := range clusters {
		uriPostfixs = append(uriPostfixs, uniqueName(cluster) 
	}

	acc.mu.Lock()
	defer acc.mu.Unlock()

	for uri, db := range acc.dbs {
		err := db.Ping()
		if err != nil {
			db.Close()
			delete(acc.dbs, uri)
		}
	}
}
