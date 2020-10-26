package initialize

import (
	"context"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cybozu-go/moco"
)

var (
	initOnceCompletedPath = filepath.Join(moco.MySQLDataPath, "init-once-completed")
	passwordFilePath      = filepath.Join("/tmp", "moco-root-password")
	rootPassword          = "root-password"
	miscConfPath          = filepath.Join(moco.MySQLDataPath, "misc.cnf")
)

func testInitializeInstance(t *testing.T) {
	ctx := context.Background()

	err := os.MkdirAll(moco.MySQLConfPath, 0755)
	if err != nil {
		t.Fatal(err)
	}

	confPath := filepath.Join(moco.MySQLConfPath, moco.MySQLConfName)
	err = ioutil.WriteFile(confPath, []byte(`[client]
socket = /var/run/mysqld/mysqld.sock
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

	err = initializeInstance(ctx)
	if err != nil {
		t.Fatal(err)
	}

}

func myAddress() net.IP {
	netInterfaceAddresses, _ := net.InterfaceAddrs()
	for _, netInterfaceAddress := range netInterfaceAddresses {
		networkIp, ok := netInterfaceAddress.(*net.IPNet)
		if ok && !networkIp.IP.IsLoopback() && networkIp.IP.To4() != nil {
			return networkIp.IP
		}
	}
	return net.IPv4zero
}

func testWaitInstanceBootstrap(t *testing.T) {
	ctx := context.Background()
	err := waitInstanceBootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

func testInitializeRootUser(t *testing.T) {
	ctx := context.Background()
	ip := myAddress()
	if ip.IsUnspecified() {
		t.Fatal("cannot get my IP address")
	}

	// Without password
	err := initializeRootUser(ctx, passwordFilePath, "root", nil, rootPassword, ip.String())
	if err != nil {
		t.Fatal(err)
	}

	// Use password set by the previous initializeRootUser
	err = initializeRootUser(ctx, passwordFilePath, "root", &rootPassword, rootPassword, ip.String())
	if err != nil {
		t.Fatal(err)
	}

	out, err := execSQL(ctx, passwordFilePath, []byte("SELECT host FROM mysql.user WHERE user='root';"), "")
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, user := range strings.Split(string(out), "\n") {
		if strings.Contains(user, ip.String()) {
			found = true
		}
	}
	if !found {
		t.Fatal("cannot find user: root@" + ip.String())
	}
}

func testInitializeOperatorUser(t *testing.T) {
	ctx := context.Background()
	operatorPassword := "operator-password"
	err := initializeOperatorUser(ctx, passwordFilePath, operatorPassword)
	if err != nil {
		t.Fatal(err)
	}

	out, err := execSQL(ctx, passwordFilePath, []byte("SELECT count(*) FROM mysql.user WHERE user='moco';"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "1") {
		t.Fatal("cannot find user: moco")
	}
}

func testInitializeOperatorAdminUser(t *testing.T) {
	ctx := context.Background()
	adminPassword := "admin-password"
	err := initializeOperatorAdminUser(ctx, passwordFilePath, adminPassword)
	if err != nil {
		t.Fatal(err)
	}

	out, err := execSQL(ctx, passwordFilePath, []byte("SELECT count(*) FROM mysql.user WHERE user='moco-admin';"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "1") {
		t.Fatal("cannot find user: moco-admin")
	}
}

func testInitializeDonorUser(t *testing.T) {
	ctx := context.Background()
	donorPassword := "donor-password"
	err := initializeDonorUser(ctx, passwordFilePath, donorPassword)
	if err != nil {
		t.Fatal(err)
	}

	out, err := execSQL(ctx, passwordFilePath, []byte("SELECT count(*) FROM mysql.user WHERE user='moco-clone-donor';"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "1") {
		t.Fatal("cannot find user: moco-clone-donor")
	}
}

func testInitializeReplicationUser(t *testing.T) {
	ctx := context.Background()
	replicationPassword := "replication-password"
	err := initializeReplicationUser(ctx, passwordFilePath, replicationPassword)
	if err != nil {
		t.Fatal(err)
	}

	out, err := execSQL(ctx, passwordFilePath, []byte("SELECT count(*) FROM mysql.user WHERE user='moco-repl';"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "1") {
		t.Fatal("cannot find user: moco-repl")
	}
}

func testInitializeMiscUser(t *testing.T) {
	ctx := context.Background()
	miscPassword := "misc-password"
	err := initializeMiscUser(ctx, passwordFilePath, miscConfPath, miscPassword)
	if err != nil {
		t.Fatal(err)
	}

	out, err := execSQL(ctx, passwordFilePath, []byte("SELECT count(*) FROM mysql.user WHERE user='misc';"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "1") {
		t.Fatal("cannot find user: misc")
	}
	_, err = os.Stat(miscConfPath)
	if err != nil {
		t.Fatal(err)
	}
}

func testInstallPlugins(t *testing.T) {
	ctx := context.Background()
	err := installPlugins(ctx, passwordFilePath)
	if err != nil {
		t.Fatal(err)
	}

	out, err := execSQL(ctx, passwordFilePath, []byte("SHOW PLUGINS;"), "")
	if err != nil {
		t.Fatal(err)
	}

	semiSyncMasterFound := false
	semiSyncSlaveFound := false
	cloneFound := false
	for _, plugin := range strings.Split(string(out), "\n") {
		if strings.Contains(plugin, "rpl_semi_sync_master") {
			semiSyncMasterFound = true
		}
		if strings.Contains(plugin, "rpl_semi_sync_slave") {
			semiSyncSlaveFound = true
		}
		if strings.Contains(plugin, "clone") {
			cloneFound = true
		}
	}
	if !semiSyncMasterFound {
		t.Fatal("cannot find plugin: rpl_semi_sync_master")
	}
	if !semiSyncSlaveFound {
		t.Fatal("cannot find plugin: rpl_semi_sync_slave")
	}
	if !cloneFound {
		t.Fatal("cannot find plugin: clone")
	}
}

func testShutdownInstance(t *testing.T) {
	ctx := context.Background()
	err := shutdownInstance(ctx, passwordFilePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = doExec(ctx, nil, "mysqladmin", "ping")
	if err == nil {
		t.Fatal("cannot shutdown instance")
	}
}

func testTouchInitOnceCompleted(t *testing.T) {
	ctx := context.Background()
	err := touchInitOnceCompleted(ctx, initOnceCompletedPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(initOnceCompletedPath)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInit(t *testing.T) {
	_, err := os.Stat(filepath.Join("/", ".dockerenv"))
	if err != nil {
		t.Skip("These tests should be run on docker")
	}
	t.Run("initializeInstance", testInitializeInstance)
	t.Run("waitInstanceBootstrap", testWaitInstanceBootstrap)
	t.Run("initializeRootUser", testInitializeRootUser)
	t.Run("initializeOperatorUser", testInitializeOperatorUser)
	t.Run("initializeOperatorAdminUser", testInitializeOperatorAdminUser)
	t.Run("initializeDonorUser", testInitializeDonorUser)
	t.Run("initializeReplicationUser", testInitializeReplicationUser)
	t.Run("initializeMiscUser", testInitializeMiscUser)
	t.Run("installPlugins", testInstallPlugins)
	t.Run("shutdownInstance", testShutdownInstance)
	t.Run("TouchInitOnceCompleted", testTouchInitOnceCompleted)
}
