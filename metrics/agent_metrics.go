package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsAgentSubsystem = "agent"
)

var (
	cloneCountMetrics           prometheus.Counter
	cloneFailureCountMetrics    prometheus.Counter
	cloneDurationSecondsMetrics prometheus.Summary
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

	registry.MustRegister(
		cloneCountMetrics,
		cloneFailureCountMetrics,
		cloneDurationSecondsMetrics,
	)
}
