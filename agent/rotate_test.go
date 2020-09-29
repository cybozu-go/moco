package agent

import (
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
	var agent = New(replicaHost, token, password, password, replicaPort,
		&accessor.MySQLAccessorConfig{
			ConnMaxLifeTime:   30 * time.Minute,
			ConnectionTimeout: 3 * time.Second,
			ReadTimeout:       30 * time.Second,
		},
	)

	It("should return 400 with bad requests", func() {
		By("passing invalid token")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/rotate", nil)
		queries := url.Values{
			moco.AgentTokenParam: []string{"invalid-token"},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))
	})

	It("should rotate log files", func() {
		By("preparing log files for testing")
		slowFile := filepath.Join(moco.VarLogPath, moco.MySQLSlowLogName)
		errFile := filepath.Join(moco.VarLogPath, moco.MySQLErrorLogName)
		logFiles := []string{slowFile, errFile}

		defer func() {
			for _, file := range logFiles {
				os.Remove(file)
				os.Remove(file + ".0")
			}
		}()

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
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusOK))

		for _, file := range logFiles {
			_, err := os.Stat(file + ".0")
			Expect(err).ShouldNot(HaveOccurred())
		}
	})
}
