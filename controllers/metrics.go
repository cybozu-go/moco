package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricsNamespace = "moco"
	metricsSubsystem = "controller"
)

var (
	operationPhaseMetrics     *prometheus.GaugeVec
	failoverCountTotalMetrics *prometheus.CounterVec
	totalReplicasMetrics      *prometheus.GaugeVec
	SyncedReplicasMetrics     *prometheus.GaugeVec
)

func RegisterMetrics() {
	operationPhaseMetrics := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "operation_phase",
		Help:      "The operation is in the labeled phase or not",
	}, []string{"cluster_name", "phase"})
	metrics.Registry.MustRegister(operationPhaseMetrics)

	failoverCountTotalMetrics := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "failover_count_total",
		Help:      "The failover count.",
	}, []string{"cluster_name"})
	metrics.Registry.MustRegister(failoverCountTotalMetrics)

	totalReplicas := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "total_replicas",
		Help:      "The number of replicas.",
	}, []string{"cluster_name"})
	metrics.Registry.MustRegister(totalReplicas)

	syncedReplicas := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "synced_replicas",
		Help:      "The number of replicas which are in synced state.",
	}, []string{"cluster_name"})
	metrics.Registry.MustRegister(syncedReplicas)
}
