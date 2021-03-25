package dbop

import (
	"context"
	"fmt"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

const (
	connTimeout = 5 * time.Second
	readTimeout = 1 * time.Minute
)

// Operator represents a set of operations for a MySQL instance.
type Operator interface {
	// Name is the name of the MySQL instance for which this operator works.
	Name() string

	// Close closes the underlying connections.
	Close() error

	// GetStatus reports the instance status.
	GetStatus(context.Context) (*MySQLInstanceStatus, error)

	// IsSubsetGTID returns true if set1 is a subset of set2.
	IsSubsetGTID(ctx context.Context, set1, set2 string) (bool, error)

	// FindTopRunner returns the index of the slice whose `GlobalVariables.ExecutedGtidSet`
	// is most advanced.  This may return ErrErrantTransactions for errant transactions
	// or ErrNoTopRunner if there is no such instance.
	FindTopRunner(context.Context, []*MySQLInstanceStatus) (int, error)

	// ConfigureReplica configures client-side replication.
	// If `symisync` is true, it enables client-side semi-synchronous replication.
	// In either case, it disables server-side semi-synchronous replication.
	ConfigureReplica(ctx context.Context, source AccessInfo, semisync bool) error

	// ConfigurePrimary configures server-side semi-synchronous replication.
	// For asynchronous replication, this method should not be called.
	ConfigurePrimary(ctx context.Context, waitForCount int) error

	// StopReplicaIOThread executes `STOP SLAVE IO_THREAD`.
	StopReplicaIOThread(context.Context) error

	// WaitForGTID waits for `mysqld` to execute all GTIDs in `gtidSet`.
	// If timeout happens, this return ErrTimeout.
	// If `timeoutSeconds` is zero, this will not timeout.
	WaitForGTID(ctx context.Context, gtidSet string, timeoutSeconds int) error

	// SetReadOnly makes the instance super_read_only if `true` is passed.
	// Otherwise, this stops the replication and makes the instance writable.
	SetReadOnly(context.Context, bool) error
}

// OperatorFactory represents the factory for Operators.
type OperatorFactory interface {
	New(*mocov1beta1.MySQLCluster, *password.MySQLPassword, int) Operator
	Cleanup()
}

// DefaultOperatorFactory is the default operator factory.
var DefaultOperatorFactory OperatorFactory = defaultFactory{}

type defaultFactory struct{}

func (defaultFactory) New(cluster *mocov1beta1.MySQLCluster, pwd *password.MySQLPassword, index int) Operator {
	podName := cluster.PodName(index)
	namespace := cluster.Namespace

	cfg := mysql.NewConfig()
	cfg.User = constants.AdminUser
	cfg.Passwd = pwd.Admin()
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s.%s.%s.svc:%d", podName, cluster.HeadlessServiceName(), namespace, constants.MySQLAdminPort)
	cfg.InterpolateParams = true
	cfg.ParseTime = true
	cfg.Timeout = connTimeout
	cfg.ReadTimeout = readTimeout
	db := sqlx.MustOpen("mysql", cfg.FormatDSN())
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(30 * time.Second)
	return &operator{
		cluster: cluster,
		passwd:  pwd,
		index:   index,
		db:      db,
	}
}

func (defaultFactory) Cleanup() {}

type operator struct {
	cluster *mocov1beta1.MySQLCluster
	passwd  *password.MySQLPassword
	index   int
	db      *sqlx.DB
}

var _ Operator = &operator{}

func (o *operator) Name() string {
	return fmt.Sprintf("%s/%s-%d", o.cluster.Namespace, o.cluster.Name, o.index)
}

func (o *operator) Close() error {
	if o.db == nil {
		return nil
	}
	if err := o.db.Close(); err != nil {
		return err
	}
	o.db = nil
	return nil
}
