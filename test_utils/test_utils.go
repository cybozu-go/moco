package test_utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"k8s.io/apimachinery/pkg/util/uuid"
)

const (
	Host             = "localhost"
	AgentPort        = 30000
	RootUser         = "root"
	RootUserPassword = "rootpassword"

	// Dummy user and password for clone from external.
	ExternalDonorUser         = "external-donor-user"
	ExternalDonorUserPassword = "externaldonorpassword"
	ExternalInitUser          = "external-init-user"
	ExternalInitUserPassword  = "externalinitpassword"

	// Dummy password for MySQL users which are managed by MOCO.
	OperatorUserPassword      = "userpassword"
	OperatorAdminUserPassword = "adminpassword"
	ReplicationUserPassword   = "replpassword"
	CloneDonorUserPassword    = "clonepassword"
	MiscUserPassword          = "miscpassword"

	// Docker network name for test.
	networkName = "moco-test-net"
)

var MySQLVersion = os.Getenv("MYSQL_VERSION")

func run(cmd *well.LogCmd) error {
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	err := cmd.Run()
	stdout := strings.TrimRight(outBuf.String(), "\n")
	if len(stdout) != 0 {
		fmt.Println("[test_utils/stdout] " + stdout)
	}
	stderr := strings.TrimRight(errBuf.String(), "\n")
	if len(stderr) != 0 {
		fmt.Println("[test_utils/stderr] " + stderr)
	}
	return err
}

func StartMySQLD(name string, port int, serverID int) error {
	ctx := context.Background()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("failed to get file path")
	}
	cmd := well.CommandContext(ctx,
		"docker", "run", "-d", "--restart=always",
		"--network="+networkName,
		"--name", name,
		"-p", fmt.Sprintf("%d:%d", port, port),
		"-v", filepath.Join(filepath.Dir(thisFile), "my.cnf")+":/etc/mysql/conf.d/my.cnf",
		"-e", "MYSQL_ROOT_PASSWORD="+RootUserPassword,
		"mysql:"+MySQLVersion,
		fmt.Sprintf("--port=%d", port),
		fmt.Sprintf("--server-id=%d", serverID),
	)
	return run(cmd)
}

func StopAndRemoveMySQLD(name string) error {
	ctx := context.Background()
	cmd := well.CommandContext(ctx, "docker", "stop", name)
	run(cmd)

	cmd = well.CommandContext(ctx, "docker", "rm", name)
	return run(cmd)
}

func CreateNetwork() error {
	ctx := context.Background()
	cmd := well.CommandContext(ctx, "docker", "network", "create", networkName)
	run(cmd)

	cmd = well.CommandContext(ctx, "docker", "network", "inspect", networkName)
	return run(cmd)
}

func RemoveNetwork() error {
	ctx := context.Background()
	cmd := well.CommandContext(ctx, "docker", "network", "rm", networkName)
	return run(cmd)
}

func Connect(port, retryCount int) (*sqlx.DB, error) {
	conf := mysql.NewConfig()
	conf.User = RootUser
	conf.Passwd = RootUserPassword
	conf.Net = "tcp"
	conf.Addr = Host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	var db *sqlx.DB
	var err error
	dataSource := conf.FormatDSN()
	for i := 0; i <= retryCount; i++ {
		fmt.Printf("[test_utils/connect] %d, %s\n", i, dataSource)
		db, err = sqlx.Connect("mysql", dataSource)
		if err == nil {
			break
		}
		time.Sleep(time.Second * 3)
	}
	return db, err
}

func InitializeMySQL(port int) error {
	db, err := Connect(port, 20)
	if err != nil {
		return err
	}

	users := []struct {
		name     string
		password string
	}{
		{
			name:     moco.AdminUser,
			password: OperatorAdminUserPassword,
		},
		{
			name:     moco.AgentUser,
			password: MiscUserPassword,
		},
		{
			name:     moco.ReplicationUser,
			password: ReplicationUserPassword,
		},
		{
			name:     moco.CloneDonorUser,
			password: CloneDonorUserPassword,
		},
	}
	for _, user := range users {
		_, err = db.Exec("CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?", user.name, user.password)
		if err != nil {
			return err
		}
		_, err = db.Exec("GRANT ALL ON *.* TO ?@'%' WITH GRANT OPTION", user.name)
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

	_, err = db.Exec("CLONE LOCAL DATA DIRECTORY = ?", "/tmp/"+uuid.NewUUID())
	if err != nil {
		return err
	}

	return ResetMaster(port)
}

func InitializeMySQLAsExternalDonor(port int) error {
	db, err := Connect(port, 20)
	if err != nil {
		return err
	}

	users := []struct {
		name     string
		password string
	}{
		{
			name:     ExternalDonorUser,
			password: ExternalDonorUserPassword,
		},
		{
			name:     ExternalInitUser,
			password: ExternalInitUserPassword,
		},
	}
	for _, user := range users {
		_, err = db.Exec("CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?", user.name, user.password)
		if err != nil {
			return err
		}
		_, err = db.Exec("GRANT ALL ON *.* TO ?@'%' WITH GRANT OPTION", user.name)
		if err != nil {
			return err
		}
	}

	_, err = db.Exec("INSTALL PLUGIN clone SONAME 'mysql_clone.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'clone' already exists" {
			return err
		}
	}

	_, err = db.Exec("CLONE LOCAL DATA DIRECTORY = ?", "/tmp/"+uuid.NewUUID())
	if err != nil {
		return err
	}

	return ResetMaster(port)
}

func PrepareTestData(port int) error {
	db, err := Connect(port, 0)
	if err != nil {
		return err
	}

	_, err = db.Exec("CREATE DATABASE IF NOT EXISTS test")
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS test.t1 (
    num bigint unsigned NOT NULL AUTO_INCREMENT,
    val0 varchar(100) DEFAULT NULL,
    val1 varchar(100) DEFAULT NULL,
    val2 varchar(100) DEFAULT NULL,
    val3 varchar(100) DEFAULT NULL,
    val4 varchar(100) DEFAULT NULL,
    UNIQUE KEY num (num)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
INSERT INTO test.t1 (val0, val1, val2, val3, val4)
WITH RECURSIVE t AS (
    SELECT 1 AS n
    UNION ALL
    SELECT n + 1 FROM t WHERE n < 10
)
SELECT MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND()), MD5(RAND())
FROM t`)
	if err != nil {
		return err
	}

	_, err = db.Exec("COMMIT")
	return err
}

func SetValidDonorList(port int, donorHost string, donorPort int) error {
	db, err := Connect(port, 0)
	if err != nil {
		return err
	}

	_, err = db.Exec("SET GLOBAL clone_valid_donor_list = ?", donorHost+":"+strconv.Itoa(donorPort))
	return err
}

func ResetMaster(port int) error {
	db, err := Connect(port, 0)
	if err != nil {
		return err
	}

	_, err = db.Exec("RESET MASTER")
	return err
}

func StartSlaveWithInvalidSettings(port int) error {
	db, err := Connect(port, 0)
	if err != nil {
		return err
	}

	_, err = db.Exec("CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?", "dummy", 3306, "dummy", "dummy")
	if err != nil {
		return err
	}
	_, err = db.Exec("START SLAVE")
	return err
}
