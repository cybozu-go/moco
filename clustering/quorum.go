package clustering

import "math"

// RequiredSemiSyncAcks returns the number of replica ACKs the primary should wait for
// to achieve a strict majority given the total number of instances (1 primary + N replicas).
// For instances <= 1 it returns 0.
func RequiredSemiSyncAcks(instances int) int {
	if instances <= 1 {
		return 0
	}
	return int(math.Ceil(float64(instances-1) / 2.0))
}
