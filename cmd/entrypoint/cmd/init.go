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
	passwordFilePath      = filepath.Join(moco.TmpPath, "moco-root-password")
	miscConfPath          = filepath.Join(moco.MySQLDataPath, "misc.cnf")
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

			// Put preparation steps which should be executed at every startup.

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
	err = initializeRootUser(ctx, os.Getenv(moco.RootPasswordEnvName), viper.GetString(moco.PodIPFlag))
	if err != nil {
		return err
	}

	log.Info("setup operator user", nil)
	err = initializeOperatorUser(ctx, os.Getenv(moco.OperatorPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup operator-admin users", nil)
	// use the password for an operator-admin user which is the same with the one for operator user
	err = initializeOperatorAdminUser(ctx, os.Getenv(moco.OperatorPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup donor user", nil)
	err = initializeDonorUser(ctx, os.Getenv(moco.ClonePasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup replication user", nil)
	err = initializeReplicationUser(ctx, os.Getenv(moco.ReplicationPasswordEnvName))
	if err != nil {
		return err
	}

	log.Info("setup misc user", nil)
	err = initializeMiscUser(ctx, os.Getenv(moco.MiscPasswordEnvName))
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
	f, err := ioutil.ReadFile(filepath.Join(moco.MySQLConfPath, moco.MySQLConfName))
	if err != nil {
		return err
	}

	fmt.Println(string(f))

	var stdOut, stdErr bytes.Buffer
	cmd := well.CommandContext(ctx, "ls", moco.MySQLDataPath)
	cmd.Stdout = &stdOut
	cmd.Stderr = &stdErr

	err = cmd.Run()
	fmt.Println(err, stdOut.String(), stdErr.String())

	out, err := doExec(ctx, nil, "mysqld", "--defaults-file="+filepath.Join(moco.MySQLConfPath, moco.MySQLConfName), "--initialize-insecure")
	if err != nil {
		f, err := ioutil.ReadFile("/var/log/mysql/mysql.err")
		if err != nil {
			return err
		}

		fmt.Println(string(f))

		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	cmd = well.CommandContext(ctx, "mysqld", "--skip-networking")
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
	if rootPassword == "" {
		return fmt.Errorf("root password is not set")
	}
	// execSQL requires the password file.
	conf := `[client]
user=root
`
	err := ioutil.WriteFile(passwordFilePath, []byte(conf), 0600)
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
user=root
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

func initializeDonorUser(ctx context.Context, password string) error {
	t := template.Must(template.New("sql").Parse(`
CREATE USER '{{ .User }}'@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
	BACKUP_ADMIN
  ON *.* TO '{{ .User }}'@'%' WITH GRANT OPTION ;
`))

	sql := new(bytes.Buffer)
	t.Execute(sql, struct {
		User     string
		Password string
	}{moco.DonorUser, password})

	out, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	return ioutil.WriteFile(moco.DonorPasswordPath, []byte(password), 0400)
}

func initializeReplicationUser(ctx context.Context, password string) error {
	// Use mysql_native_password because no ssl connections without sha-2 cache fail
	// Will fix it when we work on replication with encrypted connection
	// See https://yoku0825.blogspot.com/2018/10/mysql-80cachingsha2password-ssl.html
	t := template.Must(template.New("sql").Parse(`
CREATE USER '{{ .User }}'@'%' IDENTIFIED WITH mysql_native_password BY '{{ .Password }}' ;
GRANT
    REPLICATION SLAVE,
    REPLICATION CLIENT
  ON *.* TO '{{ .User }}'@'%' WITH GRANT OPTION ;
`))

	sql := new(bytes.Buffer)
	t.Execute(sql, struct {
		User     string
		Password string
	}{moco.ReplicatorUser, password})

	out, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func initializeMiscUser(ctx context.Context, password string) error {
	t := template.Must(template.New("sql").Parse(`
CREATE USER misc@'%' IDENTIFIED BY '{{ .Password }}' ;
GRANT
	RELOAD,
	CLONE_ADMIN
  ON *.* TO misc@'%' ;
`))

	sql := new(bytes.Buffer)
	t.Execute(sql, struct {
		Password string
	}{Password: password})

	out, err := execSQL(ctx, sql.Bytes(), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}

	conf := `
[client]
user=misc
password=%s
`
	if err := ioutil.WriteFile(miscConfPath, []byte(fmt.Sprintf(conf, password)), 0400); err != nil {
		return err
	}

	return ioutil.WriteFile(moco.MiscPasswordPath, []byte(password), 0400)
}

func installPlugins(ctx context.Context) error {
	sql := `INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so';
INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so';
INSTALL PLUGIN clone SONAME 'mysql_clone.so';
`
	out, err := execSQL(ctx, []byte(sql), "")
	if err != nil {
		return fmt.Errorf("stdout=%s, err=%v", out, err)
	}
	return nil
}

func shutdownInstance(ctx context.Context) error {
	out, err := doExec(ctx, nil,
		"mysqladmin", "--defaults-extra-file="+passwordFilePath, "shutdown")
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

	var stdOut, stdErr bytes.Buffer
	cmd.Stdout = &stdOut
	cmd.Stderr = &stdErr

	err := cmd.Run()
	return append(stdOut.Bytes(), stdErr.Bytes()...), err
	// return cmd.Output()
}

func execSQL(ctx context.Context, input []byte, databaseName string) ([]byte, error) {
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

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().String(moco.PodNameFlag, "", "Pod Name created by StatefulSet")
	initCmd.Flags().String(moco.PodIPFlag, "", "Pod IP address")
	err := viper.BindPFlags(initCmd.Flags())
	if err != nil {
		panic(err)
	}
}
