package agent

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	promgo "github.com/prometheus/client_model/go"
)

const (
	donorHost     = "moco-test-mysqld-donor"
	donorPort     = 3307
	replicaHost   = "moco-test-mysqld-replica"
	replicaPort   = 3308
	password      = "test-password"
	token         = "dummy-token"
	metricsPrefix = "moco_agent_"
)

func TestAgent(t *testing.T) {
	// If you want to suppress mysqld logs, please uncomment the below line
	// mysql.SetLogger(mysql.Logger(log.New(GinkgoWriter, "[mysql] ", log.Ldate|log.Ltime|log.Lshortfile)))
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Suite")
}

var _ = BeforeSuite(func(done Done) {
	err := initializeMySQL(donorHost, donorPort)
	Expect(err).ShouldNot(HaveOccurred())
	err = initializeMySQL(replicaHost, replicaPort)
	Expect(err).ShouldNot(HaveOccurred())

	err = prepareTestData(donorHost, donorPort)
	Expect(err).ShouldNot(HaveOccurred())

	err = setValidDonorList(replicaHost, replicaPort)
	Expect(err).ShouldNot(HaveOccurred())

	err = resetMaster(donorHost, donorPort)
	Expect(err).ShouldNot(HaveOccurred())
	err = resetMaster(replicaHost, replicaPort)
	Expect(err).ShouldNot(HaveOccurred())

	close(done)
}, 60)

var _ = AfterSuite(func() {})

func initializeMySQL(host string, port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = password
	conf.Net = "tcp"
	conf.Addr = host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	var db *sqlx.DB
	var err error
	for i := 0; i < 10; i++ {
		db, err = sqlx.Connect("mysql", conf.FormatDSN())
		if err == nil {
			break
		}
		time.Sleep(time.Second * 3)
	}
	if err != nil {
		return err
	}

	for _, user := range []string{moco.DonorUser, moco.MiscUser} {
		_, err = db.Exec("CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?", user, password)
		if err != nil {
			return err
		}
		_, err = db.Exec("GRANT ALL ON *.* TO ?@'%' WITH GRANT OPTION", user)
		if err != nil {
			return err
		}

	}

	_, err = db.Exec("INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSTALL PLUGIN clone SONAME 'mysql_clone.so'")
	if err != nil {
		return err
	}

	return nil
}

func prepareTestData(host string, port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = password
	conf.Net = "tcp"
	conf.Addr = host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	db, err := sqlx.Connect("mysql", conf.FormatDSN())
	if err != nil {
		return err
	}

	queries := []string{
		"CREATE DATABASE IF NOT EXISTS test",
		"CREATE TABLE IF NOT EXISTS `test`.`t1` (`num` bigint unsigned NOT NULL AUTO_INCREMENT,`val0` varchar(100) DEFAULT NULL,`val1` varchar(100) DEFAULT NULL,`val2` varchar(100) DEFAULT NULL,`val3` varchar(100) DEFAULT NULL,`val4` varchar(100) DEFAULT NULL,UNIQUE KEY `num` (`num`)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci",
		"INSERT INTO test.t1 (val0, val1, val2, val3, val4) WITH RECURSIVE t AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM t WHERE n < 10) SELECT MD5(RAND()),MD5(RAND()),MD5(RAND()),MD5(RAND()),MD5(RAND()) FROM t",
		"COMMIT",
	}
	for _, query := range queries {
		_, err = db.Exec(query)
		if err != nil {
			return err
		}
	}

	return nil
}

func startSlaveWithInvalidSettings(host string, port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = password
	conf.Net = "tcp"
	conf.Addr = host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	db, err := sqlx.Connect("mysql", conf.FormatDSN())
	if err != nil {
		return err
	}

	_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?`, "dummy", 3306, "dummy", "dummy")
	if err != nil {
		return err
	}
	_, err = db.Exec(`START SLAVE`)
	if err != nil {
		return err
	}

	return nil
}

func setValidDonorList(host string, port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = password
	conf.Net = "tcp"
	conf.Addr = host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	db, err := sqlx.Connect("mysql", conf.FormatDSN())
	if err != nil {
		return err
	}

	_, err = db.Exec(`SET GLOBAL clone_valid_donor_list = ?`, donorHost+":"+strconv.Itoa(donorPort))
	if err != nil {
		return err
	}

	return nil
}

func resetMaster(host string, port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = password
	conf.Net = "tcp"
	conf.Addr = host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	db, err := sqlx.Connect("mysql", conf.FormatDSN())
	if err != nil {
		return err
	}
	_, err = db.Exec("RESET MASTER")
	return err
}

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

var _ = Describe("Test Agent", func() {
	testAgentClone()
	testAgentHealth()
	testAgentRotate()
})
