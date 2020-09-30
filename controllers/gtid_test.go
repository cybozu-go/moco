package controllers

import (
	"errors"
	"reflect"
	"testing"

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
			name:  "",
			input: "3E11FA47-71CA-11E1-9E33-C80AA9429562:23",
			want: MySQLGTIDSet{
				"3E11FA47-71CA-11E1-9E33-C80AA9429562": []Interval{{23, 23}},
			},
			wantErr: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseGTIDSet(test.input)
			if !reflect.DeepEqual(got, test.want) {
				diff := cmp.Diff(test.want, got)
				t.Errorf("diff: %s", diff)
			}
			if !errors.Is(err, test.wantErr) {
				t.Errorf("error = %v, want = %v", err, test.wantErr)
			}
		})
	}
}
