package initialize

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/cybozu-go/moco"
)

func testInitializeInstance(t *testing.T) {
	err := os.MkdirAll(moco.MySQLConfPath, 0755)
	if err != nil {
		t.Fatal(err)
	}

	confPath := filepath.Join(moco.MySQLConfPath, moco.MySQLConfName)
	err = ioutil.WriteFile(confPath, []byte(`[client]
loose_default_character_set = utf8mb4
[mysqld]
socket = /var/run/mysqld/mysqld.sock
datadir = /var/lib/mysql
log_error = /var/log/mysql/error.log
slow_query_log_file = /var/log/mysql/slow.log
pid_file = /var/run/mysqld/mysqld.pid
character_set_server = utf8mb4
collation_server = utf8mb4_unicode_ci
default_time_zone = +0:00
disabled_storage_engines = MyISAM
enforce_gtid_consistency = ON
gtid_mode = ON
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	err = initializeInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func testShutdownInstance(t *testing.T) {
}

func testTouchInitOnceCompleted(t *testing.T) {
}

func testWaitInstanceBootstrap(t *testing.T) {
}

func TestInit(t *testing.T) {
	_, err := os.Stat(filepath.Join("/", ".dockerenv"))
	if err != nil {
		t.Skip("These tests should be run on docker")
	}
	t.Run("initializeInstance", testInitializeInstance)
	t.Run("shutdownInstance", testShutdownInstance)
	t.Run("touchInitOnceCompleted", testTouchInitOnceCompleted)
	t.Run("waitInstanceBootstrap", testWaitInstanceBootstrap)
}
