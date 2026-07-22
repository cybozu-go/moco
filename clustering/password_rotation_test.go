package clustering

import (
	"testing"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
)

func TestNeedsSuperReadOnlyOff(t *testing.T) {
	src := "external-source"
	testCases := []struct {
		name         string
		idx          int
		primaryIndex int
		sourceSecret *string
		want         bool
	}{
		{
			name:         "normal cluster, primary — writable, no toggle needed",
			idx:          0,
			primaryIndex: 0,
			sourceSecret: nil,
			want:         false,
		},
		{
			name:         "normal cluster, replica — super_read_only=1, toggle needed",
			idx:          1,
			primaryIndex: 0,
			sourceSecret: nil,
			want:         true,
		},
		{
			name:         "intermediate primary cluster, primary — super_read_only=1, toggle needed",
			idx:          0,
			primaryIndex: 0,
			sourceSecret: &src,
			want:         true,
		},
		{
			name:         "intermediate primary cluster, replica — super_read_only=1, toggle needed",
			idx:          1,
			primaryIndex: 0,
			sourceSecret: &src,
			want:         true,
		},
		{
			name:         "normal cluster, primary index != 0",
			idx:          2,
			primaryIndex: 2,
			sourceSecret: nil,
			want:         false,
		},
		{
			name:         "intermediate primary cluster, primary index != 0",
			idx:          2,
			primaryIndex: 2,
			sourceSecret: &src,
			want:         true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &mocov1beta2.MySQLCluster{}
			cluster.Spec.ReplicationSourceSecretName = tc.sourceSecret
			got := needsSuperReadOnlyOff(tc.idx, tc.primaryIndex, cluster)
			if got != tc.want {
				t.Errorf("needsSuperReadOnlyOff(idx=%d, primary=%d, sourceSecret=%v) = %v, want %v",
					tc.idx, tc.primaryIndex, tc.sourceSecret, got, tc.want)
			}
		})
	}
}
