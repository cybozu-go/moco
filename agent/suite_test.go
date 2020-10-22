package agent

import (
	"fmt"
	"log" // restrictpkg:ignore to suppress mysql client logs.
	"testing"

	"github.com/cybozu-go/moco"
	"github.com/go-sql-driver/mysql"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	promgo "github.com/prometheus/client_model/go"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	password      = "test-password"
	token         = "dummy-token"
	metricsPrefix = "moco_agent_"
)

func TestAgent(t *testing.T) {
	mysql.SetLogger(mysql.Logger(log.New(GinkgoWriter, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile)))
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Suite")
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	mysql.SetLogger(mysql.Logger(log.New(GinkgoWriter, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile)))

	moco.StopAndRemoveMySQLD(donorHost)
	moco.StopAndRemoveMySQLD(replicaHost)
	moco.RemoveNetwork()

	err := moco.CreateNetwork()
	Expect(err).ShouldNot(HaveOccurred())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	moco.StopAndRemoveMySQLD(donorHost)
	moco.StopAndRemoveMySQLD(replicaHost)
	moco.RemoveNetwork()
})

func getMetric(registry *prometheus.Registry, metricName string) (*promgo.Metric, error) {
	metricsFamily, err := registry.Gather()
	if err != nil {
		return nil, err
	}

	for _, mf := range metricsFamily {
		if *mf.Name == metricName {
			if len(mf.Metric) != 1 {
				return nil, fmt.Errorf("metrics family should have a single metric: name=%s", *mf.Name)
			}
			return mf.Metric[0], nil
		}
	}

	return nil, fmt.Errorf("cannot find a metric: name=%s", metricName)
}

var _ = Describe("Test Agent", func() {
	testAgentHealth()
	testAgentRotate()
})
