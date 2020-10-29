package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/metrics"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

func testClone() {
	var agent *Agent
	var registry *prometheus.Registry

	BeforeEach(func() {
		err := moco.StartMySQLD(donorHost, donorPort, donorServerID)
		Expect(err).ShouldNot(HaveOccurred())
		err = moco.StartMySQLD(replicaHost, replicaPort, replicaServerID)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.InitializeMySQL(donorPort)
		Expect(err).ShouldNot(HaveOccurred())
		err = moco.InitializeMySQL(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.PrepareTestData(donorPort)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.SetValidDonorList(replicaPort, donorHost, donorPort)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.ResetMaster(donorPort)
		Expect(err).ShouldNot(HaveOccurred())
		err = moco.ResetMaster(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

		agent = New(host, token, password, password, replicationSourceSecretPath, "", replicaPort,
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
		moco.StopAndRemoveMySQLD(donorHost)
		moco.StopAndRemoveMySQLD(replicaHost)
	})

	It("should return 400 with bad requests", func() {
		By("initializing metrics registry")
		registry := prometheus.NewRegistry()
		metrics.RegisterAgentMetrics(registry)

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
			moco.AgentTokenParam:     []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))

		By("passing empty donorPort")
		req = httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries = url.Values{
			moco.CloneParamDonorHostName: []string{donorHost},
			moco.AgentTokenParam:         []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))

		By("passing invalid donorHostName")
		req = httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries = url.Values{
			moco.CloneParamDonorHostName: []string{"invalid-host-name"},
			moco.CloneParamDonorPort:     []string{strconv.Itoa(donorPort)},
			moco.AgentTokenParam:         []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusOK))

		Eventually(func() error {
			cloneCount, err := getMetric(registry, metricsPrefix+"clone_count")
			if err != nil {
				return err
			}
			if *cloneCount.Counter.Value != 1.0 {
				return errors.New("clone_count does not increase yet")
			}

			cloneFailureCount, err := getMetric(registry, metricsPrefix+"clone_failure_count")
			if err != nil {
				return err
			}
			if *cloneFailureCount.Counter.Value != 1.0 {
				return errors.New("clone_failure_count does not increase yet")
			}

			return nil
		}, 30*time.Second).Should(Succeed())
	})

	It("should clone from donor successfully", func() {
		By("initializing metrics registry")
		registry := prometheus.NewRegistry()
		metrics.RegisterAgentMetrics(registry)

		cloneCount, err := getMetric(registry, metricsPrefix+"clone_count")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*cloneCount.Counter.Value).Should(Equal(0.0))

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

		Eventually(func() error {
			db, err := agent.acc.Get(host+":"+strconv.Itoa(replicaPort), moco.MiscUser, password)
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

			cloneCount, err = getMetric(registry, metricsPrefix+"clone_count")
			if err != nil {
				return err
			}
			if *cloneCount.Counter.Value != 1.0 {
				return errors.New("clone count isn't incremented yet")
			}

			cloneFailureCount, err := getMetric(registry, metricsPrefix+"clone_failure_count")
			if err != nil {
				return err
			}
			if *cloneFailureCount.Counter.Value != 0.0 {
				return errors.New("clone failure count shouldn't be incremented")
			}

			cloneDurationSeconds, err := getMetric(registry, metricsPrefix+"clone_duration_seconds")
			if err != nil {
				return err
			}
			if len(cloneDurationSeconds.Summary.Quantile) == 0 {
				return errors.New("clone duration seconds should have values")
			}

			return nil
		}, 30*time.Second).Should(Succeed())
	})

	It("should not clone if recipient has some data", func() {
		By("write data to recipient")
		err := moco.PrepareTestData(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

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
		Expect(res).Should(HaveHTTPStatus(http.StatusForbidden))
	})

	It("should clone from external MySQL", func() {
		By("cloning from external MySQL")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries := url.Values{
			moco.CloneParamExternal: []string{"true"},
			moco.AgentTokenParam:    []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusOK))

		Eventually(func() error {
			db, err := agent.acc.Get(host+":"+strconv.Itoa(replicaPort), moco.MiscUser, password)
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

		By("getting 500 when secret files doesn't exist")
		pwd, err := os.Getwd()
		Expect(err).ShouldNot(HaveOccurred())
		agent = New(host, token, password, password, pwd, "", replicaPort,
			&accessor.MySQLAccessorConfig{
				ConnMaxLifeTime:   30 * time.Minute,
				ConnectionTimeout: 3 * time.Second,
				ReadTimeout:       30 * time.Second,
			},
		)

		req = httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
		queries = url.Values{
			moco.CloneParamExternal: []string{"true"},
			moco.AgentTokenParam:    []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusInternalServerError))
	})
}
