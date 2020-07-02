package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const timeoutDuration = 30 * time.Second

var (
	initOnceCompletedPath = filepath.Join(moco.MySQLDataPath, "init-once-completed")
	socketPath            = filepath.Join(moco.VarRunPath, "mysqld.sock")
	passwordFilePath      = filepath.Join(moco.TmpPath, "moco-root-password")
	pingConfPath          = filepath.Join(moco.TmpPath, "ping.cnf")
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize MySQL instance",
	Long: fmt.Sprintf(`Initialize MySQL instance managed by MOCO.
	If %s already exists, this command does nothing.
	`, initOnceCompletedPath),
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

			return nil
		})

		well.Stop()
		err := well.Wait()
		if err != nil {
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

	log.Info("setup root user", nil)
	err = initializeRootUser(ctx, viper.GetString(moco.RootPasswordFlag), viper.GetString(moco.PodIPFlag))
	if err != nil {
		return err
	}

	log.Info("setup operator user", nil)
	err = initializeOperatorUser(ctx, viper.GetString(moco.OperatorPasswordFlag))
	if err != nil {
		return err
	}

	log.Info("setup operator-admin users", nil)
	// use the password for an operator-admin user which is the same with the one for operator user
	err = initializeOperatorAdminUser(ctx, viper.GetString(moco.OperatorPasswordFlag))
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
	out, err := doExec(ctx, nil, "mysqld", "--defaults-file="+filepath.Join(moco.MySQLConfPath, moco.MySQLConfName), "--initialize-insecure")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	cmd := well.CommandContext(ctx, "mysqld", "--skip-networking", "--socket="+socketPath)
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
			_, err := doExec(ctx, nil, "mysqladmin", "--socket="+socketPath, "ping")
			if err == nil {
				return nil
			}
		}
	}
}

func importTimeZoneFromHost(ctx context.Context) error {
	out, err := doExec(ctx, nil, "mysql_tzinfo_to_sql", "/usr/share/zoneinfo")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	out, err = execSQL(ctx, out, "mysql")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
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

	out, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
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
	}{moco.OperatorUser, password})

	out, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
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
	}{moco.OperatorAdminUser, password})

	out, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func initializePingUser(ctx context.Context) error {
	out, err := execSQL(ctx, []byte("CREATE USER ping@localhost IDENTIFIED BY 'pingpass' ;"), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
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
	out, err := execSQL(ctx, []byte(sql), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func shutdownInstance(ctx context.Context) error {
	out, err := doExec(ctx, nil,
		"mysqladmin", "--defaults-file="+passwordFilePath, "shutdown", "-uroot", "--socket="+socketPath)
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func touchInitOnceCompleted(ctx context.Context) error {
	f, err := os.Create(initOnceCompletedPath)
	if err != nil {
		return err
	}
	return f.Close()
}

func doExec(ctx context.Context, input []byte, command string, args ...string) ([]byte, error) {
	cmd := well.CommandContext(ctx, command, args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}

	return cmd.Output()
}

func execSQL(ctx context.Context, input []byte, databaseName string) ([]byte, error) {
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
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().String(moco.PodNameFlag, "", "Pod Name created by StatefulSet")
	initCmd.Flags().String(moco.PodIPFlag, "", "Pod IP address")
	initCmd.Flags().String(moco.OperatorPasswordFlag, "", "Password for both operator user and operator admin user")
	initCmd.Flags().String(moco.RootPasswordFlag, "", "Password for root user")
	err := viper.BindPFlags(initCmd.Flags())
	if err != nil {
		panic(err)
	}
}
