package test_utils

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	Host        = "localhost"
	UserName    = "root"
	Password    = "testpassword"
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

	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	cmd := well.CommandContext(ctx,
		"docker", "run", "-d", "--restart=always",
		"--network="+networkName,
		"--name", name,
		"-p", fmt.Sprintf("%d:%d", port, port),
		"-v", filepath.Join(wd, "..", "my.cnf")+":/etc/mysql/conf.d/my.cnf",
		"-e", "MYSQL_ROOT_PASSWORD="+Password,
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

func InitializeMySQL(port int) error {
	conf := mysql.NewConfig()
	conf.User = UserName
	conf.Passwd = Password
	conf.Net = "tcp"
	conf.Addr = Host + ":" + strconv.Itoa(port)
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
	if err != nil {
		return err
	}

	for _, user := range []string{moco.OperatorAdminUser, moco.CloneDonorUser, moco.MiscUser} {
		_, err = db.Exec("CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?", user, Password)
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

	_, err = db.Exec(`CLONE LOCAL DATA DIRECTORY = ?`, "/tmp/"+uuid.NewUUID())
	if err != nil {
		return err
	}

	ResetMaster(port)

	return nil
}

func PrepareTestData(port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = Password
	conf.Net = "tcp"
	conf.Addr = Host + ":" + strconv.Itoa(port)
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

func SetValidDonorList(port int, donorHost string, donorPort int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = Password
	conf.Net = "tcp"
	conf.Addr = Host + ":" + strconv.Itoa(port)
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

func ResetMaster(port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = Password
	conf.Net = "tcp"
	conf.Addr = Host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	db, err := sqlx.Connect("mysql", conf.FormatDSN())
	if err != nil {
		return err
	}
	_, err = db.Exec("RESET MASTER")
	return err
}

func StartSlaveWithInvalidSettings(port int) error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = Password
	conf.Net = "tcp"
	conf.Addr = Host + ":" + strconv.Itoa(port)
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
