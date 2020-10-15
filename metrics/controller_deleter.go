package metrics

import (
	"github.com/cybozu-go/moco"
	corev1 "k8s.io/api/core/v1"
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
	for _, c := range []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionTrue, corev1.ConditionUnknown} {
		clusterViolationStatusMetrics.DeleteLabelValues(clusterName, string(c))
	}
}

func deleteClusterStatusFailureMetrics(clusterName string) {
	for _, c := range []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionTrue, corev1.ConditionUnknown} {
		clusterFailureStatusMetrics.DeleteLabelValues(clusterName, string(c))
	}
}

func deleteClusterStatusHealthyMetrics(clusterName string) {
	for _, c := range []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionTrue, corev1.ConditionUnknown} {
		clusterHealthyStatusMetrics.DeleteLabelValues(clusterName, string(c))
	}
}

func deleteClusterStatusAvailableMetrics(clusterName string) {
	for _, c := range []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionTrue, corev1.ConditionUnknown} {
		clusterAvailableStatusMetrics.DeleteLabelValues(clusterName, string(c))
	}
}
