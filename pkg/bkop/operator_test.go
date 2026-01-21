package bkop

import (
	"context"
	"fmt"
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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	sourceName  = "moco-backup-source"
	restoreName = "moco-restore"
)

// setupTestDatabases creates test databases (db1, db2, db3) with users and sample data.
// This is a helper function for backup/restore tests.
func setupTestDatabases(bkDB *sqlx.DB) {
	// db1: has ALL privilege
	bkDB.MustExec(`CREATE DATABASE IF NOT EXISTS db1`)
	bkDB.MustExec(`CREATE TABLE db1.t1 (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL)`)
	bkDB.MustExec(`INSERT INTO db1.t1 (data) VALUES ('a')`)
	bkDB.MustExec(`CREATE USER 'db1'@'%'`)
	bkDB.MustExec(`GRANT ALL ON db1.* TO 'db1'@'%'`)

	// db2: has ALL privilege
	bkDB.MustExec(`CREATE DATABASE IF NOT EXISTS db2`)
	bkDB.MustExec(`CREATE TABLE db2.t1 (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL)`)
	bkDB.MustExec(`INSERT INTO db2.t1 (data) VALUES ('db2 data')`)
	bkDB.MustExec(`CREATE USER 'db2'@'%'`)
	bkDB.MustExec(`GRANT ALL ON db2.* TO 'db2'@'%'`)

	// db3: has SELECT privilege
	bkDB.MustExec(`CREATE DATABASE IF NOT EXISTS db3`)
	bkDB.MustExec(`CREATE TABLE db3.t1 (i INT PRIMARY KEY AUTO_INCREMENT, data TEXT NOT NULL)`)
	bkDB.MustExec(`INSERT INTO db3.t1 (data) VALUES ('db3 data')`)
	bkDB.MustExec(`CREATE USER 'db3'@'%'`)
	bkDB.MustExec(`GRANT SELECT ON db3.t1 TO 'db3'@'%'`)
}

// executeDumpAndRestoreTest performs a complete dump and restore test with binlog replay.
// It creates a full dump, applies binlog changes up to a restore point, and verifies the restore.
// The restoreSchema and users parameters can be used to filter what gets restored.
func executeDumpAndRestoreTest(ctx context.Context, opBk, opRe Operator, bkDB *sqlx.DB, baseDir string, restoreSchema, users string) {
	// Determine which schema to use for inserting test data
	schema := "db1"
	if restoreSchema != "" {
		schema = restoreSchema
	}

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
	bkDB.MustExec(fmt.Sprintf("INSERT INTO `%s`.`t1` (data) VALUES ('b')", schema))
	time.Sleep(1100 * time.Millisecond)
	restorePoint := time.Now()
	time.Sleep(1100 * time.Millisecond)
	bkDB.MustExec(fmt.Sprintf("INSERT INTO `%s`.`t1` (data) VALUES ('c')", schema))

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
	bkDB.MustExec("INSERT INTO `db1`.`t1` (data) VALUES ('d')")
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
	err = opRe.LoadDump(ctx, dumpDir, restoreSchema, users)
	Expect(err).NotTo(HaveOccurred())

	var restoredGTID string
	err = opRe.(operator).db.Get(&restoredGTID, `SELECT @@gtid_executed`)
	Expect(err).NotTo(HaveOccurred())
	Expect(restoredGTID).To(Equal(dumpGTID))

	tmpDir := filepath.Join(baseDir, "tmp")
	err = os.MkdirAll(tmpDir, 0755)
	Expect(err).NotTo(HaveOccurred())

	err = opRe.LoadBinlog(ctx, binlogDir, tmpDir, restorePoint, restoreSchema)
	Expect(err).NotTo(HaveOccurred())
	Expect(restoredGTID).To(Equal(dumpGTID))
	var maxID int
	err = opRe.(operator).db.Get(&maxID, fmt.Sprintf("SELECT MAX(i) FROM `%s`.`t1`", schema))
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
}

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
		setupTestDatabases(bkDB)

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
		executeDumpAndRestoreTest(ctx, opBk, opRe, bkDB, baseDir, "", "")

		// Verify all databases were restored
		var restoredDBCount int
		err := opRe.(operator).db.Get(&restoredDBCount,
			`SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredDBCount).To(Equal(3))

		// Verify users were restored
		var restoredUserCount int
		err = opRe.(operator).db.Get(&restoredUserCount, `SELECT COUNT(*) FROM mysql.user WHERE user LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredUserCount).To(Equal(3))
	})

	// NOTE: Restore fails if there are users with table privileges outside the restore schema.
	// For example, when restoring schema db1, if user db2 exists with SELECT privilege on schema db2, the restore will fail.
	// This is due to mysqlsh's specification, and moco will not handle this case.
	// https://dev.mysql.com/doc/mysql-shell/8.4/en/mysql-shell-utilities-load-dump.html#mysql-shell-utilities-load-dump-opt-filtering
	// In this test, db3 user has SELECT privilege on db3 schema, so trying to restore only db1 schema would fail.
	It("should backup and restore data with schema", func() {
		restoreSchema := "db3"
		executeDumpAndRestoreTest(ctx, opBk, opRe, bkDB, baseDir, restoreSchema, "")

		// Verify specified database was restored
		var restoredDBCount int
		err := opRe.(operator).db.Get(&restoredDBCount,
			fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '%s'", restoreSchema))
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredDBCount).To(Equal(1))

		// Verify only specified database was restored
		var dbCount int
		err = opRe.(operator).db.Get(&dbCount,
			`SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(dbCount).To(Equal(1))

		// Verify all users were restored
		var restoredUserCount int
		err = opRe.(operator).db.Get(&restoredUserCount, `SELECT COUNT(*) FROM mysql.user WHERE user LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredUserCount).To(Equal(3))
	})

	// Verify that restore becomes possible by explicitly specifying the user.
	It("should backup and restore data with schema and user", func() {
		restoreSchema := "db1"
		restoreUser := "db1"
		executeDumpAndRestoreTest(ctx, opBk, opRe, bkDB, baseDir, restoreSchema, restoreUser)

		// Verify specified database was restored
		var restoredDBCount int
		err := opRe.(operator).db.Get(&restoredDBCount,
			fmt.Sprintf("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '%s'", restoreSchema))
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredDBCount).To(Equal(1))

		// Verify specified user was restored
		var restoredUserCount int
		err = opRe.(operator).db.Get(&restoredUserCount,
			fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE user = '%s'", restoreUser))
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredUserCount).To(Equal(1))

		// Verify only specified databases were restored
		var dbCount int
		err = opRe.(operator).db.Get(&dbCount,
			`SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(dbCount).To(Equal(1))

		// Verify only specified user was restored
		var userCount int
		err = opRe.(operator).db.Get(&userCount, `SELECT COUNT(*) FROM mysql.user WHERE user LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(userCount).To(Equal(1))
	})

	It("should backup and restore data with users", func() {
		restoreUsers := "db1@%,db2@%"
		executeDumpAndRestoreTest(ctx, opBk, opRe, bkDB, baseDir, "", restoreUsers)

		// Verify databases were restored
		var restoredDBCount int
		err := opRe.(operator).db.Get(&restoredDBCount,
			`SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(restoredDBCount).To(Equal(3))

		// Verify db1 user was restored
		var db1UserCount int
		err = opRe.(operator).db.Get(&db1UserCount, `SELECT COUNT(*) FROM mysql.user WHERE user = 'db1' AND host = '%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(db1UserCount).To(Equal(1))

		// Verify db2 user was restored
		var db2UserCount int
		err = opRe.(operator).db.Get(&db2UserCount, `SELECT COUNT(*) FROM mysql.user WHERE user = 'db2' AND host = '%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(db2UserCount).To(Equal(1))

		// Verify only specified users were restored
		var userCount int
		err = opRe.(operator).db.Get(&userCount, `SELECT COUNT(*) FROM mysql.user WHERE user LIKE 'db%'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(userCount).To(Equal(2))
	})
})
