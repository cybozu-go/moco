package bkop

import (
	"context"
	"fmt"
)

func (o operator) GetServerStatus(ctx context.Context, st *ServerStatus) error {
	ms := &showMasterStatus{}
	if err := o.db.GetContext(ctx, ms, `SHOW MASTER STATUS`); err != nil {
		return fmt.Errorf("failed to show master status: %w", err)
	}

	if err := o.db.GetContext(ctx, st, `SELECT @@super_read_only, @@server_uuid`); err != nil {
		return fmt.Errorf("failed to get global variables: %w", err)
	}

	st.CurrentBinlog = ms.File
	return nil
}
