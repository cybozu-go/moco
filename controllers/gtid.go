package controllers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cybozu-go/moco"
)

type MySQLGTIDSet map[string][]Interval

type Interval struct {
	Start, End int64
}

func latestTransactionID(intervals []Interval) int64 {
	latest := int64(0)
	for _, interval := range intervals {
		if latest < interval.End {
			latest = interval.End
		}
	}
	return latest
}

func CompareGTIDSet(set1, set2 MySQLGTIDSet) (int, error) {
	keys := map[string]struct{}{}
	for k := range set1 {
		keys[k] = struct{}{}
	}
	for k := range set2 {
		keys[k] = struct{}{}
	}

	compared := false
	set1IsLater := false
	for k := range keys {
		if _, ok := set1[k]; !ok {
			if compared && set1IsLater {
				return 0, moco.ErrCannotCompareGITDs
			}
			compared = true
			set1IsLater = false
			continue
		}
		if _, ok := set2[k]; !ok {
			if compared && !set1IsLater {
				return 0, moco.ErrCannotCompareGITDs
			}
			compared = true
			set1IsLater = true
			continue
		}
		latest1 := latestTransactionID(set1[k])
		latest2 := latestTransactionID(set2[k])
		if latest1 == latest2 {
			continue
		}

		if latest1 > latest2 {
			if compared && !set1IsLater {
				return 0, moco.ErrCannotCompareGITDs
			}
			set1IsLater = true
		} else {
			if compared && set1IsLater {
				return 0, moco.ErrCannotCompareGITDs
			}
			set1IsLater = false
		}

		compared = true
	}
	if !compared {
		return 0, nil
	}
	if set1IsLater {
		return 1, nil
	}
	return -1, nil
}

func ParseGTIDSet(input string) (MySQLGTIDSet, error) {
	gtids := make(MySQLGTIDSet)

	for _, gtid := range strings.Split(input, ",") {
		gtid = strings.TrimSpace(gtid)
		if len(gtid) == 0 {
			continue
		}
		parts := strings.Split(gtid, ":")
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid GTID: %s", input)
		}
		var result []Interval
		for _, part := range parts[1:] {
			intervals := strings.Split(part, "-")
			interval := Interval{}
			switch len(intervals) {
			case 1:
				start, err := strconv.ParseInt(intervals[0], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid GTID: %s", input)
				}
				interval.Start = start
				interval.End = start
			case 2:
				start, err := strconv.ParseInt(intervals[0], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid GTID: %s", input)
				}
				end, err := strconv.ParseInt(intervals[1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid GTID: %s", input)
				}
				interval.Start = start
				interval.End = end
			default:
				return nil, fmt.Errorf("invalid GTID: %s", input)
			}
			result = append(result, interval)
		}
		gtids[parts[0]] = result
	}
	return gtids, nil
}
