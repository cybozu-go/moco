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
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func testAgentRotate() {
	var tmpDir string
	var agent *Agent

	BeforeEach(func() {
		var err error
		tmpDir, err = ioutil.TempDir("", "moco-test-agent-")
		Expect(err).ShouldNot(HaveOccurred())
		agent = New(replicaHost, token, password, password, tmpDir, replicaPort,
			&accessor.MySQLAccessorConfig{
				ConnMaxLifeTime:   30 * time.Minute,
				ConnectionTimeout: 3 * time.Second,
				ReadTimeout:       30 * time.Second,
			},
		)
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
	})
}
