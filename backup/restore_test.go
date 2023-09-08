package backup

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestFindNearestDump(t *testing.T) {
	keys := []string{
		"moco/test/test/20210525-112233/dump.tar",
		"moco/test/test/20210525-112233/binlog.tar.zst",
		"moco/test/test/20210525-120001/dump.tar",
		"moco/test/test/garbage",
		"moco/test/test/20210526000000/dump.tar", // invalid
		"moco/test/test/20210526-000000/dump.tar",
		"moco/test/test/20210527-000000/dump.tar",
		"moco/test/test/20210527-000000/binlog.tar.zst",
		"moco/test/test/20210528-000000/dump.tar",
		"moco/test/test/20210528-000000/binlog.tar.zst",
	}

	testCases := []struct {
		name         string
		restorePoint time.Time

		expectDump   string
		expectBinlog string
		expectTime   time.Time
	}{
		{"exact", time.Date(2021, time.May, 26, 0, 0, 0, 0, time.UTC),
			"moco/test/test/20210526-000000/dump.tar", "", time.Date(2021, time.May, 26, 0, 0, 0, 0, time.UTC)},
		{"up-to-date", time.Date(2021, time.May, 26, 1, 0, 0, 0, time.UTC),
			"moco/test/test/20210526-000000/dump.tar", "", time.Date(2021, time.May, 26, 0, 0, 0, 0, time.UTC)},
		{"no-binlog", time.Date(2021, time.May, 25, 13, 0, 0, 0, time.UTC),
			"moco/test/test/20210525-120001/dump.tar", "", time.Date(2021, time.May, 25, 12, 0, 1, 0, time.UTC)},
		{"with-binlog", time.Date(2021, time.May, 25, 11, 22, 33, 0, time.UTC),
			"moco/test/test/20210525-112233/dump.tar", "moco/test/test/20210525-112233/binlog.tar.zst",
			time.Date(2021, time.May, 25, 11, 22, 33, 0, time.UTC)},
		{"not-found", time.Date(2021, time.May, 24, 0, 0, 0, 0, time.UTC), "", "", time.Time{}},
		{"#563", time.Date(2021, time.May, 27, 12, 0, 0, 0, time.UTC),
			"moco/test/test/20210527-000000/dump.tar", "moco/test/test/20210527-000000/binlog.tar.zst",
			time.Date(2021, time.May, 27, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rm := &RestoreManager{
				log:          logr.Discard(),
				restorePoint: tc.restorePoint,
			}
			dump, binlog, bkt := rm.FindNearestDump(keys)
			if dump != tc.expectDump {
				t.Errorf("unexpected dump: %s, expected %s", dump, tc.expectDump)
			}
			if binlog != tc.expectBinlog {
				t.Errorf("unexpected binlog %s, expected %s", binlog, tc.expectBinlog)
			}
			if !bkt.Equal(tc.expectTime) {
				t.Errorf("unexpected backup time %s, expected %s", bkt.String(), tc.expectTime.String())
			}
		})
	}
}
