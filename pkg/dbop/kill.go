package dbop

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
)

func (o *operator) KillConnections(ctx context.Context) error {
	var procs []Process

	if err := o.db.SelectContext(ctx, &procs, `SELECT ID, USER, HOST FROM information_schema.PROCESSLIST`); err != nil {
		return fmt.Errorf("failed to get process list: %w", err)
	}

	for _, p := range procs {
		if constants.MocoSystemUsers[p.User] {
			continue
		}
		if p.Host == "localhost" {
			continue
		}

		if _, err := o.db.ExecContext(ctx, `KILL CONNECTION ?`, p.ID); err != nil {
			return fmt.Errorf("failed to kill connection %d for %s from %s: %w", p.ID, p.User, p.Host, err)
		}
	}
	return nil
}
