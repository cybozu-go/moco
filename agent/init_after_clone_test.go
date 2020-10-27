package agent

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/metrics"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	OperatorPassword    = "operator"
	ReplicationPassword = "replication"
	RootPassword        = "root"
	CloneDonorPassword  = "clone-donor"
	MiscPassword        = "misc"
	PodIP               = "127.0.0.1"
)

var _ = Describe("Test Agent: InitAfterClone Request", func() {
	var agent *Agent
	var registry *prometheus.Registry

	BeforeEach(func() {
		err := moco.StartMySQLD(replicaHost, replicaPort, replicaServerID)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.InitializeMySQL(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.SetValidDonorList(replicaPort, donorHost, donorPort)
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

		err = os.Setenv(moco.OperatorPasswordKey, OperatorPassword)
		Expect(err).ShouldNot(HaveOccurred())
		err = os.Setenv(moco.ReplicationPasswordKey, ReplicationPassword)
		Expect(err).ShouldNot(HaveOccurred())
		err = os.Setenv(moco.RootPasswordKey, RootPassword)
		Expect(err).ShouldNot(HaveOccurred())
		err = os.Setenv(moco.CloneDonorPasswordKey, CloneDonorPassword)
		Expect(err).ShouldNot(HaveOccurred())
		err = os.Setenv(moco.MiscPasswordKey, MiscPassword)
		Expect(err).ShouldNot(HaveOccurred())
		err = os.Setenv(moco.PodIPEnvName, PodIP)
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		moco.StopAndRemoveMySQLD(replicaHost)
	})

	It("should return 400 with bad requests", func() {
		By("initializing metrics registry")
		registry := prometheus.NewRegistry()
		metrics.RegisterAgentMetrics(registry)

		By("passing invalid token")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/init-after-clone", nil)
		queries := url.Values{
			moco.AgentTokenParam: []string{"invalid-token"},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.InitAfterClone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusBadRequest))
	})

	// successful case is tested in e2e because agent is to connect to mysqld via unix domain socket but it's not possible.
})
