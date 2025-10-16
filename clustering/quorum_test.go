package clustering

import "testing"

func TestRequiredSemiSyncAcks(t *testing.T) {
	cases := []struct{ instances, expectedAcks int }{
		{0, 0}, {1, 0}, {2, 1}, {3, 1}, {4, 2}, {5, 2},
	}

	for _, c := range cases {
		if got := RequiredSemiSyncAcks(c.instances); got != c.expectedAcks {
			t.Fatalf("instances=%d got=%d expected=%d", c.instances, got, c.expectedAcks)
		}
	}
}
