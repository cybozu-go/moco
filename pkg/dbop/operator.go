package dbop

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"

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

	// KillConnections kills all connections except for ones from `localhost`
	// and ones for MOCO.
	KillConnections(context.Context) error
}

// OperatorFactory represents the factory for Operators.
type OperatorFactory interface {
	New(context.Context, *mocov1beta2.MySQLCluster, *password.MySQLPassword, int) (Operator, error)
	Cleanup()
}

type Resolver interface {
	Resolve(context.Context, *mocov1beta2.MySQLCluster, int) (string, error)
}

type defaultFactory struct {
	r Resolver
}

var _ OperatorFactory = defaultFactory{}

// NewFactory returns a new OperatorFactory that resolves instance IP address using `r`.
// If `r.Resolve` returns an error, the `New` method will return a NopOperator.
func NewFactory(r Resolver) OperatorFactory {
	return defaultFactory{r: r}
}

func (f defaultFactory) New(ctx context.Context, cluster *mocov1beta2.MySQLCluster, pwd *password.MySQLPassword, index int) (Operator, error) {
	addr, err := f.r.Resolve(ctx, cluster, index)
	if err != nil {
		return NopOperator{name: fmt.Sprintf("%s/%s", cluster.Namespace, cluster.PodName(index))}, nil
	}

	cfg := mysql.NewConfig()
	cfg.User = constants.AdminUser
	cfg.Passwd = pwd.Admin()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(addr, strconv.Itoa(constants.MySQLAdminPort))
	cfg.InterpolateParams = true
	cfg.ParseTime = true
	cfg.Timeout = connTimeout
	cfg.ReadTimeout = readTimeout
	db, err := sqlx.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", cluster.PodName(index), err)
	}
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(30 * time.Second)
	return &operator{
		namespace: cluster.Namespace,
		name:      cluster.PodName(index),
		passwd:    pwd,
		index:     index,
		db:        db,
	}, nil
}

func (defaultFactory) Cleanup() {}

type operator struct {
	namespace string
	name      string
	passwd    *password.MySQLPassword
	index     int
	db        *sqlx.DB
}

var _ Operator = &operator{}

func (o *operator) Name() string {
	return fmt.Sprintf("%s/%s", o.namespace, o.name)
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
