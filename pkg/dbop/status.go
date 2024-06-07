package dbop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
