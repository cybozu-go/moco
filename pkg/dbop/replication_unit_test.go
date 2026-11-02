package dbop

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
)

// recordingDriver is a minimal database/sql driver that records the executed
// statements.  It allows the SQL issued by operator methods to be tested
// without a MySQL server.
type recordingDriver struct {
	mu      sync.Mutex
	queries []string
}

func (d *recordingDriver) Open(string) (driver.Conn, error) {
	return &recordingConn{driver: d}, nil
}

func (d *recordingDriver) record(query string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = append(d.queries, query)
}

func (d *recordingDriver) recorded() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.queries...)
}

type recordingConnector struct {
	driver *recordingDriver
}

func (c *recordingConnector) Connect(context.Context) (driver.Conn, error) {
	return &recordingConn{driver: c.driver}, nil
}

func (c *recordingConnector) Driver() driver.Driver {
	return c.driver
}

type recordingConn struct {
	driver *recordingDriver
}

func (c *recordingConn) Prepare(query string) (driver.Stmt, error) {
	return &recordingStmt{conn: c, query: query}, nil
}

func (c *recordingConn) Close() error {
	return nil
}

func (c *recordingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

type recordingStmt struct {
	conn  *recordingConn
	query string
}

func (s *recordingStmt) Close() error {
	return nil
}

func (s *recordingStmt) NumInput() int {
	return -1
}

func (s *recordingStmt) Exec([]driver.Value) (driver.Result, error) {
	s.conn.driver.record(s.query)
	return driver.RowsAffected(0), nil
}

func (s *recordingStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("queries are not supported")
}

func newRecordingOperator(t *testing.T) (*operator, *recordingDriver) {
	t.Helper()

	drv := &recordingDriver{}
	db := sql.OpenDB(&recordingConnector{driver: drv})
	t.Cleanup(func() {
		_ = db.Close()
	})
	return &operator{db: sqlx.NewDb(db, "recording")}, drv
}

func TestSetReadOnly(t *testing.T) {
	ctx := context.Background()

	t.Run("writable", func(t *testing.T) {
		op, drv := newRecordingOperator(t)
		if err := op.SetReadOnly(ctx, false); err != nil {
			t.Fatalf("SetReadOnly failed: %v", err)
		}

		want := []string{
			"STOP REPLICA",
			"RESET REPLICA ALL",
			"SET GLOBAL read_only=0",
		}
		if got := drv.recorded(); !reflect.DeepEqual(got, want) {
			t.Errorf("unexpected statements: got %q, want %q", got, want)
		}
	})

	t.Run("read-only", func(t *testing.T) {
		op, drv := newRecordingOperator(t)
		if err := op.SetReadOnly(ctx, true); err != nil {
			t.Fatalf("SetReadOnly failed: %v", err)
		}

		want := []string{"SET GLOBAL super_read_only=1"}
		if got := drv.recorded(); !reflect.DeepEqual(got, want) {
			t.Errorf("unexpected statements: got %q, want %q", got, want)
		}
	})
}
