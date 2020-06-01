package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/myso"
	"github.com/cybozu-go/well"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const timeoutSeconds = 30

var (
	initOnceCompletedPath = filepath.Join(myso.MySQLDataPath, "init-once-completed")
	socketPath            = filepath.Join(myso.VarRunPath, "mysqld.sock")
	passwordFilePath      = filepath.Join(myso.TmpPath, "myso-root-password")
	pingConfPath          = filepath.Join(myso.TmpPath, "ping.cnf")
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize MySQL instance",
	Long: fmt.Sprintf(`Initialize MySQL instance managed by MySO.
	If %s already exists, this command does nothing.
	`, initOnceCompletedPath),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		well.Go(func(ctx context.Context) error {
			log.Info("start initialization", nil)
			err := initializeOnce(ctx)
			if err != nil {
				return err
			}

			log.Info("create config file for ping user", nil)
			err = confPingUser(ctx)
			if err != nil {
				return err
			}

			log.Info("create config file for admin interface", nil)
			err = confAdminInterface(ctx, viper.GetString(myso.PodIPFlag))
			if err != nil {
				return err
			}

			log.Info("create config file for server-id", nil)
			err = confServerID(ctx, viper.GetString(myso.PodNameFlag))
			if err != nil {
				return err
			}

			well.Stop()
			return nil
		})

		err := well.Wait()
		if err != nil && !well.IsSignaled(err) {
			log.ErrorExit(err)
		}

		return nil
	},
}

func initializeOnce(ctx context.Context) error {
	_, err := os.Stat(initOnceCompletedPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	skipInitializeOnce := err == nil
	if skipInitializeOnce {
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

	log.Info("setup root user", nil)
	err = initializeRootUser(ctx, viper.GetString(myso.RootPasswordFlag), viper.GetString(myso.PodIPFlag))
	if err != nil {
		return err
	}

	log.Info("setup operator user", nil)
	err = initializeOperatorUser(ctx, viper.GetString(myso.OperatorPasswordFlag))
	if err != nil {
		return err
	}

	log.Info("setup operator-admin users", nil)
	// use the password for an operator-admin user which is the same with the one for operator user
	err = initializeOperatorAdminUser(ctx, viper.GetString(myso.OperatorPasswordFlag))
	if err != nil {
		return err
	}

	log.Info("setup ping user", nil)
	err = initializePingUser(ctx)
	if err != nil {
		return err
	}

	log.Info("sync timezone with system", nil)
	err = importTimeZoneFromHost(ctx)
	if err != nil {
		return err
	}

	log.Info("install plugins", nil)
	err = installPlugins(ctx)
	if err != nil {
		return err
	}

	log.Info("shutdown instance", nil)
	err = shutdownInstance(ctx)
	if err != nil {
		return err
	}

	log.Info("touch "+initOnceCompletedPath, nil)
	return touchInitOnceCompleted(ctx)
}

func initializeInstance(ctx context.Context) error {
	stdout, stderr, err := doExec(ctx, nil, "mysqld", "--initialize-insecure")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}

	cmd := well.CommandContext(ctx, "mysqld", "--skip-networking", "--socket="+socketPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func waitInstanceBootstrap(ctx context.Context) error {
	timeout := time.After(timeoutSeconds * time.Second)
	tick := time.Tick(time.Second)
	for {
		select {
		case <-timeout:
			return errors.New("timed out")
		case <-tick:
			_, _, err := doExec(ctx, nil, "mysqladmin", "--socket="+socketPath, "ping")
			if err == nil {
				return nil
			}
		}
	}
}

func importTimeZoneFromHost(ctx context.Context) error {
	stdout, stderr, err := doExec(ctx, nil, "mysql_tzinfo_to_sql", "/usr/share/zoneinfo")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}

	stdout, stderr, err = execSQL(ctx, stdout, "mysql")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}
	return nil
}

func initializeRootUser(ctx context.Context, rootPassword, rootHost string) error {
	err := ioutil.WriteFile(passwordFilePath, nil, 0600)
	if err != nil {
		return err
	}

	t := template.Must(template.New("sql").Parse(
		`DELETE FROM mysql.user WHERE NOT (user IN ('root', 'mysql.sys', 'mysql.session', 'mysql.infoschema') AND host = 'localhost');
ALTER USER 'root'@'localhost' IDENTIFIED BY '{{ .Password }}';
CREATE USER 'root'@'{{ .Host }}' IDENTIFIED BY '{{ .Password }}';
GRANT ALL ON *.* TO 'root'@'{{ .Host }}' WITH GRANT OPTION ;
GRANT PROXY ON ''@'' TO 'root'@'{{ .Host }}' WITH GRANT OPTION ;
FLUSH PRIVILEGES ;
`))

	sql := new(bytes.Buffer)
	t.Execute(sql, struct {
		Password string
		Host     string
	}{rootPassword, rootHost})

	stdout, stderr, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}

	passwordConf := `[client]
password="%s"
`
	return ioutil.WriteFile(passwordFilePath, []byte(fmt.Sprintf(passwordConf, rootPassword)), 0600)
}

func initializeOperatorUser(ctx context.Context, password string) error {
	t := template.Must(template.New("sql").Parse(`
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
    SELECT,
    SHOW VIEW,
    TRIGGER,
    LOCK TABLES,
    REPLICATION CLIENT,
    BACKUP_ADMIN,
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
	t.Execute(sql, struct {
		User     string
		Password string
	}{myso.OperatorUser, password})

	stdout, stderr, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}
	return nil
}

func initializeOperatorAdminUser(ctx context.Context, password string) error {
	t := template.Must(template.New("sql").Parse(`
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
	ALL
  ON *.* TO '{{ .User }}'@'%' WITH GRANT OPTION ;
`))

	sql := new(bytes.Buffer)
	t.Execute(sql, struct {
		User     string
		Password string
	}{myso.OperatorAdminUser, password})

	stdout, stderr, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}
	return nil
}

func initializePingUser(ctx context.Context) error {
	stdout, stderr, err := execSQL(ctx, []byte("CREATE USER ping@localhost IDENTIFIED BY 'pingpass' ;"), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}
	return nil
}

func confPingUser(ctx context.Context) error {
	conf := `
[client]
user=ping
password=pingpass
socket=%s
`
	return ioutil.WriteFile(pingConfPath, []byte(fmt.Sprintf(conf, socketPath)), 0400)
}

func installPlugins(ctx context.Context) error {
	sql := `INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so';
INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so';
`
	stdout, stderr, err := execSQL(ctx, []byte(sql), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}
	return nil
}

func shutdownInstance(ctx context.Context) error {
	stdout, stderr, err := doExec(ctx, nil,
		"mysqladmin", "--defaults-file="+passwordFilePath, "shutdown", "-uroot", "--socket="+socketPath)
	if err != nil {
		return fmt.Errorf("stdout=%s, stderr=%s, err=%v", stdout, stderr, err)
	}
	return nil
}

func confAdminInterface(ctx context.Context, podIP string) error {
	conf := `
[mysqld]
admin-address=%s
`
	return ioutil.WriteFile(filepath.Join(myso.MySQLConfPath, "admin-interface.cnf"), []byte(fmt.Sprintf(conf, podIP)), 0400)
}

func confServerID(ctx context.Context, podNameWithOrdinal string) error {
	// ordinal should be increased by 1000 because the case server-id is 0 is not suitable for the replication purpose
	const ordinalOffset = 1000

	s := strings.Split(podNameWithOrdinal, "-")
	if len(s) < 2 {
		return errors.New("podName should contain an ordinal with dash, like 'podname-0', at the end: " + podNameWithOrdinal)
	}

	ordinal, err := strconv.Atoi(s[len(s)-1])
	if err != nil {
		return err
	}

	conf := `
[mysqld]
server-id=%d
`
	return ioutil.WriteFile(filepath.Join(myso.MySQLConfPath, "server-id.cnf"), []byte(fmt.Sprintf(conf, ordinal+ordinalOffset)), 0400)
}

func touchInitOnceCompleted(ctx context.Context) error {
	f, err := os.Create(initOnceCompletedPath)
	if err != nil {
		return err
	}
	return f.Close()
}

func doExec(ctx context.Context, input []byte, command string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := well.CommandContext(ctx, command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}

	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func execSQL(ctx context.Context, input []byte, databaseName string) ([]byte, []byte, error) {
	args := []string{
		"--defaults-file=" + passwordFilePath,
		"-uroot",
		"-hlocalhost",
		"--socket=" + socketPath,
		"--init-command=SET @@GLOBAL.SUPER_READ_ONLY=OFF; SET @@GLOBAL.OFFLINE_MODE=OFF; SET @@SESSION.SQL_LOG_BIN=0;",
	}
	if databaseName != "" {
		args = append(args, databaseName)
	}
	return doExec(ctx, input, "mysql", args...)
}

func init() {
	initCmd.Flags().String(myso.PodNameFlag, "", "Pod Name created by StatefulSet")
	initCmd.Flags().String(myso.PodIPFlag, "", "Pod IP address")
	initCmd.Flags().String(myso.OperatorPasswordFlag, "", "Password for both operator user and operator admin user")
	initCmd.Flags().String(myso.RootPasswordFlag, "", "Password for root user")
	err := viper.BindPFlags(initCmd.Flags())
	if err != nil {
		panic(err)
	}
}
