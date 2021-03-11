package metrics

import (
	"github.com/cybozu-go/moco"
)

func DeleteAllControllerMetrics(clusterName string) {
	deleteOperationPhase(clusterName)
	deleteFailoverCountTotalMetrics(clusterName)
	deleteTotalReplicasMetrics(clusterName)
	deleteSyncedReplicasMetrics(clusterName)
	deleteClusterStatusViolationMetrics(clusterName)
	deleteClusterStatusFailureMetrics(clusterName)
	deleteClusterStatusHealthyMetrics(clusterName)
	deleteClusterStatusAvailableMetrics(clusterName)
}

func deleteOperationPhase(clusterName string) {
	for _, labelPhase := range moco.AllOperationPhases {
		operationPhaseMetrics.DeleteLabelValues(clusterName, string(labelPhase))
	}
}

func deleteFailoverCountTotalMetrics(clusterName string) {
	failoverCountTotalMetrics.DeleteLabelValues(clusterName)
}

func deleteTotalReplicasMetrics(clusterName string) {
	totalReplicasMetrics.DeleteLabelValues(clusterName)
}

func deleteSyncedReplicasMetrics(clusterName string) {
	syncedReplicasMetrics.DeleteLabelValues(clusterName)
}

func deleteClusterStatusViolationMetrics(clusterName string) {
	clusterViolationStatusMetrics.DeleteLabelValues(clusterName)
}

func deleteClusterStatusFailureMetrics(clusterName string) {
	clusterFailureStatusMetrics.DeleteLabelValues(clusterName)
}

func deleteClusterStatusHealthyMetrics(clusterName string) {
	clusterHealthyStatusMetrics.DeleteLabelValues(clusterName)
}

func deleteClusterStatusAvailableMetrics(clusterName string) {
	clusterAvailableStatusMetrics.DeleteLabelValues(clusterName)
}
