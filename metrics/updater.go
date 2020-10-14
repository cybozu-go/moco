package metrics

import "github.com/cybozu-go/moco"

func UpdateOperationPhase(clusterName string, phase moco.OperationPhase) {
	for _, labelPhase := range moco.AllOperationPhases {
		if labelPhase == phase {
			operationPhaseMetrics.WithLabelValues(clusterName, string(labelPhase)).Set(1)
		} else {
			operationPhaseMetrics.WithLabelValues(clusterName, string(labelPhase)).Set(0)
		}
	}
}

func UpdateFailoverCountTotalMetrics(clusterName string) {
	failoverCountTotalMetrics.WithLabelValues(clusterName).Inc()
}

func UpdateTotalReplicasMetrics(clusterName string, count int32) {
	totalReplicasMetrics.WithLabelValues(clusterName).Set(float64(count))
}

func UpdateSyncedReplicasMetrics(clusterName string, count *int) {
	if count == nil {
		syncedReplicasMetrics.WithLabelValues(clusterName).Set(float64(0))
		return
	}
	syncedReplicasMetrics.WithLabelValues(clusterName).Set(float64(*count))
}
