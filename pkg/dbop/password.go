package dbop

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/cybozu-go/moco/pkg/constants"
)

// defaultAuthPlugin is the fallback when authentication_policy's first element
// is '*' (any plugin allowed) or empty.
const defaultAuthPlugin = "caching_sha2_password"

// validPluginName matches MySQL authentication plugin names (alphanumeric + underscore).
var validPluginName = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// validMocoUsers is a set of allowed user names for password rotation SQL operations.
var validMocoUsers map[string]struct{}

func init() {
	validMocoUsers = make(map[string]struct{}, len(constants.MocoUsers))
	for _, u := range constants.MocoUsers {
		validMocoUsers[u] = struct{}{}
	}
}

// validateMocoUser returns an error if user is not one of the fixed system user
// names. This guards against SQL injection since the user name is interpolated
// directly into ALTER USER statements.
func validateMocoUser(user string) error {
	if _, ok := validMocoUsers[user]; !ok {
		return fmt.Errorf("invalid MOCO user name %q: must be one of constants.MocoUsers", user)
	}
	return nil
}

// GetAuthPlugin returns the default authentication plugin for the first factor
// by parsing @@global.authentication_policy (introduced in MySQL 8.0.27).
//
// The variable format is "plugin1,plugin2,plugin3" where each element controls
// one authentication factor. Only the first element (primary authentication) is
// relevant for MOCO system users.
//
// If the first element is '*' (any plugin allowed) or empty, "caching_sha2_password"
// is returned as the default.
func (o *operator) GetAuthPlugin(ctx context.Context) (string, error) {
	var policy string
	if err := o.db.GetContext(ctx, &policy, "SELECT @@global.authentication_policy"); err != nil {
		return "", fmt.Errorf("failed to get authentication_policy: %w", err)
	}
	return parseAuthPlugin(policy)
}

// parseAuthPlugin extracts the first-factor authentication plugin from the
// authentication_policy value. See GetAuthPlugin for the full specification.
func parseAuthPlugin(policy string) (string, error) {
	first := strings.SplitN(policy, ",", 2)[0]
	first = strings.TrimSpace(first)

	if first == "" || first == "*" {
		return defaultAuthPlugin, nil
	}
	if !validPluginName.MatchString(first) {
		return "", fmt.Errorf("invalid authentication plugin name %q from authentication_policy", first)
	}
	return first, nil
}

// SetSuperReadOnly sets or unsets super_read_only on the instance.
// Unlike SetReadOnly, this does NOT stop replication or reset replica state.
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

// RotateUserPassword executes ALTER USER ... IDENTIFIED BY ...
// RETAIN CURRENT PASSWORD with sql_log_bin=0 to prevent binlog propagation to
// cross-cluster replicas.
//
// Unlike the previous implementation, this does NOT specify IDENTIFIED WITH <plugin>.
// Using plain IDENTIFIED BY avoids MySQL Error 3894 which is raised when the
// authentication plugin is changed in a RETAIN CURRENT PASSWORD statement.
// Plugin migration is handled separately by MigrateUserAuthPlugin after DISCARD.
//
// A dedicated connection (db.Conn) is used to ensure sql_log_bin=0 is set on the
// same session as the ALTER USER statement. sql_log_bin is a session variable, so
// it does not affect other connections in the pool.
//
// user must be one of the fixed system user names defined in pkg/constants/users.go.
// The user name is interpolated directly into the SQL statement because MySQL does
// not support placeholders for the user position of ALTER USER.
func (o *operator) RotateUserPassword(ctx context.Context, user, newPassword string) error {
	if err := validateMocoUser(user); err != nil {
		return err
	}

	conn, err := o.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for rotate: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SET sql_log_bin=0"); err != nil {
		return fmt.Errorf("failed to set sql_log_bin=0: %w", err)
	}
	query := fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED BY ? RETAIN CURRENT PASSWORD", user)
	if _, err := conn.ExecContext(ctx, query, newPassword); err != nil {
		return fmt.Errorf("failed to rotate password for %s: %w", user, err)
	}
	return nil
}

// MigrateUserAuthPlugin executes ALTER USER ... IDENTIFIED WITH <authPlugin> BY ...
// with sql_log_bin=0 to migrate the user to the target authentication plugin.
//
// This is called after DISCARD OLD PASSWORD in Phase 2, so the user has only
// a single password at this point. The statement re-hashes the password under
// the new plugin without RETAIN, which is safe because there is no secondary
// password to preserve.
//
// A dedicated connection (db.Conn) is used to ensure sql_log_bin=0 is set on the
// same session as the ALTER USER statement.
//
// user must be one of the fixed system user names defined in pkg/constants/users.go.
// The user name and authPlugin are interpolated directly into the SQL statement
// because MySQL does not support placeholders in these positions of ALTER USER.
func (o *operator) MigrateUserAuthPlugin(ctx context.Context, user, password, authPlugin string) error {
	if err := validateMocoUser(user); err != nil {
		return err
	}
	if !validPluginName.MatchString(authPlugin) {
		return fmt.Errorf("invalid authentication plugin name %q", authPlugin)
	}

	conn, err := o.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for auth plugin migration: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SET sql_log_bin=0"); err != nil {
		return fmt.Errorf("failed to set sql_log_bin=0: %w", err)
	}
	query := fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED WITH %s BY ?", user, authPlugin)
	if _, err := conn.ExecContext(ctx, query, password); err != nil {
		return fmt.Errorf("failed to migrate auth plugin for %s: %w", user, err)
	}
	return nil
}

// HasDualPassword checks whether the given user currently has a dual password
// (i.e., User_attributes contains additional_password in mysql.user).
// This is a read-only SELECT, so no dedicated connection or sql_log_bin=0 is needed.
func (o *operator) HasDualPassword(ctx context.Context, user string) (bool, error) {
	if err := validateMocoUser(user); err != nil {
		return false, err
	}
	var hasDual bool
	query := "SELECT IFNULL(JSON_CONTAINS_PATH(User_attributes, 'one', '$.additional_password'), 0) FROM mysql.user WHERE User = ? AND Host = '%'"
	if err := o.db.GetContext(ctx, &hasDual, query, user); err != nil {
		return false, fmt.Errorf("failed to check dual password for %s: %w", user, err)
	}
	return hasDual, nil
}

// DiscardOldPassword executes ALTER USER ... DISCARD OLD PASSWORD
// with sql_log_bin=0 to prevent binlog propagation to cross-cluster replicas.
//
// See RotateUserPassword for the rationale on dedicated connection and sql_log_bin.
//
// user must be one of the fixed system user names defined in pkg/constants/users.go.
func (o *operator) DiscardOldPassword(ctx context.Context, user string) error {
	if err := validateMocoUser(user); err != nil {
		return err
	}

	conn, err := o.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for discard: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SET sql_log_bin=0"); err != nil {
		return fmt.Errorf("failed to set sql_log_bin=0: %w", err)
	}
	query := fmt.Sprintf("ALTER USER '%s'@'%%' DISCARD OLD PASSWORD", user)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to discard old password for %s: %w", user, err)
	}
	return nil
}
