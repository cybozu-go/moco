package bkop

import (
	"sort"
	"strconv"
	"strings"
)

// SortBinlogs sort binlog filenames according to its number.
// `binlogs` should contains filenames such as `binlog.000001`.
func SortBinlogs(binlogs []string) {
	sort.Slice(binlogs, func(i, j int) bool {
		log1 := binlogs[i]
		log2 := binlogs[j]
		var index1, index2 int64
		if fields := strings.Split(log1, "."); len(fields) == 2 {
			index1, _ = strconv.ParseInt(fields[1], 10, 64)
		}
		if fields := strings.Split(log2, "."); len(fields) == 2 {
			index2, _ = strconv.ParseInt(fields[1], 10, 64)
		}
		return index1 < index2
	})
}
