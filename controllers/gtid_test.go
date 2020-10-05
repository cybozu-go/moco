package controllers

import (
	"reflect"
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/google/go-cmp/cmp"
)

func TestParseGTIDSet(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    MySQLGTIDSet
		wantErr error
	}{
		{
			name:  "Single source and single transaction",
			input: "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
			want: MySQLGTIDSet{
				"3E11FA47-71CA-11E1-9E33-C80AA9429562": []Interval{{23, 23}},
			},
			wantErr: nil,
		},
		{
			name:  "Single source and a sequence of transactions",
			input: "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-5",
			want: MySQLGTIDSet{
				"3E11FA47-71CA-11E1-9E33-C80AA9429562": []Interval{{1, 5}},
			},
			wantErr: nil,
		},
		{
			name:  "Single source and multiple sequences of transactions",
			input: "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-3:11:47-49",
			want: MySQLGTIDSet{
				"3E11FA47-71CA-11E1-9E33-C80AA9429562": []Interval{{1, 3}, {11, 11}, {47, 49}},
			},
			wantErr: nil,
		},
		{
			name:  "Multiple sources",
			input: "2174B383-5441-11E8-B90A-C80AA9429562:1-3, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			want: MySQLGTIDSet{
				"2174B383-5441-11E8-B90A-C80AA9429562": []Interval{{1, 3}},
				"24DA167-0C0C-11E8-8442-00059A3C7B00":  []Interval{{1, 19}},
			},
			wantErr: nil,
		},
		{
			name:    "Empty string",
			input:   "",
			want:    make(MySQLGTIDSet),
			wantErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGTIDSet(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				diff := cmp.Diff(tt.want, got)
				t.Errorf("diff: %s", diff)
			}
			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("wantErr: %v, err: nil", tt.wantErr)
				}
				if tt.wantErr.Error() != err.Error() {
					t.Errorf("wantErr: %v, err: %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name    string
		set1    string
		set2    string
		want    int
		wantErr error
	}{
		{
			name:    "Equal",
			set1:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
			set2:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
			want:    0,
			wantErr: nil,
		},
		{
			name:    "set1 is ahead of set2 (single transaction)",
			set1:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:24",
			set2:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
			want:    1,
			wantErr: nil,
		},
		{
			name:    "set1 is behind of set2 (single transaction)",
			set1:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
			set2:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:24",
			want:    -1,
			wantErr: nil,
		},
		{
			name:    "set1 is ahead of set2 (multiple transaction)",
			set1:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-24",
			set2:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-23",
			want:    1,
			wantErr: nil,
		},
		{
			name:    "set1 is behind of set2 (multiple transaction)",
			set1:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-24",
			set2:    "3E11FA47-71CA-11E1-9E33-C80AA9429562:25",
			want:    -1,
			wantErr: nil,
		},
		{
			name:    "set1 is ahead of set2 (multiple source & transaction)",
			set1:    "2174B383-5441-11E8-B90A-C80AA9429562:1-4, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			set2:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			want:    1,
			wantErr: nil,
		},
		{
			name:    "set1 is behind of set2 (multiple source & transaction)",
			set1:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			set2:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-20",
			want:    -1,
			wantErr: nil,
		},
		{
			name:    "set1 is ahead of set2 (set1 includes set2)",
			set1:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			set2:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3",
			want:    1,
			wantErr: nil,
		},
		{
			name:    "cannot compare because of more than two different transactions",
			set1:    "2174B383-5441-11E8-B90A-C80AA9429562:1-4, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-20",
			set2:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			want:    1,
			wantErr: nil,
		},
		{
			name:    "set1 is ahead of set2 (multiple source and set2 is empty)",
			set1:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3, 24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			set2:    "",
			want:    1,
			wantErr: nil,
		},
		{
			name:    "cannot comapre because of more than one different source and transaction",
			set1:    "2174B383-5441-11E8-B90A-C80AA9429562:1-3",
			set2:    "24DA167-0C0C-11E8-8442-00059A3C7B00:1-19",
			want:    0,
			wantErr: moco.ErrCannotCompareGITDs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set1GTID, err := ParseGTIDSet(tt.set1)
			if err != nil {
				t.Error(err)
			}
			set2GTID, err := ParseGTIDSet(tt.set2)
			if err != nil {
				t.Error(err)
			}
			got, err := CompareGTIDSet(set1GTID, set2GTID)
			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("wantErr: %v, err: nil", tt.wantErr)
				}
				if tt.wantErr.Error() != err.Error() {
					t.Errorf("wantErr: %v, err: %v", tt.wantErr, err)
				}
			}
			if got != tt.want {
				t.Errorf("Compare(%s, %s) want: %v, got: %v", tt.set1, tt.set2, tt.want, got)
			}
		})
	}
}
