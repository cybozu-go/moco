package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsNamespace    = "moco"
	clusteringSubsystem = "cluster"
)

// Clustering related metrics
var (
	CheckCountVec      *prometheus.CounterVec
	ErrorCountVec      *prometheus.CounterVec
	AvailableVec       *prometheus.GaugeVec
	HealthyVec         *prometheus.GaugeVec
	SwitchoverCountVec *prometheus.CounterVec
	FailoverCountVec   *prometheus.CounterVec
	TotalReplicasVec   *prometheus.GaugeVec
	ReadyReplicasVec   *prometheus.GaugeVec
	ErrantReplicasVec  *prometheus.GaugeVec
)

// Register registers Prometheus metrics vectors to the registry.
func Register(registry prometheus.Registerer) {
	CheckCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "checks_total",
		Help:      "The number of times MOCO has checked the cluster",
	}, []string{"name", "namespace"})
	registry.MustRegister(CheckCountVec)

	ErrorCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "errors_total",
		Help:      "The number of times MOCO has got errors from the cluster",
	}, []string{"name", "namespace"})
	registry.MustRegister(ErrorCountVec)

	AvailableVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "available",
		Help:      "The cluster status about available condition",
	}, []string{"name", "namespace"})
	registry.MustRegister(AvailableVec)

	HealthyVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "healthy",
		Help:      "The cluster status about healthy condition",
	}, []string{"name", "namespace"})
	registry.MustRegister(HealthyVec)

	SwitchoverCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "switchover_total",
		Help:      "The total count of switchover in the cluster",
	}, []string{"name", "namespace"})
	registry.MustRegister(SwitchoverCountVec)

	FailoverCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "failover_total",
		Help:      "The total count of failover in the cluster",
	}, []string{"name", "namespace"})
	registry.MustRegister(FailoverCountVec)

	TotalReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "replicas",
		Help:      "The number of instances in the cluster",
	}, []string{"name", "namespace"})
	registry.MustRegister(TotalReplicasVec)

	ReadyReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "ready_replicas",
		Help:      "The number of ready Pods in the cluster",
	}, []string{"name", "namespace"})
	registry.MustRegister(ReadyReplicasVec)

	ErrantReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "errant_replicas",
		Help:      "The number of instances that have errant transactions",
	}, []string{"name", "namespace"})
	registry.MustRegister(ErrantReplicasVec)
}
