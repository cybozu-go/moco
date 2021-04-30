package clustering

import (
	"errors"
	"fmt"

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

var _ types.GomegaMatcher = &opMetrics{}

func (m *opMetrics) Match(actual interface{}) (success bool, err error) {
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
	}
	return false, fmt.Errorf("unsupported operator %s", m.op)
}

func (m *opMetrics) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("given metrics is not %s %f", m.op, m.val)
}

func (m *opMetrics) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("given metrics is %s %f", m.op, m.val)
}
