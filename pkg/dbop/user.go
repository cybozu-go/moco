package dbop

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/cybozu-go/moco/pkg/constants"
)

const systemUserHost = "%"

var authenticationPluginRegexp = regexp.MustCompile(constants.AuthenticationPluginPattern)

type mysqlUserPlugin struct {
	User   string `db:"User"`
	Host   string `db:"Host"`
	Plugin string `db:"plugin"`
}

// SetSystemUserAuthenticationPlugin changes the authentication plugin of all
// MOCO-created users on this instance.  The passwords are kept unchanged.
//
// The session executing ALTER USER disables binary logging so that this
// local-only operation does not create errant GTIDs.  If the instance is
// super_read_only, only super_read_only is cleared temporarily and restored
// before returning.
func (o *operator) SetSystemUserAuthenticationPlugin(ctx context.Context, plugin string) (changed []string, retErr error) {
	if !authenticationPluginRegexp.MatchString(plugin) {
		return nil, fmt.Errorf("invalid authentication plugin %q", plugin)
	}

	conn, err := o.db.Connx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get a connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var rows []mysqlUserPlugin
	if err := conn.SelectContext(ctx, &rows, "SELECT User, Host, plugin FROM mysql.user"); err != nil {
		return nil, fmt.Errorf("failed to get user authentication plugins: %w", err)
	}
	pluginByUser := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.Host != systemUserHost {
			continue
		}
		pluginByUser[row.User] = row.Plugin
	}

	var usersToChange []string
	for _, user := range constants.MocoUsers {
		currentPlugin, ok := pluginByUser[user]
		if !ok || currentPlugin == plugin {
			continue
		}
		usersToChange = append(usersToChange, user)
	}
	if len(usersToChange) == 0 {
		return nil, nil
	}

	var superReadOnly bool
	if err := conn.GetContext(ctx, &superReadOnly, "SELECT @@global.super_read_only"); err != nil {
		return nil, fmt.Errorf("failed to get super_read_only: %w", err)
	}
	if superReadOnly {
		if _, err := conn.ExecContext(ctx, "SET GLOBAL super_read_only = 0"); err != nil {
			return nil, fmt.Errorf("failed to clear super_read_only: %w", err)
		}
		defer func() {
			restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if _, err := o.db.ExecContext(restoreCtx, "SET GLOBAL super_read_only = 1"); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to restore super_read_only: %w", err))
			}
		}()
	}

	var sqlLogBin int
	if err := conn.GetContext(ctx, &sqlLogBin, "SELECT @@session.sql_log_bin"); err != nil {
		return nil, fmt.Errorf("failed to get sql_log_bin: %w", err)
	}
	if sqlLogBin != 0 {
		if _, err := conn.ExecContext(ctx, "SET SESSION sql_log_bin = 0"); err != nil {
			return nil, fmt.Errorf("failed to disable sql_log_bin: %w", err)
		}
		defer func() {
			restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = conn.ExecContext(restoreCtx, "SET SESSION sql_log_bin = 1")
		}()
	}

	for _, user := range usersToChange {
		password, ok := o.passwordForUser(user)
		if !ok {
			return changed, fmt.Errorf("no password is configured for system user %s", user)
		}
		if _, err := conn.ExecContext(ctx, "ALTER USER ?@'%' IDENTIFIED WITH "+plugin+" BY ?", user, password); err != nil {
			return changed, fmt.Errorf("failed to change authentication plugin for %s: %w", user, err)
		}
		changed = append(changed, user)
	}

	return changed, nil
}

func (o *operator) passwordForUser(user string) (string, bool) {
	if o.passwd == nil {
		return "", false
	}

	switch user {
	case constants.AdminUser:
		return o.passwd.Admin(), true
	case constants.AgentUser:
		return o.passwd.Agent(), true
	case constants.ReplicationUser:
		return o.passwd.Replicator(), true
	case constants.CloneDonorUser:
		return o.passwd.Donor(), true
	case constants.ExporterUser:
		return o.passwd.Exporter(), true
	case constants.BackupUser:
		return o.passwd.Backup(), true
	case constants.ReadOnlyUser:
		return o.passwd.ReadOnly(), true
	case constants.WritableUser:
		return o.passwd.Writable(), true
	}
	return "", false
}
