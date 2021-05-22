package bkop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cybozu-go/moco/pkg/constants"
)

func (o operator) GetServerStatus(ctx context.Context, st *ServerStatus) error {
	ms := &showMasterStatus{}
	if err := o.db.GetContext(ctx, ms, `SHOW MASTER STATUS`); err != nil {
		return fmt.Errorf("failed to show master status: %w", err)
	}

	var uuid string
	if err := o.db.GetContext(ctx, &uuid, `SELECT @@server_uuid`); err != nil {
		return fmt.Errorf("failed to get server_uuid: %w", err)
	}

	st.CurrentBinlog = ms.File
	st.UUID = uuid
	return nil
}

func (o operator) DumpFull(ctx context.Context, dir string) error {
	args := []string{
		fmt.Sprintf("mysql://%s@%s:%d", o.user, o.host, o.port),
		"--passwords-from-stdin",
		"--save-passwords=never",
		"-C", "False",
		"--",
		"util",
		"dump-instance",
		dir,
		"--excludeUsers=" + strings.Join(constants.MocoUsers, ","),
		"--threads=" + fmt.Sprint(o.threads),
	}

	cmd := exec.CommandContext(ctx, "mysqlsh", args...)
	cmd.Stdin = strings.NewReader(o.password)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (o operator) GetBinlogs(ctx context.Context) ([]string, error) {
	var binlogs []showBinaryLogs
	if err := o.db.SelectContext(ctx, &binlogs, `SHOW BINARY LOGS`); err != nil {
		return nil, fmt.Errorf("failed to show binary logs: %w", err)
	}

	r := make([]string, len(binlogs))
	for i, row := range binlogs {
		r[i] = row.LogName
	}
	return r, nil
}

func (o operator) DumpBinlog(ctx context.Context, dir, binlogName, filterGTID string) error {
	args := []string{
		"-h", o.host,
		"--port", fmt.Sprint(o.port),
		"--protocol=tcp",
		"-u", o.user,
		"-p" + o.password,
		"--get-server-public-key",
		"--read-from-remote-master=BINLOG-DUMP-GTIDS",
		"--exclude-gtids=" + filterGTID,
		"-t",
		"--raw",
		"--result-file=" + dir + "/",
		binlogName,
	}

	cmd := exec.CommandContext(ctx, "mysqlbinlog", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
