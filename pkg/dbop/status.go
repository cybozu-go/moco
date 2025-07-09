package dbop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var statusGlobalVarsString = strings.Join(statusGlobalVars, ",")

func (o *operator) GetStatus(ctx context.Context) (*MySQLInstanceStatus, error) {

	status := &MySQLInstanceStatus{}

	globalVariablesStatus, err := o.getGlobalVariablesStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get global variables: pod=%s, namespace=%s: %w", o.name, o.namespace, err)
	}
	status.GlobalVariables = *globalVariablesStatus

	globalStatus, err := o.getGlobalStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get global status: pod=%s, namespace=%s: %w", o.name, o.namespace, err)
	}
	status.GlobalStatus = globalStatus

	if err := o.db.Select(&status.ReplicaHosts, `SHOW REPLICAS`); err != nil {
		return nil, fmt.Errorf("failed to get replica hosts: pod=%s, namespace=%s: %w", o.name, o.namespace, err)
	}

	replicaStatus, err := o.getReplicaStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get replica status: pod=%s, namespace=%s: %w", o.name, o.namespace, err)
	}
	status.ReplicaStatus = replicaStatus

	cloneStatus, err := o.getCloneStateStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get clone status: pod=%s, namespace=%s: %w", o.name, o.namespace, err)
	}
	status.CloneStatus = cloneStatus

	return status, nil
}

func (o *operator) getGlobalVariablesStatus(ctx context.Context) (*GlobalVariables, error) {
	status := &GlobalVariables{}
	err := o.db.GetContext(ctx, status, "SELECT "+statusGlobalVarsString)
	if err != nil {
		return nil, fmt.Errorf("failed to get mysql global variables: %w", err)
	}
	return status, nil
}

func (o *operator) getGlobalStatus(ctx context.Context) (*GlobalStatus, error) {
	status := &GlobalStatus{}
	rows, err := o.db.QueryxContext(ctx, "SHOW GLOBAL STATUS LIKE '%Rpl_semi%'")
	if err != nil {
		return nil, fmt.Errorf("failed to get mysql global status: %w", err)
	}
	defer rows.Close()
	var variableName string
	var value string
	for rows.Next() {
		if err = rows.Scan(&variableName, &value); err == nil {
			switch variableName {
			case "Rpl_semi_sync_master_wait_sessions":
				semiSyncMasterWaitSessions, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("failed to parse Rpl_semi_sync_master_wait_sessions: %w", err)
				}
				status.SemiSyncMasterWaitSessions = semiSyncMasterWaitSessions
			default:
				continue
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read mysql globals status: %w", err)
	}

	return status, nil
}

func (o *operator) getReplicaStatus(ctx context.Context) (*ReplicaStatus, error) {
	status := &ReplicaStatus{}
	err := o.db.GetContext(ctx, status, `SHOW REPLICA STATUS`)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// replica status can be empty for non-replica servers
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get replica status: %w", err)
	}
	return status, nil
}

func (o *operator) getCloneStateStatus(ctx context.Context) (*CloneStatus, error) {
	status := &CloneStatus{}
	err := o.db.GetContext(ctx, status, `SELECT state FROM performance_schema.clone_status`)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// clone status can be empty
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get ps.clone_status: %w", err)
	}
	return status, nil
}
