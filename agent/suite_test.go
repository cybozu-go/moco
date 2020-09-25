package agent

import (
	"strconv"
	"testing"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	donorHost   = "localhost"
	donorPort   = 3307
	replicaHost = "localhost"
	replicaPort = 3308
	password    = "test-password"
	token       = "dummy-token"
)

func TestAgent(t *testing.T) {
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
		if err.Error() != "Error 1125: Function 'rpl_semi_sync_master' already exists" {
			return err
		}
	}
	_, err = db.Exec("INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'rpl_semi_sync_slave' already exists" {
			return err
		}
	}
	_, err = db.Exec("INSTALL PLUGIN clone SONAME 'mysql_clone.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'clone' already exists" {
			return err
		}
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

	_, err = db.Exec(`SET GLOBAL clone_valid_donor_list = ?`, "172.17.0.1:"+strconv.Itoa(donorPort))
	if err != nil {
		return err
	}

	return nil
}

var _ = Describe("Test Agent", func() {
	testAgentClone()
})
