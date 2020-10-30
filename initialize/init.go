package initialize

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
	"text/template"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
	"github.com/spf13/viper"
)

const timeoutDuration = 30 * time.Second

func InitializeOnce(ctx context.Context, initOnceCompletedPath, passwordFilePath, miscConfPath string) error {
	_, err := os.Stat(initOnceCompletedPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		log.Info("skip data initialization since "+initOnceCompletedPath+" already exists", nil)
		return nil
	}

	log.Info("initialize mysql database", nil)
	err = initializeInstance(ctx)
	if err != nil {
		return err
	}

	log.Info("wait until the instance is started", nil)
	err = waitInstanceBootstrap(ctx)
	if err != nil {
		return err
	}

	err = RestoreUsers(ctx, passwordFilePath, miscConfPath, "root", nil, viper.GetString(moco.PodIPFlag))
	if err != nil {
		return err
	}

	log.Info("shutdown instance", nil)
	err = ShutdownInstance(ctx, passwordFilePath)
	if err != nil {
		return err
	}

	log.Info("touch "+initOnceCompletedPath, nil)
	return touchInitOnceCompleted(ctx, initOnceCompletedPath)
}

// RestoreUsers creates users for MOCO and grants privileges to them.
func RestoreUsers(ctx context.Context, passwordFilePath, miscConfPath, initUser string, initPassword *string, rootHost string) error {

	log.Info("setup root user", nil)
	err := initializeRootUser(ctx, passwordFilePath, initUser, initPassword, os.Getenv(moco.RootPasswordEnvName), rootHost)
	if err != nil {
		return err
	}

	log.Info("setup operator user", nil)
	err = initializeOperatorUser(ctx, passwordFilePath, os.Getenv(moco.OperatorPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup operator-admin users", nil)
	// use the password for an operator-admin user which is the same with the one for operator user
	err = initializeOperatorAdminUser(ctx, passwordFilePath, os.Getenv(moco.OperatorPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup donor user", nil)
	err = initializeDonorUser(ctx, passwordFilePath, os.Getenv(moco.ClonePasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup replication user", nil)
	err = initializeReplicationUser(ctx, passwordFilePath, os.Getenv(moco.ReplicationPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup misc user", nil)
	err = initializeMiscUser(ctx, passwordFilePath, miscConfPath, os.Getenv(moco.MiscPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup readonly user", nil)
	err = initializeReadOnlyUser(ctx, passwordFilePath, os.Getenv(moco.ReadOnlyPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup writable user", nil)
	err = initializeWritableUser(ctx, passwordFilePath, os.Getenv(moco.WritablePasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("sync timezone with system", nil)
	err = importTimeZoneFromHost(ctx, passwordFilePath)
	if err != nil {
		return err
	}

	log.Info("install plugins", nil)
	err = installPlugins(ctx, passwordFilePath)
	if err != nil {
		return err
	}

	return nil
}

func initializeInstance(ctx context.Context) error {
	out, err := doExec(ctx, nil, "mysqld", "--defaults-file="+filepath.Join(moco.MySQLConfPath, moco.MySQLConfName), "--initialize-insecure")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	cmd := well.CommandContext(ctx, "mysqld", "--skip-networking")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func waitInstanceBootstrap(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, timeoutDuration)
	defer cancel()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			_, err := doExec(ctx, nil, "mysqladmin", "ping")
			if err == nil {
				return nil
			}
		}
	}
}

func importTimeZoneFromHost(ctx context.Context, passwordFilePath string) error {
	out, err := doExec(ctx, nil, "mysql_tzinfo_to_sql", "/usr/share/zoneinfo")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	out, err = execSQL(ctx, passwordFilePath, out, "mysql")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func initializeRootUser(ctx context.Context, passwordFilePath, initUser string, initPassword *string, rootPassword, rootHost string) error {
	if rootPassword == "" {
		return fmt.Errorf("root password is not set")
	}

	// execSQL requires the password file.
	conf := fmt.Sprintf(`[client]
user="%s"
`, initUser)
	if initPassword != nil {
		conf += fmt.Sprintf(`password="%s"
`, *initPassword)
	}

	err := ioutil.WriteFile(passwordFilePath, []byte(conf), 0600)
	if err != nil {
		return err
	}

	t := template.Must(template.New("init").Parse(`
CREATE USER IF NOT EXISTS 'root'@'localhost';
GRANT ALL ON *.* TO 'root'@'localhost' WITH GRANT OPTION ;
GRANT PROXY ON ''@'' TO 'root'@'localhost' WITH GRANT OPTION ;
ALTER USER 'root'@'localhost' IDENTIFIED BY '{{ .Password }}';
`))
	init := new(bytes.Buffer)
	err = t.Execute(init, struct {
		Password string
	}{rootPassword})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, init.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	passwordConf := `[client]
	user=root
	password="%s"
	`
	err = ioutil.WriteFile(passwordFilePath, []byte(fmt.Sprintf(passwordConf, rootPassword)), 0600)
	if err != nil {
		return err
	}

	t = template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS 'root'@'{{ .Host }}' ;
CREATE USER 'root'@'{{ .Host }}' IDENTIFIED BY '{{ .Password }}';
GRANT ALL ON *.* TO 'root'@'{{ .Host }}' WITH GRANT OPTION ;
GRANT PROXY ON ''@'' TO 'root'@'{{ .Host }}' WITH GRANT OPTION ;
FLUSH PRIVILEGES ;
`))

	sql := new(bytes.Buffer)
	err = t.Execute(sql, struct {
		Password string
		Host     string
	}{rootPassword, rootHost})
	if err != nil {
		return err
	}

	out, err = execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	return nil
}

func initializeOperatorUser(ctx context.Context, passwordFilePath string, password string) error {
	t := template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS '{{ .User }}'@'%' ;
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
    SELECT,
    SHOW VIEW,
    TRIGGER,
    LOCK TABLES,
    REPLICATION CLIENT,
    BACKUP_ADMIN,
    CLONE_ADMIN,
    BINLOG_ADMIN,
    SYSTEM_VARIABLES_ADMIN,
    REPLICATION_SLAVE_ADMIN,
    SERVICE_CONNECTION_ADMIN
  ON *.* TO '{{ .User }}'@'%' ;
SET GLOBAL partial_revokes=on ;
REVOKE
    SHOW VIEW,
    TRIGGER,
    LOCK TABLES,
    REPLICATION CLIENT
  ON mysql.* FROM '{{ .User }}'@'%' ;
`))

	sql := new(bytes.Buffer)
	err := t.Execute(sql, struct {
		User     string
		Password string
	}{moco.OperatorUser, password})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func initializeOperatorAdminUser(ctx context.Context, passwordFilePath string, password string) error {
	t := template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS '{{ .User }}'@'%' ;
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
    ALL
  ON *.* TO '{{ .User }}'@'%' WITH GRANT OPTION ;
`))

	sql := new(bytes.Buffer)
	err := t.Execute(sql, struct {
		User     string
		Password string
	}{moco.OperatorAdminUser, password})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func initializeDonorUser(ctx context.Context, passwordFilePath string, password string) error {
	t := template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS '{{ .User }}'@'%' ;
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
    BACKUP_ADMIN,
    SERVICE_CONNECTION_ADMIN
  ON *.* TO '{{ .User }}'@'%' WITH GRANT OPTION ;
`))

	sql := new(bytes.Buffer)
	err := t.Execute(sql, struct {
		User     string
		Password string
	}{moco.CloneDonorUser, password})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	err = os.Remove(moco.DonorPasswordPath)
	if err != nil && err.(*os.PathError).Unwrap() != syscall.ENOENT {
		return err
	}
	return ioutil.WriteFile(moco.DonorPasswordPath, []byte(password), 0400)
}

func initializeReplicationUser(ctx context.Context, passwordFilePath string, password string) error {
	// Use mysql_native_password because no ssl connections without sha-2 cache fail
	// Will fix it when we work on replication with encrypted connection
	// See https://yoku0825.blogspot.com/2018/10/mysql-80cachingsha2password-ssl.html
	t := template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS '{{ .User }}'@'%' ;
CREATE USER '{{ .User }}'@'%' IDENTIFIED WITH mysql_native_password BY '{{ .Password }}' ;
GRANT
    REPLICATION SLAVE,
    REPLICATION CLIENT
  ON *.* TO '{{ .User }}'@'%' WITH GRANT OPTION ;
`))

	sql := new(bytes.Buffer)
	err := t.Execute(sql, struct {
		User     string
		Password string
	}{moco.ReplicationUser, password})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func initializeMiscUser(ctx context.Context, passwordFilePath string, miscConfPath string, password string) error {
	t := template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS '{{ .User }}'@'%' ;
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
    SELECT,
    RELOAD,
    CLONE_ADMIN,
    SERVICE_CONNECTION_ADMIN,
    REPLICATION CLIENT
  ON *.* TO '{{ .User }}'@'%' ;
`))

	sql := new(bytes.Buffer)
	err := t.Execute(sql, struct {
		User     string
		Password string
	}{moco.MiscUser, password})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	conf := `
[client]
user=%s
password=%s
`
	err = os.Remove(miscConfPath)
	if err != nil && err.(*os.PathError).Unwrap() != syscall.ENOENT {
		return err
	}
	if err := ioutil.WriteFile(miscConfPath, []byte(fmt.Sprintf(conf, moco.MiscUser, password)), 0400); err != nil {
		return err
	}

	err = os.Remove(moco.MiscPasswordPath)
	if err != nil && err.(*os.PathError).Unwrap() != syscall.ENOENT {
		return err
	}
	return ioutil.WriteFile(moco.MiscPasswordPath, []byte(password), 0400)
}

func initializeReadOnlyUser(ctx context.Context, passwordFilePath string, password string) error {
	t := template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS '{{ .User }}'@'%' ;
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
    PROCESS,
    SELECT,
    SHOW DATABASES,
    SHOW VIEW,
    REPLICATION CLIENT,
    REPLICATION SLAVE
  ON *.* TO '{{ .User }}'@'%' ;
`))

	sql := new(bytes.Buffer)
	err := t.Execute(sql, struct {
		User     string
		Password string
	}{moco.ReadOnlyUser, password})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func initializeWritableUser(ctx context.Context, passwordFilePath string, password string) error {
	t := template.Must(template.New("sql").Parse(`
DROP USER IF EXISTS '{{ .User }}'@'%' ;
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
    ALTER,
    ALTER ROUTINE,
    CREATE,
    CREATE ROLE,
    CREATE ROUTINE,
    CREATE TEMPORARY TABLES,
    CREATE USER,
    CREATE VIEW,
    DELETE,
    DROP,
    DROP ROLE,
    EVENT,
    EXECUTE,
    INDEX,
    INSERT,
    LOCK TABLES,
    PROCESS,
    REFERENCES,
    REPLICATION CLIENT,
    REPLICATION SLAVE,
    SELECT,
    SHOW DATABASES,
    SHOW VIEW,
    TRIGGER,
    UPDATE
  ON *.* TO '{{ .User }}'@'%' WITH GRANT OPTION;
SET GLOBAL partial_revokes=on ;
REVOKE
    CREATE,
    CREATE ROUTINE,
    CREATE TEMPORARY TABLES,
    CREATE VIEW,
    DELETE,
    DROP,
    EVENT,
    EXECUTE,
    INDEX,
    INSERT,
    LOCK TABLES,
    REFERENCES,
    TRIGGER,
    UPDATE
  ON mysql.* FROM '{{ .User }}'@'%' ;
`))

	sql := new(bytes.Buffer)
	err := t.Execute(sql, struct {
		User     string
		Password string
	}{moco.WritableUser, password})
	if err != nil {
		return err
	}

	out, err := execSQL(ctx, passwordFilePath, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func installPlugins(ctx context.Context, passwordFilePath string) error {
	// to make this procedure idempotent, uninstall first.
	sql := `
UNINSTALL PLUGIN rpl_semi_sync_master;
UNINSTALL PLUGIN rpl_semi_sync_slave;
UNINSTALL PLUGIN clone;
`
	// ignore uninstallation error
	execSQL(ctx, passwordFilePath, []byte(sql), "")

	sql = `
INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so';
INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so';
INSTALL PLUGIN clone SONAME 'mysql_clone.so';
`
	out, err := execSQL(ctx, passwordFilePath, []byte(sql), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func ShutdownInstance(ctx context.Context, passwordFilePath string) error {
	out, err := doExec(ctx, nil,
		"mysqladmin", "--defaults-extra-file="+passwordFilePath, "shutdown")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func touchInitOnceCompleted(ctx context.Context, initOnceCompletedPath string) error {
	f, err := os.Create(initOnceCompletedPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := f.Sync(); err != nil {
		return err
	}

	dataDir, err := os.Open(moco.MySQLDataPath)
	if err != nil {
		return err
	}
	defer dataDir.Close()

	return dataDir.Sync()
}

func doExec(ctx context.Context, input []byte, command string, args ...string) ([]byte, error) {
	cmd := well.CommandContext(ctx, command, args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	return cmd.Output()
}

func execSQL(ctx context.Context, passwordFilePath string, input []byte, databaseName string) ([]byte, error) {
	args := []string{
		"--defaults-extra-file=" + passwordFilePath,
		"-hlocalhost",
		"--init-command=SET @@GLOBAL.SUPER_READ_ONLY=OFF; SET @@GLOBAL.OFFLINE_MODE=OFF; SET @@SESSION.SQL_LOG_BIN=0;",
	}
	if databaseName != "" {
		args = append(args, databaseName)
	}
	return doExec(ctx, input, "mysql", args...)
}
