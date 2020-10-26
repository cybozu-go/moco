package agent

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/metrics"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
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
	})

	AfterEach(func() {
		moco.StopAndRemoveMySQLD(replicaHost)
	})

	It("should return 400 with bad requests", func() {
		By("initializing metrics registry")
		registry := prometheus.NewRegistry()
		metrics.RegisterAgentMetrics(registry)

		By("preparing agent")

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

	It("should drop unnecessary users", func() {
		By("adding unnecessary users")
		conf := mysql.NewConfig()
		conf.User = "root"
		conf.Passwd = password
		conf.Net = "tcp"
		conf.Addr = host + ":" + strconv.Itoa(replicaPort)
		conf.InterpolateParams = true

		var db *sqlx.DB
		var err error
		for i := 0; i < 20; i++ {
			db, err = sqlx.Connect("mysql", conf.FormatDSN())
			if err == nil {
				break
			}
			time.Sleep(time.Second * 3)
		}
		Expect(err).ShouldNot(HaveOccurred())

		_, err = db.Exec("CREATE USER IF NOT EXISTS 'hoge'@'fuga' IDENTIFIED BY 'password")
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec("CREATE USER IF NOT EXISTS 'xxx'@'%' IDENTIFIED BY 'password")
		Expect(err).ShouldNot(HaveOccurred())

		db.Close()

		By("doing initialization after clone")
		req := httptest.NewRequest("GET", "http://"+replicaHost+"/init-after-clone", nil)
		queries := url.Values{
			moco.AgentTokenParam: []string{token},
		}
		req.URL.RawQuery = queries.Encode()

		res := httptest.NewRecorder()
		agent.InitAfterClone(res, req)
		Expect(res).Should(HaveHTTPStatus(http.StatusOK))

		By("checking users are dropped")
		for i := 0; i < 20; i++ {
			db, err = sqlx.Connect("mysql", conf.FormatDSN())
			if err == nil {
				break
			}
			time.Sleep(time.Second * 3)
		}
		Expect(err).ShouldNot(HaveOccurred())

		sqlRows, err := db.Query("SELECT user FROM mysql.user WHERE (user = 'hoge' AND host = 'fuga') OR (user = 'xxx' AND host = '%')")
		Expect(err).ShouldNot(HaveOccurred())
		Expect(sqlRows.Next()).Should(BeFalse())

		// TODO: check necessary users exist
	})
})
