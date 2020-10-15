package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsNamespace = "moco"
	metricsSubsystem = "controller"
)

var (
	clusterViolationStatusMetrics *prometheus.GaugeVec
	clusterFailureStatusMetrics   *prometheus.GaugeVec
	clusterAvailableStatusMetrics *prometheus.GaugeVec
	clusterHealthyStatusMetrics   *prometheus.GaugeVec
	operationPhaseMetrics         *prometheus.GaugeVec
	failoverCountTotalMetrics     *prometheus.CounterVec
	totalReplicasMetrics          *prometheus.GaugeVec
	syncedReplicasMetrics         *prometheus.GaugeVec
)

func RegisterControllerMetrics(registry *prometheus.Registry) {
	clusterViolationStatusMetrics = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "cluster_violation_status",
		Help:      "The cluster status about violation condition",
	}, []string{"cluster_name", "status"})
	registry.MustRegister(clusterViolationStatusMetrics)

	clusterFailureStatusMetrics = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "cluster_failure_status",
		Help:      "The cluster status about failure condition",
	}, []string{"cluster_name", "status"})
	registry.MustRegister(clusterFailureStatusMetrics)

	clusterAvailableStatusMetrics = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "cluster_available_status",
		Help:      "The cluster status about available condition",
	}, []string{"cluster_name", "status"})
	registry.MustRegister(clusterAvailableStatusMetrics)

	clusterHealthyStatusMetrics = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "cluster_healthy_status",
		Help:      "The cluster status about healthy condition",
	}, []string{"cluster_name", "status"})
	registry.MustRegister(clusterHealthyStatusMetrics)

	operationPhaseMetrics = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "operation_phase",
		Help:      "The operation is in the labeled phase or not",
	}, []string{"cluster_name", "phase"})
	registry.MustRegister(operationPhaseMetrics)

	failoverCountTotalMetrics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "failover_count_total",
		Help:      "The failover count.",
	}, []string{"cluster_name"})
	registry.MustRegister(failoverCountTotalMetrics)

	totalReplicasMetrics = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "total_replicas",
		Help:      "The number of replicas.",
	}, []string{"cluster_name"})
	registry.MustRegister(totalReplicasMetrics)

	syncedReplicasMetrics = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Name:      "synced_replicas",
		Help:      "The number of replicas which are in synced state.",
	}, []string{"cluster_name"})
	registry.MustRegister(syncedReplicasMetrics)
}
