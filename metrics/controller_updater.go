package metrics

import (
	"github.com/cybozu-go/moco"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
)

func UpdateOperationPhase(clusterName string, phase moco.OperationPhase) {
	for _, labelPhase := range moco.AllOperationPhases {
		if labelPhase == phase {
			operationPhaseMetrics.WithLabelValues(clusterName, string(labelPhase)).Set(1)
		} else {
			operationPhaseMetrics.WithLabelValues(clusterName, string(labelPhase)).Set(0)
		}
	}
}

func IncrementFailoverCountTotalMetrics(clusterName string) {
	failoverCountTotalMetrics.WithLabelValues(clusterName).Inc()
}

func UpdateTotalReplicasMetrics(clusterName string, count int32) {
	totalReplicasMetrics.WithLabelValues(clusterName).Set(float64(count))
}

func UpdateSyncedReplicasMetrics(clusterName string, count *int) {
	if count == nil {
		syncedReplicasMetrics.WithLabelValues(clusterName).Set(0)
		return
	}
	syncedReplicasMetrics.WithLabelValues(clusterName).Set(float64(*count))
}

func UpdateClusterStatusViolationMetrics(clusterName string, status corev1.ConditionStatus) {
	updateClusterStatusMetrics(clusterViolationStatusMetrics, clusterName, status)
}

func UpdateClusterStatusFailureMetrics(clusterName string, status corev1.ConditionStatus) {
	updateClusterStatusMetrics(clusterFailureStatusMetrics, clusterName, status)
}

func UpdateClusterStatusHealthyMetrics(clusterName string, status corev1.ConditionStatus) {
	updateClusterStatusMetrics(clusterHealthyStatusMetrics, clusterName, status)
}

func UpdateClusterStatusAvailableMetrics(clusterName string, status corev1.ConditionStatus) {
	updateClusterStatusMetrics(clusterAvailableStatusMetrics, clusterName, status)
}

func updateClusterStatusMetrics(target *prometheus.GaugeVec, clusterName string, status corev1.ConditionStatus) {
	for _, c := range []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionTrue, corev1.ConditionUnknown} {
		if status == c {
			target.WithLabelValues(clusterName, string(c)).Set(1)
			continue
		}
		target.WithLabelValues(clusterName, string(c)).Set(0)
	}
}
