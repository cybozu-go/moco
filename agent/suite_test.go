package agent

import (
	"fmt"
	"io/ioutil"
	"log" // restrictpkg:ignore to suppress mysql client logs.
	"os"
	"path"
	"strconv"
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
	token           = "dummy-token"
	metricsPrefix   = "moco_agent_"
	donorHost       = "moco-test-mysqld-donor"
	donorPort       = 3307
	donorServerID   = 1
	replicaHost     = "moco-test-mysqld-replica"
	replicaPort     = 3308
	replicaServerID = 2
)

var replicationSourceSecretPath string

type testData struct {
	primaryHost            string
	primaryPort            int
	primaryUser            string
	primaryPassword        string
	cloneUser              string
	clonePassword          string
	initAfterCloneUser     string
	initAfterClonePassword string
}

func writeTestData(data *testData) {
	writeFile := func(filename, data string) error {
		return ioutil.WriteFile(path.Join(replicationSourceSecretPath, filename), []byte(data), 0664)
	}

	var err error
	err = writeFile("PRIMARY_HOST", data.primaryHost)
	Expect(err).ShouldNot(HaveOccurred())
	err = writeFile("PRIMARY_PORT", strconv.Itoa(data.primaryPort))
	Expect(err).ShouldNot(HaveOccurred())
	err = writeFile("PRIMARY_USER", data.primaryUser)
	Expect(err).ShouldNot(HaveOccurred())
	err = writeFile("PRIMARY_PASSWORD", data.primaryPassword)
	Expect(err).ShouldNot(HaveOccurred())
	err = writeFile("CLONE_USER", data.cloneUser)
	Expect(err).ShouldNot(HaveOccurred())
	err = writeFile("CLONE_PASSWORD", data.clonePassword)
	Expect(err).ShouldNot(HaveOccurred())
	err = writeFile("INIT_AFTER_CLONE_USER", data.initAfterCloneUser)
	Expect(err).ShouldNot(HaveOccurred())
	err = writeFile("INIT_AFTER_CLONE_PASSWORD", data.initAfterClonePassword)
	Expect(err).ShouldNot(HaveOccurred())
}

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
	err = os.RemoveAll(replicationSourceSecretPath)
	Expect(err).ShouldNot(HaveOccurred())
	err = os.Mkdir(replicationSourceSecretPath, 0775)
	Expect(err).ShouldNot(HaveOccurred())

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

	err := os.RemoveAll(replicationSourceSecretPath)
	Expect(err).ShouldNot(HaveOccurred())
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
