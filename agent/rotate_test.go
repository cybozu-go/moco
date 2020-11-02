package agent

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/metrics"
	"github.com/cybozu-go/moco/test_utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

func testRotate() {
	var tmpDir string
	var agent *Agent
	var registry *prometheus.Registry

	BeforeEach(func() {
		var err error
		tmpDir, err = ioutil.TempDir("", "moco-test-agent-")
		Expect(err).ShouldNot(HaveOccurred())
		agent = New(host, token, password, password, replicationSourceSecretPath, tmpDir, replicaPort,
			&accessor.MySQLAccessorConfig{
				ConnMaxLifeTime:   30 * time.Minute,
				ConnectionTimeout: 3 * time.Second,
				ReadTimeout:       30 * time.Second,
			},
		)

		registry = prometheus.NewRegistry()
		metrics.RegisterAgentMetrics(registry)
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("should return 400 with bad requests", func() {
		By("passing invalid token")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/rotate", nil)
		queries := url.Values{
			moco.AgentTokenParam: []string{"invalid-token"},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.RotateLog(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))
	})

	It("should rotate log files", func() {
		err := test_utils.StartMySQLD(donorHost, donorPort, donorServerID)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.StartMySQLD(replicaHost, replicaPort, replicaServerID)
		Expect(err).ShouldNot(HaveOccurred())

		err = test_utils.InitializeMySQL(donorPort)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.InitializeMySQL(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

		defer func() {
			test_utils.StopAndRemoveMySQLD(donorHost)
			test_utils.StopAndRemoveMySQLD(replicaHost)
		}()

		By("preparing log files for testing")
		slowFile := filepath.Join(tmpDir, moco.MySQLSlowLogName)
		errFile := filepath.Join(tmpDir, moco.MySQLErrorLogName)
		logFiles := []string{slowFile, errFile}

		for _, file := range logFiles {
			_, err := os.Create(file)
			Expect(err).ShouldNot(HaveOccurred())
		}

		By("calling rotate API")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/rotate", nil)
		queries := url.Values{
			moco.AgentTokenParam: []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.RotateLog(res, req)
		body, err := ioutil.ReadAll(res.Body)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(res).Should(HaveHTTPStatus(http.StatusOK), "body: %s", body)

		for _, file := range logFiles {
			_, err := os.Stat(file + ".0")
			Expect(err).ShouldNot(HaveOccurred())
		}
		rotationCount, err := getMetric(registry, metricsPrefix+"log_rotation_count")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*rotationCount.Counter.Value).Should(Equal(1.0))
		rotationFailureCount, err := getMetric(registry, metricsPrefix+"log_rotation_failure_count")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*rotationFailureCount.Counter.Value).Should(Equal(0.0))
		rotationDurationSeconds, err := getMetric(registry, metricsPrefix+"log_rotation_duration_seconds")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(len(rotationDurationSeconds.Summary.Quantile)).ShouldNot(Equal(0))

		By("creating the same name directory")
		for _, file := range logFiles {
			err := os.Rename(file+".0", file)
			Expect(err).ShouldNot(HaveOccurred())
			err = os.Mkdir(file+".0", 0777)
			Expect(err).ShouldNot(HaveOccurred())
		}

		res = httptest.NewRecorder()
		agent.RotateLog(res, req)
		body, err = ioutil.ReadAll(res.Body)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(res).Should(HaveHTTPStatus(http.StatusInternalServerError), "body: %s", body)
		rotationCount, err = getMetric(registry, metricsPrefix+"log_rotation_count")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*rotationCount.Counter.Value).Should(Equal(2.0))
		rotationFailureCount, err = getMetric(registry, metricsPrefix+"log_rotation_failure_count")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*rotationFailureCount.Counter.Value).Should(Equal(1.0))
	})
}
