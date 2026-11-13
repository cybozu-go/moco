package clustering

import (
	"errors"
	"fmt"
	"math"

	"github.com/onsi/gomega/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type opMetrics struct {
	op  string
	val float64
}

func MetricsIs(op string, val float64) *opMetrics {
	return &opMetrics{op: op, val: val}
}

// MetricsIsNaN matches a gauge whose value is NaN.
func MetricsIsNaN() *opMetrics {
	return &opMetrics{op: "nan"}
}

var _ types.GomegaMatcher = &opMetrics{}

func (m *opMetrics) Match(actual any) (success bool, err error) {
	c, ok := actual.(prometheus.Collector)
	if !ok {
		return false, errors.New("not a collector")
	}
	val := testutil.ToFloat64(c)
	switch m.op {
	case "==":
		return val == m.val, nil
	case ">":
		return val > m.val, nil
	case ">=":
		return val >= m.val, nil
	case "<":
		return val < m.val, nil
	case "<=":
		return val <= m.val, nil
	case "nan":
		return math.IsNaN(val), nil
	}
	return false, fmt.Errorf("unsupported operator %s", m.op)
}

func (m *opMetrics) FailureMessage(actual any) (message string) {
	return fmt.Sprintf("given metrics is not %s %f", m.op, m.val)
}

func (m *opMetrics) NegatedFailureMessage(actual any) (message string) {
	return fmt.Sprintf("given metrics is %s %f", m.op, m.val)
}
