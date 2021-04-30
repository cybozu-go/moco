package dbop

import (
	"context"
	"fmt"
)

func (o *operator) FindTopRunner(ctx context.Context, status []*MySQLInstanceStatus) (int, error) {
	latest := -1
	var latestGTID string

	for i := 0; i < len(status); i++ {
		if status[i] == nil {
			continue
		}
		repl := status[i].ReplicaStatus
		if repl == nil {
			continue
		}

		gtid := repl.RetrievedGtidSet
		if len(gtid) == 0 {
			continue
		}

		if len(latestGTID) == 0 {
			latest = i
			latestGTID = gtid
			continue
		}

		isSubset, err := o.IsSubsetGTID(ctx, gtid, latestGTID)
		if err != nil {
			return -1, err
		}
		if isSubset {
			continue
		}

		isSubset, err = o.IsSubsetGTID(ctx, latestGTID, gtid)
		if err != nil {
			return -1, err
		}
		if isSubset {
			latest = i
			latestGTID = gtid
			continue
		}

		return -1, fmt.Errorf("%w: set1=%s, set2=%s", ErrErrantTransactions, gtid, latestGTID)
	}

	if latest == -1 {
		return -1, ErrNoTopRunner
	}

	return latest, nil
}

func (o *operator) IsSubsetGTID(ctx context.Context, set1, set2 string) (bool, error) {
	var ret bool
	if err := o.db.GetContext(ctx, &ret, `SELECT GTID_SUBSET(?,?)`, set1, set2); err != nil {
		return false, fmt.Errorf("failed to get gtid_subset(%s, %s): %w", set1, set2, err)
	}
	return ret, nil
}
