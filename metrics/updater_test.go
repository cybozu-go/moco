package metrics

import (
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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

	RegisterMetrics()
	registry := prometheus.NewRegistry()
	registry.MustRegister(operationPhaseMetrics)

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
									t.Errorf("metric value is not set: phase=%#v", phase)
								}
								found = true
							} else {
								if *met.Gauge.Value != 0.0 {
									t.Errorf("metric value is not unset: phase=%#v", phase)
								}
							}
						}
					}
					if !found {
						t.Errorf("could not find metrics whose value is set")
					}
				} else {
					t.Errorf("unknown metrics name: %s", *mf.Name)
				}
			}

		})
	}
}

func labelToMap(labelPair []*dto.LabelPair) map[string]string {
	res := make(map[string]string)
	for _, l := range labelPair {
		res[*l.Name] = *l.Value
	}
	return res
}
