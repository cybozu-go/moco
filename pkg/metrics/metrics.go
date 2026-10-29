package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsNamespace    = "moco"
	clusteringSubsystem = "cluster"
	backupSubsystem     = "backup"
)

var metricLabels = []string{"name", "namespace"}

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
	ProcessingTimeVec  *prometheus.HistogramVec

	VolumeResizedTotal            *prometheus.CounterVec
	VolumeResizedErrorTotal       *prometheus.CounterVec
	StatefulSetRecreateTotal      *prometheus.CounterVec
	StatefulSetRecreateErrorTotal *prometheus.CounterVec

	ClusteringStoppedVec     *prometheus.GaugeVec
	ReconciliationStoppedVec *prometheus.GaugeVec

	CurrentReplicasVec             *prometheus.GaugeVec
	UpdatedReplicasVec             *prometheus.GaugeVec
	LastPartitionUpdatedVec        *prometheus.GaugeVec
	PartitionUpdateRetriesTotalVec *prometheus.CounterVec
)

// Backup related metrics
var (
	BackupTimestamp    *prometheus.GaugeVec
	BackupElapsed      *prometheus.GaugeVec
	BackupDumpSize     *prometheus.GaugeVec
	BackupBinlogSize   *prometheus.GaugeVec
	BackupWorkDirUsage *prometheus.GaugeVec
	BackupWarnings     *prometheus.GaugeVec
)

// Register registers Prometheus metrics vectors to the registry.
func Register(registry prometheus.Registerer) {
	CheckCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "checks_total",
		Help:      "The number of times MOCO has checked the cluster",
	}, metricLabels)
	registry.MustRegister(CheckCountVec)

	ErrorCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "errors_total",
		Help:      "The number of times MOCO has got errors from the cluster",
	}, metricLabels)
	registry.MustRegister(ErrorCountVec)

	AvailableVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "available",
		Help:      "The cluster status about available condition",
	}, metricLabels)
	registry.MustRegister(AvailableVec)

	HealthyVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "healthy",
		Help:      "The cluster status about healthy condition",
	}, metricLabels)
	registry.MustRegister(HealthyVec)

	SwitchoverCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "switchover_total",
		Help:      "The total count of switchover in the cluster",
	}, metricLabels)
	registry.MustRegister(SwitchoverCountVec)

	FailoverCountVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "failover_total",
		Help:      "The total count of failover in the cluster",
	}, metricLabels)
	registry.MustRegister(FailoverCountVec)

	TotalReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "replicas",
		Help:      "The number of instances in the cluster",
	}, metricLabels)
	registry.MustRegister(TotalReplicasVec)

	ReadyReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "ready_replicas",
		Help:      "The number of ready Pods in the cluster",
	}, metricLabels)
	registry.MustRegister(ReadyReplicasVec)

	ErrantReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "errant_replicas",
		Help:      "The number of instances that have errant transactions",
	}, metricLabels)
	registry.MustRegister(ErrantReplicasVec)

	ProcessingTimeVec = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "processing_time_seconds",
		Help:      "The length of time in seconds processing the cluster",
		Buckets:   []float64{0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10, 20, 30},
	}, metricLabels)
	registry.MustRegister(ProcessingTimeVec)

	BackupTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: backupSubsystem,
		Name:      "timestamp",
		Help:      "The timestamp of the last successful backup",
	}, metricLabels)
	registry.MustRegister(BackupTimestamp)

	BackupElapsed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: backupSubsystem,
		Name:      "elapsed_seconds",
		Help:      "The time taken for the backup",
	}, metricLabels)
	registry.MustRegister(BackupElapsed)

	BackupDumpSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: backupSubsystem,
		Name:      "dump_bytes",
		Help:      "The size of compressed full backup data",
	}, metricLabels)
	registry.MustRegister(BackupDumpSize)

	BackupBinlogSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: backupSubsystem,
		Name:      "binlog_bytes",
		Help:      "The size of compressed binlog files",
	}, metricLabels)
	registry.MustRegister(BackupBinlogSize)

	BackupWorkDirUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: backupSubsystem,
		Name:      "workdir_usage_bytes",
		Help:      "The maximum usage of the working directory",
	}, metricLabels)
	registry.MustRegister(BackupWorkDirUsage)

	BackupWarnings = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: backupSubsystem,
		Name:      "warnings",
		Help:      "The number of warnings in the last successful backup",
	}, metricLabels)
	registry.MustRegister(BackupWarnings)

	VolumeResizedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "volume_resized_total",
		Help:      "The number of successful volume resizes",
	}, metricLabels)
	registry.MustRegister(VolumeResizedTotal)

	VolumeResizedErrorTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "volume_resized_errors_total",
		Help:      "The number of failed volume resizes",
	}, metricLabels)
	registry.MustRegister(VolumeResizedErrorTotal)

	StatefulSetRecreateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "statefulset_recreate_total",
		Help:      "The number of successful StatefulSet recreates",
	}, metricLabels)
	registry.MustRegister(StatefulSetRecreateTotal)

	StatefulSetRecreateErrorTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "statefulset_recreate_errors_total",
		Help:      "The number of failed StatefulSet recreates",
	}, metricLabels)
	registry.MustRegister(StatefulSetRecreateErrorTotal)

	ClusteringStoppedVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "clustering_stopped",
		Help:      "Indicates if clustering has stopped",
	}, metricLabels)
	registry.MustRegister(ClusteringStoppedVec)

	ReconciliationStoppedVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "reconciliation_stopped",
		Help:      "Indicates if reconciliation has stopped",
	}, metricLabels)
	registry.MustRegister(ReconciliationStoppedVec)

	CurrentReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "current_replicas",
		Help:      "The number of current replicas",
	}, metricLabels)
	registry.MustRegister(CurrentReplicasVec)

	UpdatedReplicasVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "updated_replicas",
		Help:      "The number of updated replicas",
	}, metricLabels)
	registry.MustRegister(UpdatedReplicasVec)

	LastPartitionUpdatedVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "last_partition_updated",
		Help:      "The timestamp of the last successful partition update",
	}, metricLabels)
	registry.MustRegister(LastPartitionUpdatedVec)

	PartitionUpdateRetriesTotalVec = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: clusteringSubsystem,
		Name:      "partition_update_retries_total",
		Help:      "The number of retries for partition updates",
	}, []string{"namespace"})
	registry.MustRegister(PartitionUpdateRetriesTotalVec)
}
