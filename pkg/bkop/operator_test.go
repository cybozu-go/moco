package bkop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	sourceName  = "moco-backup-source"
	restoreName = "moco-restore"
)

var _ = Describe("Operator", func() {
	ctx := context.Background()
	var opBk, opRe Operator
	var bkDB *sqlx.DB
	var baseDir string
	BeforeEach(func() {
		By("preparing databases...")
		sourcePwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())
		restorePwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		err = dbop.RunMySQLOnDocker(sourceName, 2288, 2289)
		Expect(err).NotTo(HaveOccurred())
		err = dbop.RunMySQLOnDocker(restoreName, 2290, 2291)
		Expect(err).NotTo(HaveOccurred())

		err = dbop.ConfigureMySQLOnDocker(sourcePwd, 2288)
		Expect(err).NotTo(HaveOccurred())
		err = dbop.ConfigureMySQLOnDocker(restorePwd, 2290)
		Expect(err).NotTo(HaveOccurred())

		cfg := mysql.NewConfig()
		cfg.User = constants.AdminUser
		cfg.Passwd = sourcePwd.Admin()
		cfg.Net = "tcp"
		cfg.Addr = "localhost:2288"
		cfg.Params = map[string]string{"autocommit": "1"}
		cfg.InterpolateParams = true
		cfg.ParseTime = true
		bkDB, err = sqlx.Connect("mysql", cfg.FormatDSN())
		Expect(err).NotTo(HaveOccurred())

		bkDB.MustExec(`SET GLOBAL read_only=0`)
		bkDB.MustExec(`DROP USER 'root'@'localhost'`)
		bkDB.MustExec(`DROP USER 'root'@'%'`)
		bkDB.MustExec(`FLUSH LOCAL PRIVILEGES`)
		bkDB.MustExec(`CREATE DATABASE foo`)
		bkDB.MustExec(`CREATE TABLE foo.t (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL)`)
		bkDB.MustExec(`INSERT INTO foo.t (data) VALUES ('a')`)

		opBk, err = NewOperator("localhost", 2288, constants.BackupUser, sourcePwd.Backup(), 1)
		Expect(err).NotTo(HaveOccurred())
		opRe, err = NewOperator("localhost", 2290, constants.AdminUser, restorePwd.Admin(), 1)
		Expect(err).NotTo(HaveOccurred())

		err = opBk.Ping()
		Expect(err).NotTo(HaveOccurred())
		err = opRe.Ping()
		Expect(err).NotTo(HaveOccurred())

		opRe.(operator).db.MustExec(`SET GLOBAL read_only=0`)
		opRe.(operator).db.MustExec(`DROP USER 'root'@'localhost'`)
		opRe.(operator).db.MustExec(`DROP USER 'root'@'%'`)
		opRe.(operator).db.MustExec(`FLUSH LOCAL PRIVILEGES`)

		mysqlVersion := os.Getenv("MYSQL_VERSION")
		if strings.HasPrefix(mysqlVersion, "8.4") {
			opRe.(operator).db.MustExec(`RESET BINARY LOGS AND GTIDS`)
		} else {
			opRe.(operator).db.MustExec(`RESET MASTER`)
		}
		opRe.(operator).db.MustExec(`SET GLOBAL super_read_only=1`)

		baseDir, err = os.MkdirTemp("", "")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if opBk != nil {
			opBk.Close()
		}
		if opRe != nil {
			opRe.Close()
		}
		if bkDB != nil {
			bkDB.Close()
		}
		exec.Command("docker", "kill", sourceName).Run()
		exec.Command("docker", "kill", restoreName).Run()
		if baseDir != "" {
			os.RemoveAll(baseDir)
		}
	})

	It("should backup and restore data", func() {
		var gtid1 string
		err := bkDB.Get(&gtid1, `SELECT @@gtid_executed`)
		Expect(err).NotTo(HaveOccurred())

		st1 := &ServerStatus{}
		err = opBk.GetServerStatus(ctx, st1)
		Expect(err).NotTo(HaveOccurred())
		Expect(st1.CurrentBinlog).NotTo(BeEmpty())
		Expect(st1.UUID).NotTo(BeEmpty())
		Expect(st1.SuperReadOnly).To(BeFalse())

		dumpDir := filepath.Join(baseDir, "dump")
		err = os.MkdirAll(dumpDir, 0755)
		Expect(err).NotTo(HaveOccurred())
		err = opBk.DumpFull(ctx, dumpDir)
		Expect(err).NotTo(HaveOccurred())

		dumpGTID, err := GetGTIDExecuted(dumpDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(dumpGTID).To(Equal(gtid1))

		bkDB.MustExec(`FLUSH LOCAL BINARY LOGS`)
		bkDB.MustExec(`INSERT INTO foo.t (data) VALUES ('b')`)
		time.Sleep(1100 * time.Millisecond)
		restorePoint := time.Now()
		time.Sleep(1100 * time.Millisecond)
		bkDB.MustExec(`INSERT INTO foo.t (data) VALUES ('c')`)

		binlogs, err := opBk.GetBinlogs(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(binlogs).To(HaveLen(2))

		binlogDir := filepath.Join(baseDir, "binlog")
		err = os.MkdirAll(binlogDir, 0755)
		Expect(err).NotTo(HaveOccurred())
		err = opBk.DumpBinlog(ctx, binlogDir, st1.CurrentBinlog, dumpGTID)
		Expect(err).NotTo(HaveOccurred())
		entries, err := os.ReadDir(binlogDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))

		bkDB.MustExec(`FLUSH LOCAL BINARY LOGS`)
		bkDB.MustExec(`INSERT INTO foo.t (data) VALUES ('d')`)
		bkDB.MustExec(`PURGE BINARY LOGS TO ?`, entries[1].Name())

		binlogDir2 := filepath.Join(baseDir, "binlog2")
		err = os.MkdirAll(binlogDir2, 0755)
		Expect(err).NotTo(HaveOccurred())
		err = opBk.DumpBinlog(ctx, binlogDir2, st1.CurrentBinlog, dumpGTID)
		Expect(err).To(HaveOccurred())

		st2 := &ServerStatus{}
		err = opRe.GetServerStatus(ctx, st2)
		Expect(err).NotTo(HaveOccurred())
		Expect(st2.CurrentBinlog).NotTo(BeEmpty())
		Expect(st2.UUID).NotTo(BeEmpty())
		Expect(st2.SuperReadOnly).To(BeTrue())

		err = opRe.PrepareRestore(ctx)
		Expect(err).NotTo(HaveOccurred())
		err = opRe.LoadDump(ctx, dumpDir, "")
		Expect(err).NotTo(HaveOccurred())

		var restoredGTID string
		err = opRe.(operator).db.Get(&restoredGTID, `SELECT @@gtid_executed`)
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredGTID).To(Equal(dumpGTID))

		tmpDir := filepath.Join(baseDir, "tmp")
		err = os.MkdirAll(tmpDir, 0755)
		Expect(err).NotTo(HaveOccurred())

		err = opRe.LoadBinlog(ctx, binlogDir, tmpDir, restorePoint, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredGTID).To(Equal(dumpGTID))
		var maxID int
		err = opRe.(operator).db.Get(&maxID, `SELECT MAX(i) FROM foo.t`)
		Expect(err).NotTo(HaveOccurred())
		Expect(maxID).To(Equal(2))

		err = opRe.FinishRestore(ctx)
		Expect(err).NotTo(HaveOccurred())
		var superReadOnly, localInFile bool
		err = opRe.(operator).db.Get(&superReadOnly, `SELECT @@super_read_only`)
		Expect(err).NotTo(HaveOccurred())
		Expect(superReadOnly).To(BeTrue())
		err = opRe.(operator).db.Get(&localInFile, `SELECT @@local_infile`)
		Expect(err).NotTo(HaveOccurred())
		Expect(localInFile).To(BeFalse())
	})
})
