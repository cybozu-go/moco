package metrics

import (
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
)

func TestOperationPhaseMetricsUpdater(t *testing.T) {
	const clusterName = "testcluster"

	tests := []struct {
		name  string
		input moco.OperationPhase
	}{
		{
			name:  "first update",
			input: moco.PhaseInitializing,
		},
		{
			name:  "next update",
			input: moco.PhaseCompleted,
		},
		{
			name:  "same update",
			input: moco.PhaseCompleted,
		},
	}

	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			UpdateOperationPhase(clusterName, tt.input)

			metricsFamily, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}

			for _, mf := range metricsFamily {
				if *mf.Name == "moco_controller_operation_phase" {
					found := false
					for _, met := range mf.Metric {
						m := labelToMap(met.Label)
						if m["cluster_name"] == clusterName {
							phase := m["phase"]
							if phase == string(tt.input) {
								if *met.Gauge.Value != 1.0 {
									t.Errorf("metric value is not 1: phase=%#v", phase)
								}
								found = true
							} else {
								if *met.Gauge.Value != 0.0 {
									t.Errorf("metric value is not 0: phase=%#v", phase)
								}
							}
						}
					}
					if !found {
						t.Errorf("could not find metrics whose value is 1")
					}
				} else {
					t.Errorf("unknown metrics name: %s", *mf.Name)
				}
			}
		})
	}
}

func TestSyncedReplicasMetricsUpdater(t *testing.T) {
	const clusterName = "testcluster"

	tests := []struct {
		name  string
		input *int
	}{
		{
			name:  "first update",
			input: intPointer(10),
		},
		{
			name:  "nil update",
			input: nil,
		},
		{
			name:  "next update",
			input: intPointer(20),
		},
	}

	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			UpdateSyncedReplicasMetrics(clusterName, tt.input)

			metricsFamily, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}

			for _, mf := range metricsFamily {
				if *mf.Name == "moco_controller_synced_replicas" {
					for _, met := range mf.Metric {
						m := labelToMap(met.Label)
						if m["cluster_name"] == clusterName {
							value := *met.Gauge.Value
							if tt.input == nil {
								if value != 0.0 {
									t.Errorf("unexpected metric value: expected = 0, actual = %f", value)
								}
							} else {
								if value != float64(*tt.input) {
									t.Errorf("unexpected metric value: expected = %d, actual = %f", *tt.input, value)
								}
							}
						}
					}
				} else {
					t.Errorf("unknown metrics name: %s", *mf.Name)
				}
			}
		})
	}
}

func TestClusterStatusMetricsUpdater(t *testing.T) {
	const clusterName = "testcluster"

	tests := []struct {
		name  string
		input corev1.ConditionStatus
	}{
		{
			name:  "first update",
			input: corev1.ConditionTrue,
		},
		{
			name:  "next update",
			input: corev1.ConditionUnknown,
		},
		{
			name:  "same update",
			input: corev1.ConditionUnknown,
		},
	}

	type nameAndFunc struct {
		f    func(clusterName string, status corev1.ConditionStatus)
		name string
	}
	for _, nf := range []nameAndFunc{
		{UpdateClusterStatusViolationMetrics, "moco_controller_cluster_violation_status"},
		{UpdateClusterStatusFailureMetrics, "moco_controller_cluster_failure_status"},
		{UpdateClusterStatusHealthyMetrics, "moco_controller_cluster_healthy_status"},
		{UpdateClusterStatusAvailableMetrics, "moco_controller_cluster_available_status"},
	} {
		registry := prometheus.NewRegistry()
		RegisterMetrics(registry)

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				nf.f(clusterName, tt.input)

				metricsFamily, err := registry.Gather()
				if err != nil {
					t.Fatal(err)
				}

				for _, mf := range metricsFamily {
					if *mf.Name == nf.name {
						found := false
						for _, met := range mf.Metric {
							m := labelToMap(met.Label)
							if m["cluster_name"] == clusterName {
								status := m["status"]
								if status == string(tt.input) {
									if *met.Gauge.Value != 1.0 {
										t.Errorf("metric value is not 1: status=%#v", status)
									}
									found = true
								} else {
									if *met.Gauge.Value != 0.0 {
										t.Errorf("metric value is not 0: status=%#v", status)
									}
								}
							}
						}
						if !found {
							t.Errorf("could not find metrics whose value is 1")
						}
					} else {
						t.Errorf("unknown metrics name: %s", *mf.Name)
					}
				}
			})
		}
	}
}

func intPointer(i int) *int {
	return &i
}

func labelToMap(labelPair []*dto.LabelPair) map[string]string {
	res := make(map[string]string)
	for _, l := range labelPair {
		res[*l.Name] = *l.Value
	}
	return res
}
