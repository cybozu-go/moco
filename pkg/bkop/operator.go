package bkop

import (
	"context"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// Operator is the interface to define backup and restore operations.
type Operator interface {
	// Ping checks the connectivity to the database server.
	Ping() error

	// Close ust be called when the operator is no longer in use.
	Close()

	// GetServerStatus fills ServerStatus struct.
	GetServerStatus(context.Context, *ServerStatus) error

	// DumpFull takes a full dump of the database instance.
	// `dir` should exist before calling this.
	DumpFull(ctx context.Context, dir string) error

	// GetBinlogs returns a list of binary log files on the mysql instance.
	GetBinlogs(context.Context) ([]string, error)

	// DumpBinLog dumps binary log files starting from `binlogName`.
	// Transactions in `filterGTID` will be excluded.
	// `dir` should exist before calling this.
	DumpBinlog(ctx context.Context, dir, binlogName, filterGTID string) error

	// PrepareRestore prepares the database instance for loading data.
	PrepareRestore(context.Context) error

	// LoadDump loads data dumped by `DumpFull`.
	LoadDump(ctx context.Context, dir string) error

	// LoadBinLog applies binary logs up to `restorePoint`.
	LoadBinlog(ctx context.Context, dir string, restorePoint time.Time) error

	// FinishRestore sets global variables of the database instance after restoration.
	FinishRestore(context.Context) error
}

type operator struct {
	db       *sqlx.DB
	host     string
	port     int
	user     string
	password string
	threads  int
}

var _ Operator = operator{}

// NewOperator creates an Operator.
func NewOperator(host string, port int, user, password string, threads int) (Operator, error) {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)
	cfg.InterpolateParams = true
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 1 * time.Minute
	db, err := sqlx.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", host, err)
	}
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(30 * time.Second)
	return operator{db, host, port, user, password, threads}, nil
}

func (o operator) Ping() error {
	return o.db.Ping()
}

func (o operator) Close() {
	o.db.Close()
}
