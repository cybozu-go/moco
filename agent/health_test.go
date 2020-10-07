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
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func testAgentHealth() {
	var agent = New(replicaHost, token, password, password, "", replicaPort,
		&accessor.MySQLAccessorConfig{
			ConnMaxLifeTime:   30 * time.Minute,
			ConnectionTimeout: 3 * time.Second,
			ReadTimeout:       30 * time.Second,
		},
	)

	It("should return 200 if no errors or cloning is not in progress", func() {
		By("getting health")
		res := getHealth(agent)
		Expect(res).Should(HaveHTTPStatus(http.StatusOK))
	})

	It("should return 500 if cloning process is in progress", func() {
		By("executing cloning")
		err := resetMaster(replicaHost, replicaPort)
		Expect(err).ShouldNot(HaveOccurred())
		err = setValidDonorList(replicaHost, replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

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

		By("getting health")
		Eventually(func() error {
			res = getHealth(agent)
			if res.Result().StatusCode != http.StatusInternalServerError {
				return fmt.Errorf("doesn't occur internal server error: %+v", res.Result().Status)
			}
			return nil
		}, 5*time.Second).Should(Succeed())

		By("wating cloning is completed")
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
	})

	It("should return 500 if replica status has IO error", func() {
		By("executing START SLAVE with invalid parameters")
		err := startSlaveWithInvalidSettings(replicaHost, replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

		By("getting health")
		Eventually(func() error {
			res := getHealth(agent)
			if res.Result().StatusCode != http.StatusInternalServerError {
				return fmt.Errorf("doesn't occur internal server error: %+v", res.Result().Status)
			}
			return nil
		}, 10*time.Second).Should(Succeed())
	})
}

func getHealth(agent *Agent) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "http://"+replicaHost+"/health", nil)
	queries := url.Values{
		moco.CloneParamDonorHostName: []string{donorHost},
		moco.CloneParamDonorPort:     []string{strconv.Itoa(donorPort)},
	}
	req.URL.RawQuery = queries.Encode()

	res := httptest.NewRecorder()
	agent.Health(res, req)
	return res
}
