package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsAgentSubsystem = "agent"
)

var (
	cloneCountMetrics                 prometheus.Counter
	cloneFailureCountMetrics          prometheus.Counter
	cloneDurationSecondsMetrics       prometheus.Summary
	logRotationCountMetrics           prometheus.Counter
	logRotationFailureCountMetrics    prometheus.Counter
	logRotationDurationSecondsMetrics prometheus.Summary
)

func RegisterAgentMetrics(registry *prometheus.Registry) {
	cloneCountMetrics = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsAgentSubsystem,
		Name:      "clone_count",
		Help:      "The clone operation count",
	})
	cloneFailureCountMetrics = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsAgentSubsystem,
		Name:      "clone_count",
		Help:      "The clone operation count",
	})
	cloneDurationSecondsMetrics = prometheus.NewSummary(prometheus.SummaryOpts{
		Namespace:  metricsNamespace,
		Subsystem:  metricsAgentSubsystem,
		Name:       "clone_duration_seconds",
		Help:       "The time took to clone operation",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})

	logRotationCountMetrics = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsAgentSubsystem,
		Name:      "log_rotation_count",
		Help:      "The log rotation operation count",
	})
	logRotationFailureCountMetrics = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsAgentSubsystem,
		Name:      "log_rotation_count",
		Help:      "The logRotation operation count",
	})
	logRotationDurationSecondsMetrics = prometheus.NewSummary(prometheus.SummaryOpts{
		Namespace:  metricsNamespace,
		Subsystem:  metricsAgentSubsystem,
		Name:       "log_rotation_duration_seconds",
		Help:       "The time took to log rotation operation",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})

	registry.MustRegister(
		cloneCountMetrics,
		cloneFailureCountMetrics,
		cloneDurationSecondsMetrics,
		logRotationCountMetrics,
		logRotationFailureCountMetrics,
		logRotationDurationSecondsMetrics,
	)
}
