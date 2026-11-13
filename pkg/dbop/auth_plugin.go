package dbop

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/jmoiron/sqlx"
)

// SupportedAuthPlugins is the set of authentication plugins that
// AlterUserAuthPlugin accepts, derived from the enum of
// MySQLClusterSpec.SystemUserAuthPlugin.
var SupportedAuthPlugins = map[string]bool{
	mocov1beta2.AuthPluginCachingSHA2Password.String(): true,
	mocov1beta2.AuthPluginMySQLNativePassword.String(): true,
}

func (o *operator) GetUserAuthPlugins(ctx context.Context) (map[string]string, error) {
	query, args, err := sqlx.In(`SELECT user, plugin FROM mysql.user WHERE user IN (?) AND host = '%'`, constants.MocoUsers)
	if err != nil {
		return nil, fmt.Errorf("failed to build query for mysql.user: %w", err)
	}
	var rows []struct {
		User   string `db:"user"`
		Plugin string `db:"plugin"`
	}
	if err := o.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to select from mysql.user: %w", err)
	}
	plugins := make(map[string]string, len(rows))
	for _, r := range rows {
		plugins[r.User] = r.Plugin
	}
	return plugins, nil
}

func (o *operator) IsAuthPluginActive(ctx context.Context, plugin string) (bool, error) {
	var statuses []string
	err := o.db.SelectContext(ctx, &statuses,
		`SELECT plugin_status FROM information_schema.plugins WHERE plugin_name = ?`, plugin)
	if err != nil {
		return false, fmt.Errorf("failed to select from information_schema.plugins: %w", err)
	}
	return len(statuses) == 1 && statuses[0] == "ACTIVE", nil
}

func (o *operator) AlterUserAuthPlugin(ctx context.Context, user, plugin string) error {
	if !SupportedAuthPlugins[plugin] {
		return fmt.Errorf("unsupported authentication plugin: %s", plugin)
	}
	pwd, err := o.userPassword(user)
	if err != nil {
		return err
	}

	// A dedicated connection guarantees that sql_log_bin=0 applies to the
	// same session as the ALTER USER statement.
	conn, err := o.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to obtain a connection: %w", err)
	}
	defer closeBinlogDisabledConn(ctx, conn)

	if _, err := conn.ExecContext(ctx, "SET sql_log_bin=0"); err != nil {
		return fmt.Errorf("failed to set sql_log_bin=0: %w", err)
	}
	// The user and plugin names cannot be placeholders. Both come from fixed
	// allowlists: the user is validated by userPassword and the plugin by
	// SupportedAuthPlugins.
	stmt := fmt.Sprintf(`ALTER USER '%s'@'%%' IDENTIFIED WITH %s BY ?`, user, plugin)
	if _, err := conn.ExecContext(ctx, stmt, pwd); err != nil {
		return fmt.Errorf("failed to change the authentication plugin of %s to %s: %w", user, plugin, err)
	}
	return nil
}

func (o *operator) SetSuperReadOnly(ctx context.Context, on bool) error {
	val := 0
	if on {
		val = 1
	}
	if _, err := o.db.ExecContext(ctx, fmt.Sprintf("SET GLOBAL super_read_only=%d", val)); err != nil {
		return fmt.Errorf("failed to set super_read_only=%d: %w", val, err)
	}
	return nil
}

// userPassword returns the current password of a MOCO user. It also works as
// the allowlist of user names for AlterUserAuthPlugin.
func (o *operator) userPassword(user string) (string, error) {
	switch user {
	case constants.AdminUser:
		return o.passwd.Admin(), nil
	case constants.AgentUser:
		return o.passwd.Agent(), nil
	case constants.ReplicationUser:
		return o.passwd.Replicator(), nil
	case constants.CloneDonorUser:
		return o.passwd.Donor(), nil
	case constants.ExporterUser:
		return o.passwd.Exporter(), nil
	case constants.BackupUser:
		return o.passwd.Backup(), nil
	case constants.ReadOnlyUser:
		return o.passwd.ReadOnly(), nil
	case constants.WritableUser:
		return o.passwd.Writable(), nil
	}
	return "", fmt.Errorf("unknown MOCO user: %s", user)
}

// closeBinlogDisabledConn restores sql_log_bin=1 before returning the
// connection to the pool. If the restore fails, the connection is discarded
// so that a session with sql_log_bin=0 is never reused.
func closeBinlogDisabledConn(ctx context.Context, conn *sql.Conn) {
	if _, err := conn.ExecContext(ctx, "SET sql_log_bin=1"); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = conn.Close()
}
