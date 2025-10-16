package dbop

import (
	"context"
	"fmt"
	"math"
)

const semiSyncMasterTimeout = 24 * 60 * 60 * 1000

func (o *operator) ConfigureReplica(ctx context.Context, primary AccessInfo, semisync bool) error {
	if _, err := o.db.ExecContext(ctx, `STOP REPLICA`); err != nil {
		return fmt.Errorf("failed to stop replica: %w", err)
	}
	var version string
	if err := o.db.GetContext(ctx, &version, `SELECT SUBSTRING_INDEX(VERSION(), '.', 2)`); err != nil {
		return fmt.Errorf("failed to get version: %w", err)
	}
	var cmd string
	if version == "8.4" {
		cmd = `CHANGE REPLICATION SOURCE TO SOURCE_HOST = :Host, SOURCE_PORT = :Port, SOURCE_USER = :User, SOURCE_PASSWORD = :Password, SOURCE_AUTO_POSITION = 1, GET_SOURCE_PUBLIC_KEY = 1`
	} else if version == "8.0" {
		cmd = `CHANGE MASTER TO MASTER_HOST = :Host, MASTER_PORT = :Port, MASTER_USER = :User, MASTER_PASSWORD = :Password, MASTER_AUTO_POSITION = 1, GET_MASTER_PUBLIC_KEY = 1`
	} else {
		return fmt.Errorf("unsupported version: %s", version)
	}
	if _, err := o.db.NamedExecContext(ctx, cmd, primary); err != nil {
		return fmt.Errorf("failed to change primary: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, "SET GLOBAL rpl_semi_sync_slave_enabled=?", semisync); err != nil {
		return fmt.Errorf("failed to set rpl_semi_sync_slave_enabled: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, "SET GLOBAL rpl_semi_sync_master_enabled=OFF"); err != nil {
		return fmt.Errorf("failed to disable rpl_semi_sync_master_enabled: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, `START REPLICA`); err != nil {
		return fmt.Errorf("failed to start replica: %w", err)
	}
	return nil
}

func (o *operator) ConfigurePrimary(ctx context.Context, semisync bool, waitForCount int) error {
	if _, err := o.db.ExecContext(ctx, "SET GLOBAL rpl_semi_sync_master_timeout=?", semiSyncMasterTimeout); err != nil {
		return fmt.Errorf("failed to set rpl_semi_sync_master_timeout count: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, "SET GLOBAL rpl_semi_sync_master_wait_for_slave_count=?", waitForCount); err != nil {
		return fmt.Errorf("failed to set rpl_semi_sync_master_wait_for_slave_count count: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, "SET GLOBAL rpl_semi_sync_master_enabled=?", semisync); err != nil {
		return fmt.Errorf("failed to enable semi-sync primary: %w", err)
	}
	return nil
}

func (o *operator) StopReplicaIOThread(ctx context.Context) error {
	if _, err := o.db.ExecContext(ctx, `STOP REPLICA IO_THREAD`); err != nil {
		return fmt.Errorf("failed to stop replica IO thread: %w", err)
	}
	return nil
}

func (o *operator) WaitForGTID(ctx context.Context, gtid string, timeoutSeconds int) error {
	var err error
	var timeout bool
	err = o.db.GetContext(ctx, &timeout, `SELECT WAIT_FOR_EXECUTED_GTID_SET(?, ?)`, gtid, timeoutSeconds)
	if err != nil {
		return fmt.Errorf("failed to wait GTID subset %s: %w", gtid, err)
	}
	if timeout {
		return ErrTimeout
	}
	return nil
}

func (o *operator) SetReadOnly(ctx context.Context, readOnly bool) error {
	if readOnly {
		if _, err := o.db.ExecContext(ctx, "SET GLOBAL super_read_only=1"); err != nil {
			return fmt.Errorf("failed to set super_read_only=1: %w", err)
		}
		return nil
	}

	if _, err := o.db.ExecContext(ctx, "STOP REPLICA"); err != nil {
		return fmt.Errorf("failed to stop replica: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, "RESET REPLICA"); err != nil {
		return fmt.Errorf("failed to stop replica: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, "SET GLOBAL read_only=0"); err != nil {
		return fmt.Errorf("failed to set read_only=0: %w", err)
	}
	return nil
}

func ComputeRequiredACKs(instances int) int {
	return int(math.Ceil(float64(instances-1) / 2))
}
