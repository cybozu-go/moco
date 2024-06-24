package bkop

import (
	"context"
	"fmt"
)

func (o operator) GetServerStatus(ctx context.Context, st *ServerStatus) error {
	bls := &showBinaryLogStatus{}
	var version string
	if err := o.db.GetContext(ctx, &version, `SELECT SUBSTRING_INDEX(VERSION(), '.', 2)`); err != nil {
		return fmt.Errorf("failed to get version: %w", err)
	}
	if version == "8.4" {
		if err := o.db.GetContext(ctx, bls, `SHOW BINARY LOG STATUS`); err != nil {
			return fmt.Errorf("failed to show binary log status: %w", err)
		}
	} else if version == "8.0" {
		if err := o.db.GetContext(ctx, bls, `SHOW MASTER STATUS`); err != nil {
			return fmt.Errorf("failed to show master status: %w", err)
		}
	} else {
		return fmt.Errorf("unsupported version: %s", version)
	}
	if err := o.db.GetContext(ctx, st, `SELECT @@super_read_only, @@server_uuid`); err != nil {
		return fmt.Errorf("failed to get global variables: %w", err)
	}

	st.CurrentBinlog = bls.File
	return nil
}
