package dbop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// SemiSyncNames holds plugin names and variable names for the installed semi-sync plugin variant.
type SemiSyncNames struct {
	// Plugin names
	SourcePluginName  string
	ReplicaPluginName string

	// System variables (source side)
	SourceEnabled       string
	SourceTimeout       string
	WaitForReplicaCount string

	// System variables (replica side)
	ReplicaEnabled string

	// Status variables
	SourceWaitSessions string
}

// NewSemiSyncNames is the name set for the new semi-sync plugins (rpl_semi_sync_source/replica).
var NewSemiSyncNames = SemiSyncNames{
	SourcePluginName:    "rpl_semi_sync_source",
	ReplicaPluginName:   "rpl_semi_sync_replica",
	SourceEnabled:       "rpl_semi_sync_source_enabled",
	SourceTimeout:       "rpl_semi_sync_source_timeout",
	WaitForReplicaCount: "rpl_semi_sync_source_wait_for_replica_count",
	ReplicaEnabled:      "rpl_semi_sync_replica_enabled",
	SourceWaitSessions:  "Rpl_semi_sync_source_wait_sessions",
}

// LegacySemiSyncNames is the name set for the deprecated semi-sync plugins (rpl_semi_sync_master/slave).
var LegacySemiSyncNames = SemiSyncNames{
	SourcePluginName:    "rpl_semi_sync_master",
	ReplicaPluginName:   "rpl_semi_sync_slave",
	SourceEnabled:       "rpl_semi_sync_master_enabled",
	SourceTimeout:       "rpl_semi_sync_master_timeout",
	WaitForReplicaCount: "rpl_semi_sync_master_wait_for_slave_count",
	ReplicaEnabled:      "rpl_semi_sync_slave_enabled",
	SourceWaitSessions:  "Rpl_semi_sync_master_wait_sessions",
}

// DetectSemiSyncNames detects which semi-sync plugin is installed and returns the appropriate name set.
// If neither plugin is installed, it returns NewSemiSyncNames (for fresh installations).
func DetectSemiSyncNames(ctx context.Context, db *sqlx.DB) (*SemiSyncNames, error) {
	// Check for new plugin first
	status, err := getPluginStatus(ctx, db, NewSemiSyncNames.SourcePluginName)
	if err != nil {
		return nil, fmt.Errorf("failed to check semi-sync source plugin: %w", err)
	}
	if status == "ACTIVE" {
		return &NewSemiSyncNames, nil
	}

	// Check for legacy plugin
	status, err = getPluginStatus(ctx, db, LegacySemiSyncNames.SourcePluginName)
	if err != nil {
		return nil, fmt.Errorf("failed to check semi-sync master plugin: %w", err)
	}
	if status == "ACTIVE" {
		return &LegacySemiSyncNames, nil
	}

	// Neither installed — assume new names for fresh install
	return &NewSemiSyncNames, nil
}

// getPluginStatus returns the PLUGIN_STATUS for the given plugin name.
// Returns "" if the plugin is not installed, or the status string ("ACTIVE", "INACTIVE", "DISABLED", etc.).
func getPluginStatus(ctx context.Context, db *sqlx.DB, name string) (string, error) {
	var status string
	err := db.GetContext(ctx, &status,
		"SELECT PLUGIN_STATUS FROM information_schema.plugins WHERE PLUGIN_NAME=?", name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("failed to check plugin %s: %w", name, err)
	}
	return status, nil
}
