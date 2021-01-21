package agent

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/metrics"
	"github.com/cybozu-go/moco/test_utils"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

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

func initializeDonorMySQL(isExternal bool) {
	By("initializing MySQL donor")
	err := test_utils.StartMySQLD(donorHost, donorPort, donorServerID)
	Expect(err).ShouldNot(HaveOccurred())
	if isExternal {
		err = test_utils.InitializeMySQLAsExternalDonor(donorPort)
		Expect(err).ShouldNot(HaveOccurred())
	} else {
		err = test_utils.InitializeMySQL(donorPort)
		Expect(err).ShouldNot(HaveOccurred())
	}
	err = test_utils.PrepareTestData(donorPort)
	Expect(err).ShouldNot(HaveOccurred())
	err = test_utils.ResetMaster(donorPort)
	Expect(err).ShouldNot(HaveOccurred())
}

func testClone() {
	var agent *Agent
	var registry *prometheus.Registry

	BeforeEach(func() {
		// The configuration of the donor MySQL is different for each test case.
		// So the donor is not initialized here. The initialization will do at the beginning of each test case.
		By("initializing MySQL replica")
		err := test_utils.StartMySQLD(replicaHost, replicaPort, replicaServerID)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.InitializeMySQL(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.SetValidDonorList(replicaPort, donorHost, donorPort)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.ResetMaster(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

		By("creating agent instance")
		agent = New(test_utils.Host, token, test_utils.MiscUserPassword, test_utils.CloneDonorUserPassword, replicationSourceSecretPath, "", replicaPort,
			&accessor.MySQLAccessorConfig{
				ConnMaxLifeTime:   30 * time.Minute,
				ConnectionTimeout: 3 * time.Second,
				ReadTimeout:       30 * time.Second,
			},
		)

		By("initializing metrics registry")
		registry = prometheus.NewRegistry()
		metrics.RegisterAgentMetrics(registry)

		cloneCount, err := getMetric(registry, metricsPrefix+"clone_count")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*cloneCount.Counter.Value).Should(Equal(0.0))

		cloneFailureCount, err := getMetric(registry, metricsPrefix+"clone_failure_count")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(*cloneFailureCount.Counter.Value).Should(Equal(0.0))

		cloneDurationSeconds, err := getMetric(registry, metricsPrefix+"clone_duration_seconds")
		Expect(err).ShouldNot(HaveOccurred())
		for _, quantile := range cloneDurationSeconds.Summary.Quantile {
			Expect(math.IsNaN(*quantile.Value)).Should(BeTrue())
		}
	})

	AfterEach(func() {
		test_utils.StopAndRemoveMySQLD(donorHost)
		test_utils.StopAndRemoveMySQLD(replicaHost)
	})

	It("should return 400 with bad requests", func() {
		initializeDonorMySQL(false)

		testcases := []struct {
			title  string
			values url.Values
		}{
			{
				title: "passing invalid token",
				values: url.Values{
					moco.CloneParamDonorHostName: []string{donorHost},
					moco.CloneParamDonorPort:     []string{strconv.Itoa(donorPort)},
					moco.AgentTokenParam:         []string{"invalid-token"},
				},
			},
			{
				title: "passing empty token",
				values: url.Values{
					moco.CloneParamDonorHostName: []string{donorHost},
					moco.CloneParamDonorPort:     []string{strconv.Itoa(donorPort)},
				},
			},
			{
				title: "passing empty donorHostName",
				values: url.Values{
					moco.CloneParamDonorPort: []string{strconv.Itoa(donorPort)},
					moco.AgentTokenParam:     []string{token},
				},
			},
			{
				title: "passing empty donorPort",
				values: url.Values{
					moco.CloneParamDonorHostName: []string{donorHost},
					moco.AgentTokenParam:         []string{token},
				},
			},
			{
				title: "passing invalid external param",
				values: url.Values{
					moco.CloneParamExternal: []string{"invalid"},
					moco.AgentTokenParam:    []string{token},
				},
			},
		}

		for _, tt := range testcases {
			By(tt.title)
			req := httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
			req.URL.RawQuery = tt.values.Encode()

			res := httptest.NewRecorder()
			agent.Clone(res, req)
			Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))
		}

		By("checking metrics")
		// In these test cases, the clone will not start actually. So the metrics will not change.
		Eventually(func() error {
			cloneCount, err := getMetric(registry, metricsPrefix+"clone_count")
			if err != nil {
				return err
			}
			if *cloneCount.Counter.Value != 0.0 {
				return fmt.Errorf("clone_count shouldn't be incremented: value=%f", *cloneCount.Counter.Value)
			}

			cloneFailureCount, err := getMetric(registry, metricsPrefix+"clone_failure_count")
			if err != nil {
				return err
			}
			if *cloneFailureCount.Counter.Value != 0.0 {
				return fmt.Errorf("clone_failure_count shouldn't be incremented: value=%f", *cloneFailureCount.Counter.Value)
			}

			return nil
		}, 30*time.Second).Should(Succeed())
	})

	It("should clone from donor successfully", func() {
		initializeDonorMySQL(false)

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

		By("cloning from donor (second time)")
		res = httptest.NewRecorder()
		agent.Clone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusTooManyRequests))

		By("checking result")
		Eventually(func() error {
			db, err := agent.acc.Get(test_utils.Host+":"+strconv.Itoa(replicaPort), moco.MiscUser, test_utils.MiscUserPassword)
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

			cloneCount, err := getMetric(registry, metricsPrefix+"clone_count")
			if err != nil {
				return err
			}
			if *cloneCount.Counter.Value != 1.0 {
				return fmt.Errorf("clone_count isn't incremented yet: value=%f", *cloneCount.Counter.Value)
			}

			cloneFailureCount, err := getMetric(registry, metricsPrefix+"clone_failure_count")
			if err != nil {
				return err
			}
			if *cloneFailureCount.Counter.Value != 0.0 {
				return fmt.Errorf("clone_failure_count shouldn't be incremented: value=%f", *cloneFailureCount.Counter.Value)
			}

			cloneDurationSeconds, err := getMetric(registry, metricsPrefix+"clone_duration_seconds")
			if err != nil {
				return err
			}
			for _, quantile := range cloneDurationSeconds.Summary.Quantile {
				if math.IsNaN(*quantile.Value) {
					return fmt.Errorf("clone_duration_seconds should not have values: quantile=%f, value=%f", *quantile.Quantile, *quantile.Value)
				}
			}

			return nil
		}, 30*time.Second).Should(Succeed())
	})

	It("should not clone if recipient has some data", func() {
		initializeDonorMySQL(false)

		By("write data to recipient")
		err := test_utils.PrepareTestData(replicaPort)
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

	It("should not clone from external MySQL with invalid donor settings", func() {
		initializeDonorMySQL(true)

		testcases := []struct {
			title         string
			donorHost     string
			donorPort     int
			cloneUser     string
			clonePassword string
		}{
			{
				title:         "invalid donorHostName",
				donorHost:     "invalid-host",
				donorPort:     donorPort,
				cloneUser:     test_utils.ExternalDonorUser,
				clonePassword: test_utils.ExternalDonorUserPassword,
			},
			{
				title:         "invalid donorPort",
				donorHost:     donorHost,
				donorPort:     10000,
				cloneUser:     test_utils.ExternalDonorUser,
				clonePassword: test_utils.ExternalDonorUserPassword,
			},
			{
				title:         "invalid cloneUser",
				donorHost:     donorHost,
				donorPort:     donorPort,
				cloneUser:     "invalid-user",
				clonePassword: test_utils.ExternalDonorUserPassword,
			},
			{
				title:         "invalid clonePassword",
				donorHost:     donorHost,
				donorPort:     donorPort,
				cloneUser:     test_utils.ExternalDonorUser,
				clonePassword: "invalid-password",
			},
		}

		for _, tt := range testcases {
			By(fmt.Sprintf("(%s) %s", tt.title, "preparing test data"))
			data := &testData{
				primaryHost:            tt.donorHost,
				primaryPort:            tt.donorPort,
				cloneUser:              tt.cloneUser,
				clonePassword:          tt.clonePassword,
				initAfterCloneUser:     test_utils.ExternalInitUser,
				initAfterClonePassword: test_utils.ExternalInitUserPassword,
			}
			writeTestData(data)

			By(fmt.Sprintf("(%s) %s", tt.title, "setting  clone_valid_donor_list"))
			err := test_utils.SetValidDonorList(replicaPort, tt.donorHost, tt.donorPort)
			Expect(err).ShouldNot(HaveOccurred())

			By(fmt.Sprintf("(%s) %s", tt.title, "cloning from external MySQL"))
			req := httptest.NewRequest("GET", "http://"+replicaHost+"/clone", nil)
			queries := url.Values{
				moco.CloneParamExternal: []string{"true"},
				moco.AgentTokenParam:    []string{token},
			}
			req.URL.RawQuery = queries.Encode()

			res := httptest.NewRecorder()
			agent.Clone(res, req)
			Expect(res).Should(HaveHTTPStatus(http.StatusOK))

			// Just in case, wait for the clone to be started.
			time.Sleep(3 * time.Second)

			By(fmt.Sprintf("(%s) %s", tt.title, "checking after clone status"))
			Eventually(func() error {
				db, err := agent.acc.Get(test_utils.Host+":"+strconv.Itoa(replicaPort), moco.MiscUser, test_utils.MiscUserPassword)
				if err != nil {
					return err
				}

				cloneStatus, err := accessor.GetMySQLCloneStateStatus(context.Background(), db)
				if err != nil {
					return err
				}

				expected := sql.NullString{Valid: true, String: "Failed"}
				if !cmp.Equal(cloneStatus.State, expected) {
					return fmt.Errorf("doesn't reach failed state: %+v", cloneStatus.State)
				}
				return nil
			}, 30*time.Second).Should(Succeed())
		}

		By("checking metrics")
		// In these test cases, the clone will start and fail. So the metrics will change.
		Eventually(func() error {
			cloneCount, err := getMetric(registry, metricsPrefix+"clone_count")
			if err != nil {
				return err
			}
			if *cloneCount.Counter.Value != float64(len(testcases)) {
				return fmt.Errorf("clone_count isn't incremented yet: value=%f", *cloneCount.Counter.Value)
			}

			cloneFailureCount, err := getMetric(registry, metricsPrefix+"clone_failure_count")
			if err != nil {
				return err
			}
			if *cloneFailureCount.Counter.Value != float64(len(testcases)) {
				return fmt.Errorf("clone_failure_count isn't incremented yet: value=%f", *cloneFailureCount.Counter.Value)
			}

			cloneDurationSeconds, err := getMetric(registry, metricsPrefix+"clone_duration_seconds")
			if err != nil {
				return err
			}
			for _, quantile := range cloneDurationSeconds.Summary.Quantile {
				if !math.IsNaN(*quantile.Value) {
					return fmt.Errorf("clone_duration_seconds should have values: quantile=%f, value=%f", *quantile.Quantile, *quantile.Value)
				}
			}

			return nil
		}, 30*time.Second).Should(Succeed())
	})

	It("should clone from external MySQL", func() {
		initializeDonorMySQL(true)

		By("preparing test data")
		data := &testData{
			primaryHost:            donorHost,
			primaryPort:            donorPort,
			cloneUser:              test_utils.ExternalDonorUser,
			clonePassword:          test_utils.ExternalDonorUserPassword,
			initAfterCloneUser:     test_utils.ExternalInitUser,
			initAfterClonePassword: test_utils.ExternalInitUserPassword,
		}
		writeTestData(data)

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

		By("confirming clone by init user")
		Eventually(func() error {
			db, err := agent.acc.Get(test_utils.Host+":"+strconv.Itoa(replicaPort), test_utils.ExternalInitUser, test_utils.ExternalInitUserPassword)
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

		// The initialization(*) after cloning from the external donor does not succeed in this test.
		// In the initialization, the agent tries to connect to the MySQL server via the Unix domain socket. But the connection will not be succeeded.
		// *) htps://github.com/cybozu-go/moco/blob/v0.3.1/agent/clone.go#L169-L197
		Skip("MySQL users for MOCO don't be created")

		By("confirming clone by restored misc user")
		restoredMiscUserPassword := "dummy"
		Eventually(func() error {
			db, err := agent.acc.Get(test_utils.Host+":"+strconv.Itoa(replicaPort), moco.MiscUser, restoredMiscUserPassword)
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
		agent = New(test_utils.Host, token, test_utils.MiscUserPassword, test_utils.CloneDonorUserPassword, pwd, "", replicaPort,
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
