package agent

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/well"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func testAgentClone() {
	It("should return 500 if token is invalid", func() {

	})

	It("should return 400 with bad requests", func() {
		By("preparing agent")
		agent := New(replicaHost, token, password, password, replicaPort,
			&accessor.MySQLAccessorConfig{
				ConnMaxLifeTime:   30 * time.Minute,
				ConnectionTimeout: 3 * time.Second,
				ReadTimeout:       30 * time.Second,
			},
		)

		By("passing invalid token")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries := url.Values{
			moco.CloneParamDonorHostName: []string{donorHost},
			moco.CloneParamDonorPort:     []string{strconv.Itoa(donorPort)},
			moco.AgentTokenParam:         []string{"invalid-token"},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))

		By("passing empty donorHostName")
		req = httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries = url.Values{
			moco.CloneParamDonorPort: []string{strconv.Itoa(donorPort)},
			moco.AgentTokenParam:     []string{"token"},
		}
		req.URL.RawQuery = queries.Encode()

		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))

		By("passing empty donorPort")
		req = httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries = url.Values{
			moco.CloneParamDonorHostName: []string{donorHost},
			moco.AgentTokenParam:         []string{"invalid-token"},
		}
		req.URL.RawQuery = queries.Encode()

		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))
	})

	It("should clone from donor successfly and should fail once cloned", func() {
		By("preparing agent")
		agent := New(replicaHost, token, password, password, replicaPort,
			&accessor.MySQLAccessorConfig{
				ConnMaxLifeTime:   30 * time.Minute,
				ConnectionTimeout: 3 * time.Second,
				ReadTimeout:       30 * time.Second,
			},
		)

		By("cloning from donor")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries := url.Values{
			moco.CloneParamDonorHostName: []string{donorHost},
			moco.CloneParamDonorPort:     []string{strconv.Itoa(donorPort)},
			moco.AgentTokenParam:         []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusOK))

		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusTooManyRequests))

		well.Stop()
		err := well.Wait()
		Expect(err).ShouldNot(HaveOccurred())

		Eventually(func() error {
			db, err := agent.acc.Get(replicaHost+":"+strconv.Itoa(replicaPort), moco.MiscUser, password)
			if err != nil {
				return err
			}

			cloneStatus, err := accessor.GetMySQLCloneStateStatus(context.Background(), db)
			if err != nil {
				return err
			}

			expected := sql.NullString{Valid: true, String: "Completed"}
			if !cmp.Equal(cloneStatus.State, expected) {
				return fmt.Errorf("doesn't reach completed state: %+v", cloneStatus.State)
			}
			return nil
		}, 30*time.Second).Should(Succeed())

		By("challenging cloning again")
		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusForbidden))
	})
}
