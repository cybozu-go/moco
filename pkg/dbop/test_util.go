package dbop

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

const testMocoNetwork = "test-moco"

var testMySQLImage = "mysql:8"

func init() {
	mysqlVersion := os.Getenv("MYSQL_VERSION")
	if mysqlVersion != "" {
		testMySQLImage = "mysql:" + mysqlVersion
	}
}

var startupMycnf = map[string]string{
	"character_set_server":     "utf8mb4",
	"collation_server":         "utf8mb4_unicode_ci",
	"default_time_zone":        "+0:00",
	"disabled_storage_engines": "MyISAM",
	"skip_slave_start":         "ON",
	"enforce_gtid_consistency": "ON",
	"gtid_mode":                "ON",
}

var dynamicMycnf = map[string]string{
	"read_only":       "ON",
	"super_read_only": "ON",
}

func NewTestFactory() OperatorFactory {
	if err := exec.Command("docker", "network", "inspect", testMocoNetwork).Run(); err != nil {
		err := exec.Command("docker", "network", "create", testMocoNetwork).Run()
		if err != nil {
			panic(err)
		}
	}
	return &testFactory{
		portBase:    35000,
		instanceMap: make(map[string][]int),
	}
}

type testFactory struct {
	portBase    int
	instanceMap map[string][]int
}

func waitTestMySQL(port int) (*sqlx.DB, error) {
	addr := fmt.Sprintf("localhost:%d", port)
	cfg := mysql.NewConfig()
	cfg.User = "root"
	cfg.Net = "tcp"
	cfg.Addr = addr
	cfg.InterpolateParams = true
	db, err := sqlx.Connect("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}

	// mysql Docker image restarts mysqld several times, so we need to wait.
	ts := time.Now()
	for {
		if time.Since(ts) > 20*time.Second {
			return db, nil
		}

		_, err := db.Exec(`SELECT @@super_read_only`)
		if err != nil {
			db.Close()
			return nil, err
		}
		time.Sleep(1 * time.Second)
	}
}

func testContainerName(cluster *mocov1beta1.MySQLCluster, index int) string {
	return fmt.Sprintf("moco-%s-%d.%s", cluster.Name, index, cluster.Namespace)
}

func (f *testFactory) New(cluster *mocov1beta1.MySQLCluster, pwd *password.MySQLPassword, index int) Operator {
	mapKey := fmt.Sprintf("%s_%s", cluster.Namespace, cluster.Name)
	instances, ok := f.instanceMap[mapKey]
	if !ok {
		instances = make([]int, cluster.Spec.Replicas)
		for i := 0; i < int(cluster.Spec.Replicas); i++ {
			port := f.portBase
			f.portBase++
			name := testContainerName(cluster, i)
			args := []string{"run", "-d", "--rm", "--name=" + name, "--network=" + testMocoNetwork,
				"-e", "MYSQL_ALLOW_EMPTY_PASSWORD=yes", "-p", fmt.Sprintf("%d:3306", port), testMySQLImage,
				"--server_id=" + fmt.Sprint(i+1)}
			for k, v := range startupMycnf {
				args = append(args, fmt.Sprintf("--%s=%s", k, v))
			}
			out, err := exec.Command("docker", args...).CombinedOutput()
			if err != nil {
				panic(fmt.Sprintf("%s: %v", out, err))
			}
			instances[i] = port
		}
		f.instanceMap[mapKey] = instances

		for _, port := range instances {
			st := time.Now()
			var db *sqlx.DB
			for {
				var err error
				db, err = waitTestMySQL(port)
				if err == nil {
					break
				}
				if time.Since(st) > 1*time.Minute {
					panic(fmt.Sprintf("failed to connect to mysqld %d", port))
				}
				time.Sleep(1 * time.Second)
			}
			defer db.Close()

			// imperfectly emulate moco-agent initialization
			db.MustExec(`CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?`, constants.AdminUser, pwd.Admin())
			db.MustExec(`GRANT ALL ON *.* TO ?@'%' WITH GRANT OPTION`, constants.AdminUser)
			db.MustExec(`CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?`, constants.AgentUser, pwd.Agent())
			db.MustExec(`GRANT ALL ON *.* TO ?@'%'`, constants.AgentUser)
			db.MustExec(`CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?`, constants.ReplicationUser, pwd.Replicator())
			db.MustExec(`GRANT REPLICATION CLIENT, REPLICATION SLAVE ON *.* TO ?@'%'`, constants.ReplicationUser)
			db.MustExec(`CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?`, constants.CloneDonorUser, pwd.Donor())
			db.MustExec(`GRANT BACKUP_ADMIN, SERVICE_CONNECTION_ADMIN ON *.* TO ?@'%'`, constants.CloneDonorUser)
			db.MustExec(`CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?`, constants.ReadOnlyUser, pwd.ReadOnly())
			db.MustExec(`GRANT PROCESS, REPLICATION CLIENT, REPLICATION SLAVE, SELECT, SHOW DATABASES, SHOW VIEW ON *.* TO ?@'%'`, constants.ReadOnlyUser)
			db.MustExec(`CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?`, constants.WritableUser, pwd.Writable())
			db.MustExec(`GRANT ALL ON *.* TO ?@'%' WITH GRANT OPTION`, constants.WritableUser)
			db.MustExec(`INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'`)
			db.MustExec(`INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'`)
			db.MustExec(`INSTALL PLUGIN clone SONAME 'mysql_clone.so'`)

			for k, v := range dynamicMycnf {
				db.MustExec("SET GLOBAL "+k+"=?", v)
			}

			// clear executed_gtid_set
			db.MustExec(`RESET MASTER`)
			db.Close()
		}
	}

	return newTestOperator(cluster, pwd, index, instances[index])
}

func newTestOperator(cluster *mocov1beta1.MySQLCluster, pwd *password.MySQLPassword, index, port int) Operator {
	cfg := mysql.NewConfig()
	cfg.User = constants.AdminUser
	cfg.Passwd = pwd.Admin()
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("localhost:%d", port)
	cfg.InterpolateParams = true
	cfg.ParseTime = true
	cfg.Timeout = connTimeout
	cfg.ReadTimeout = readTimeout
	udb := sqlx.MustOpen("mysql", cfg.FormatDSN())
	udb.SetMaxIdleConns(1)
	udb.SetConnMaxIdleTime(30 * time.Second)

	return &operator{
		cluster: cluster,
		passwd:  pwd,
		index:   index,
		db:      udb,
	}
}

func (f *testFactory) Cleanup() {
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		return
	}
	for _, name := range strings.Fields(string(out)) {
		if strings.HasPrefix(name, "moco-") {
			exec.Command("docker", "kill", name).Run()
		}
	}
	time.Sleep(100 * time.Millisecond)
	exec.Command("docker", "network", "rm", testMocoNetwork).Run()
}
