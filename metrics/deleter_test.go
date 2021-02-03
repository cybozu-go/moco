package metrics

import (
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
)

func TestDeleteAllMetrics(t *testing.T) {
	const (
		clusterName        = "testcluster"
		anotherClusterName = "anothercluster"
	)

	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)

	for _, c := range []string{clusterName, anotherClusterName} {
		UpdateOperationPhase(c, moco.PhaseCompleted)
		UpdateTotalReplicasMetrics(c, 3)
		UpdateSyncedReplicasMetrics(c, intPointer(2))
		IncrementFailoverCountTotalMetrics(c)
		for _, f := range []func(c string, status corev1.ConditionStatus){
			UpdateClusterStatusViolationMetrics,
			UpdateClusterStatusFailureMetrics,
			UpdateClusterStatusHealthyMetrics,
			UpdateClusterStatusAvailableMetrics,
		} {
			f(c, corev1.ConditionTrue)
		}
	}

	beforeDelete, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	DeleteAllControllerMetrics(clusterName)

	afterDelete, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	expected := []*dto.MetricFamily{}
	for _, mf := range beforeDelete {
		var toAdd []*dto.Metric
		for _, met := range mf.Metric {
			m := labelToMap(met.Label)
			if m["cluster_name"] != clusterName {
				toAdd = append(toAdd, met)
			}
		}
		if len(toAdd) > 0 {
			mfAdd := mf
			mfAdd.Metric = toAdd
			expected = append(expected, mfAdd)
		}
	}

	diff := cmp.Diff(expected, afterDelete)
	if diff != "" {
		t.Errorf("deletion was failed: diff=%+v", diff)
	}
}
