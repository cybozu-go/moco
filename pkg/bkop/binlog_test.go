package bkop

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSortBinlogs(t *testing.T) {
	binlogs := []string{
		"binlog.01",
		"binlog.101",
		"binlog.02",
		"binlog.31",
	}
	SortBinlogs(binlogs)
	expected := []string{
		"binlog.01",
		"binlog.02",
		"binlog.31",
		"binlog.101",
	}

	if !cmp.Equal(binlogs, expected) {
		t.Error("wrong sort result", cmp.Diff(binlogs, expected))
	}
}
