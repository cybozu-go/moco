package dbop

import (
	"context"
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

func RunMySQLOnDocker(name string, port, xport int) error {
	args := []string{"run", "-d", "--rm", "--name=" + name, "--network=" + testMocoNetwork,
		"-e", "MYSQL_ALLOW_EMPTY_PASSWORD=yes",
		"-p", fmt.Sprintf("%d:3306", port),
		"-p", fmt.Sprintf("%d:33060", xport),
		testMySQLImage,
		"--server_id=" + fmt.Sprint(port)}
	for k, v := range startupMycnf {
		args = append(args, fmt.Sprintf("--%s=%s", k, v))
	}
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run MySQL container: %w: %s", err, out)
	}
	return nil
}

func ConfigureMySQLOnDocker(pwd *password.MySQLPassword, port int) error {
	st := time.Now()
	var db *sqlx.DB
	for {
		var err error
		db, err = waitTestMySQL(port)
		if err == nil {
			break
		}
		if time.Since(st) > 1*time.Minute {
			return fmt.Errorf("failed to connect to mysqld %d", port)
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
	db.MustExec(`CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?`, constants.BackupUser, pwd.Backup())
	db.MustExec(`GRANT BACKUP_ADMIN, EVENT, RELOAD, SELECT, SHOW VIEW, TRIGGER, REPLICATION CLIENT, REPLICATION SLAVE, SERVICE_CONNECTION_ADMIN ON *.* TO ?@'%'`, constants.BackupUser)
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
	return nil
}

func NewTestFactory() OperatorFactory {
	return &testFactory{
		portBase:    10000,
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

func (f *testFactory) New(ctx context.Context, cluster *mocov1beta1.MySQLCluster, pwd *password.MySQLPassword, index int) (Operator, error) {
	mapKey := fmt.Sprintf("%s_%s", cluster.Namespace, cluster.Name)
	instances, ok := f.instanceMap[mapKey]
	if !ok {
		instances = make([]int, cluster.Spec.Replicas)
		for i := 0; i < int(cluster.Spec.Replicas); i++ {
			port := f.portBase
			f.portBase += 2
			name := testContainerName(cluster, i)
			if err := RunMySQLOnDocker(name, port, port+1); err != nil {
				return nil, err
			}
			instances[i] = port
		}
		f.instanceMap[mapKey] = instances

		for _, port := range instances {
			if err := ConfigureMySQLOnDocker(pwd, port); err != nil {
				return nil, err
			}
		}
	}

	return newTestOperator(cluster, pwd, index, instances[index])
}

func newTestOperator(cluster *mocov1beta1.MySQLCluster, pwd *password.MySQLPassword, index, port int) (Operator, error) {
	cfg := mysql.NewConfig()
	cfg.User = constants.AdminUser
	cfg.Passwd = pwd.Admin()
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("localhost:%d", port)
	cfg.InterpolateParams = true
	cfg.ParseTime = true
	cfg.Timeout = connTimeout
	cfg.ReadTimeout = readTimeout
	udb, err := sqlx.Connect("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", cfg.FormatDSN(), err)
	}
	udb.SetMaxIdleConns(1)
	udb.SetConnMaxIdleTime(30 * time.Second)

	return &operator{
		namespace: cluster.Namespace,
		name:      cluster.PodName(index),
		passwd:    pwd,
		index:     index,
		db:        udb,
	}, nil
}

func (f *testFactory) newConn(ctx context.Context, cluster *mocov1beta1.MySQLCluster, user, passwd string, index int) (*sqlx.DB, error) {
	mapKey := fmt.Sprintf("%s_%s", cluster.Namespace, cluster.Name)
	instances, ok := f.instanceMap[mapKey]
	if !ok {
		panic("bug")
	}
	port := instances[index]
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = passwd
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("localhost:%d", port)
	cfg.InterpolateParams = true
	cfg.ParseTime = true
	cfg.Timeout = connTimeout
	cfg.ReadTimeout = readTimeout
	udb, err := sqlx.Connect("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", cfg.FormatDSN(), err)
	}
	udb.SetMaxIdleConns(1)
	udb.SetConnMaxIdleTime(30 * time.Second)
	return udb, nil
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
}
