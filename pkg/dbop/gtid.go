package dbop

import (
	"context"
	"fmt"
)

func (o *operator) FindTopRunner(ctx context.Context, status []*MySQLInstanceStatus) (int, error) {
	latest := -1
	var latestGTIDs string

	for i := 0; i < len(status); i++ {
		if status[i] == nil {
			continue
		}
		repl := status[i].ReplicaStatus
		if repl == nil {
			continue
		}

		// There are cases where Retrieved_Gtid_Set is empty,
		// such as when there is no transaction immediately after a fail-over.
		// Therefore, Retrieved_Gtid_Set and Executed_Gtid_Set are unioned to find for the top runner.
		// The union of two GTID sets is simply their joined together with an interposed comma.
		// https://dev.mysql.com/doc/refman/8.0/en/gtid-functions.html
		var gtids string
		if len(repl.RetrievedGtidSet) == 0 {
			gtids = repl.ExecutedGtidSet
		} else {
			gtids = fmt.Sprintf("%s,%s", repl.RetrievedGtidSet, repl.ExecutedGtidSet)
		}
		if len(gtids) == 0 {
			continue
		}

		if len(latestGTIDs) == 0 {
			latest = i
			latestGTIDs = gtids
			continue
		}

		isSubset, err := o.IsSubsetGTID(ctx, gtids, latestGTIDs)
		if err != nil {
			return -1, err
		}
		if isSubset {
			continue
		}

		isSubset, err = o.IsSubsetGTID(ctx, latestGTIDs, gtids)
		if err != nil {
			return -1, err
		}
		if isSubset {
			latest = i
			latestGTIDs = gtids
			continue
		}

		return -1, fmt.Errorf("%w: set1=%s, set2=%s", ErrErrantTransactions, gtids, latestGTIDs)
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

func (o *operator) SubtractGTID(ctx context.Context, set1, set2 string) (string, error) {
	var ret string
	if err := o.db.GetContext(ctx, &ret, `SELECT GTID_SUBTRACT(?,?)`, set1, set2); err != nil {
		return "", fmt.Errorf("failed to get gtid_subtract(%s, %s): %w", set1, set2, err)
	}
	return ret, nil
}
