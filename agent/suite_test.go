package agent

import (
	"fmt"
	"log" // restrictpkg:ignore to suppress mysql client logs.
	"os"
	"path"
	"testing"
	"time"

	"github.com/cybozu-go/moco/test_utils"
	"github.com/go-sql-driver/mysql"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	promgo "github.com/prometheus/client_model/go"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	password        = "test-password"
	token           = "dummy-token"
	metricsPrefix   = "moco_agent_"
	host            = "localhost"
	donorHost       = "moco-test-mysqld-donor"
	donorPort       = 3307
	donorServerID   = 1
	replicaHost     = "moco-test-mysqld-replica"
	replicaPort     = 3308
	replicaServerID = 2
)

var replicationSourceSecretPath string

func TestAgent(t *testing.T) {
	mysql.SetLogger(mysql.Logger(log.New(GinkgoWriter, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile)))
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Suite")
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	mysql.SetLogger(mysql.Logger(log.New(GinkgoWriter, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile)))

	var err error
	pwd, err := os.Getwd()
	Expect(err).ShouldNot(HaveOccurred())
	replicationSourceSecretPath = path.Join(pwd, "test_data")

	test_utils.StopAndRemoveMySQLD(donorHost)
	test_utils.StopAndRemoveMySQLD(replicaHost)
	test_utils.RemoveNetwork()

	Eventually(func() error {
		return test_utils.CreateNetwork()
	}, 10*time.Second).Should(Succeed())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	test_utils.StopAndRemoveMySQLD(donorHost)
	test_utils.StopAndRemoveMySQLD(replicaHost)
	test_utils.RemoveNetwork()
})

var _ = Describe("Test Agent", func() {
	Context("health", testHealth)
	Context("rotate", testRotate)
	Context("clone", testClone)
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
