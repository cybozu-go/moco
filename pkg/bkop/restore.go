package bkop

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (o operator) PrepareRestore(ctx context.Context) error {
	if _, err := o.db.ExecContext(ctx, `SET GLOBAL local_infile=1`); err != nil {
		return fmt.Errorf("failed to turn on local_infile: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, `SET GLOBAL read_only=0`); err != nil {
		return fmt.Errorf("failed to set read_only=0: %w", err)
	}
	return nil
}

func (o operator) LoadDump(ctx context.Context, dir string, schema string) error {
	args := []string{
		fmt.Sprintf("mysql://%s@%s", o.user, net.JoinHostPort(o.host, fmt.Sprint(o.port))),
		"-p" + o.password,
		"--save-passwords=never",
		"-C", "False",
		"--",
		"util",
		"load-dump",
		dir,
		"--threads=" + fmt.Sprint(o.threads),
		"--loadUsers=true",
		"--analyzeTables=on",
		"--skipBinlog=true",
		"--deferTableIndexes=all",
		"--updateGtidSet=replace",
	}
	if schema != "" {
		args = append(args, "--includeSchemas="+schema)
	}

	cmd := exec.CommandContext(ctx, "mysqlsh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (o operator) LoadBinlog(ctx context.Context, binlogDir, tmpDir string, restorePoint time.Time, schema string) error {
	dirents, err := os.ReadDir(binlogDir)
	if err != nil {
		return err
	}
	var binlogs []string
	for _, e := range dirents {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "binlog.") {
			binlogs = append(binlogs, e.Name())
		}
	}

	if len(binlogs) == 0 {
		return fmt.Errorf("no binlog files in %s", binlogDir)
	}
	SortBinlogs(binlogs)
	binlogFiles := make([]string, len(binlogs))
	for i, n := range binlogs {
		binlogFiles[i] = filepath.Join(binlogDir, n)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create a pipe: %w", err)
	}
	defer func() {
		if pr != nil {
			pr.Close()
		}
		if pw != nil {
			pw.Close()
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	//mysqlbinlog --stop-datetime="2021-05-13 10:45:00" log/binlog.000001 log/binlog.000002
	// | mysql --binary-mode -h moco-single-primary.bar.svc -u moco-admin -p
	binlogArgs := append([]string{"--stop-datetime=" + restorePoint.Format("2006-01-02 15:04:05")}, binlogFiles...)
	if schema != "" {
		binlogArgs = append(binlogArgs, "--database="+schema)
	}
	binlogCmd := exec.CommandContext(ctx, "mysqlbinlog", binlogArgs...)
	binlogCmd.Stdout = pw
	binlogCmd.Stderr = os.Stderr
	env := os.Environ()
	env = append(env, "TZ=Etc/UTC")
	// mysqlbinlog requires enough space to be specified as TMPDIR.
	env = append(env, "TMPDIR="+tmpDir)
	binlogCmd.Env = env

	mysqlArgs := []string{
		"--binary-mode",
		"-h", o.host,
		"--port", fmt.Sprint(o.port),
		"--protocol=tcp",
		"-u", o.user,
		"-p" + o.password,
	}
	mysqlCmd := exec.CommandContext(ctx, "mysql", mysqlArgs...)
	mysqlCmd.Stdin = pr
	mysqlCmd.Stdout = os.Stdout
	mysqlCmd.Stderr = os.Stderr

	if err := binlogCmd.Start(); err != nil {
		return fmt.Errorf("failed to start mysqlbinlog: %w", err)
	}
	pw.Close()
	pw = nil
	if err := mysqlCmd.Run(); err != nil {
		return fmt.Errorf("failed to apply binlog: %w", err)
	}
	if err := binlogCmd.Wait(); err != nil {
		return fmt.Errorf("mysqlbinlog existed abnormally: %w", err)
	}
	return nil
}

func (o operator) FinishRestore(ctx context.Context) error {
	if _, err := o.db.ExecContext(ctx, `SET GLOBAL super_read_only=1`); err != nil {
		return fmt.Errorf("failed to set super_read_only=1: %w", err)
	}
	if _, err := o.db.ExecContext(ctx, `SET GLOBAL local_infile=0`); err != nil {
		return fmt.Errorf("failed to turn off local_infile: %w", err)
	}
	return nil
}
