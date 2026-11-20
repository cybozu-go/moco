package dbop

import (
	"context"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// connectTestUser verifies that a user can log in with the given password.
func connectTestUser(port int, user, pwd string) error {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = pwd
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("localhost:%d", port)
	db, err := sqlx.Connect("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	return db.Close()
}

var _ = Describe("auth plugin", func() {
	ctx := context.Background()

	It("should read and change the authentication plugin of MOCO users", func() {
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "authplugin"
		cluster.Spec.Replicas = 1

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		op, err := factory.New(ctx, cluster, passwd, 0)
		Expect(err).NotTo(HaveOccurred())
		defer op.Close()

		port := factory.(*testFactory).instanceMap["test_authplugin"][0]

		By("getting the current plugins")
		plugins, err := op.GetUserAuthPlugins(ctx)
		Expect(err).NotTo(HaveOccurred())
		// the test harness does not create moco-exporter
		Expect(plugins).To(HaveLen(len(constants.MocoUsers) - 1))
		current := plugins[constants.ReadOnlyUser]
		Expect(current).NotTo(BeEmpty())

		By("checking the plugin availability")
		active, err := op.IsAuthPluginActive(ctx, current)
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(BeTrue())
		active, err = op.IsAuthPluginActive(ctx, "no_such_plugin")
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(BeFalse())

		By("rejecting plugins and users outside the allowlists")
		// Assert the reason, not just the failure: under super_read_only the
		// statement would fail on the server too, which must not mask a
		// missing allowlist.
		err = op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, "sha256_password")
		Expect(err).To(MatchError(ContainSubstring("unsupported authentication plugin")))
		err = op.AlterUserAuthPlugin(ctx, "root", current)
		Expect(err).To(MatchError(ContainSubstring("unknown MOCO user")))

		By("failing while super_read_only is on")
		err = op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, current)
		var roErr *mysql.MySQLError
		Expect(errors.As(err, &roErr)).To(BeTrue())
		// ER_OPTION_PREVENTS_STATEMENT: rejected by the server, not by the
		// allowlists.
		Expect(roErr.Number).To(BeEquivalentTo(1290))

		By("clearing only super_read_only")
		Expect(op.SetSuperReadOnly(ctx, false)).To(Succeed())
		st, err := op.GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.GlobalVariables.SuperReadOnly).To(BeFalse())
		Expect(st.GlobalVariables.ReadOnly).To(BeTrue())
		gtidBefore := st.GlobalVariables.ExecutedGTID

		By("not returning accounts other than the MOCO users at host '%'")
		cfg := mysql.NewConfig()
		cfg.User = constants.AdminUser
		cfg.Passwd = passwd.Admin()
		cfg.Net = "tcp"
		cfg.Addr = fmt.Sprintf("localhost:%d", port)
		adminDB, err := sqlx.Connect("mysql", cfg.FormatDSN())
		Expect(err).NotTo(HaveOccurred())
		// Keep the fixture writes out of the binlog so that the GTID check
		// at the end of this test sees only what AlterUserAuthPlugin did.
		adminDB.MustExec(`SET sql_log_bin=0`)
		adminDB.MustExec(`CREATE USER 'app-user'@'%' IDENTIFIED BY 'apppass'`)
		adminDB.MustExec(`CREATE USER 'moco-readonly'@'localhost' IDENTIFIED WITH sha256_password BY 'localpass'`)
		plugins, err = op.GetUserAuthPlugins(ctx)
		Expect(err).NotTo(HaveOccurred())
		// an application account must not be included
		Expect(plugins).NotTo(HaveKey("app-user"))
		Expect(plugins).To(HaveLen(len(constants.MocoUsers) - 1))
		// a same-named account at another host must not shadow the '%' row
		Expect(plugins[constants.ReadOnlyUser]).To(Equal(current))
		adminDB.MustExec(`DROP USER 'app-user'@'%'`)
		adminDB.MustExec(`DROP USER 'moco-readonly'@'localhost'`)
		Expect(adminDB.Close()).To(Succeed())

		By("changing the plugin while keeping the password")
		other := "mysql_native_password"
		if current == other {
			other = "caching_sha2_password"
		}
		// The precheck must predict whether the ALTER below succeeds.
		// On MySQL 8.4, mysql_native_password exists in
		// information_schema.plugins but is DISABLED, which exercises the
		// "present but not ACTIVE" branch of IsAuthPluginActive.
		otherActive, err := op.IsAuthPluginActive(ctx, other)
		Expect(err).NotTo(HaveOccurred())
		err = op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, other)
		var merr *mysql.MySQLError
		if errors.As(err, &merr) && merr.Number == 1524 {
			// mysql_native_password is not enabled on MySQL 8.4 and later
			Expect(otherActive).To(BeFalse())
		} else {
			Expect(otherActive).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())
			plugins, err = op.GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(plugins[constants.ReadOnlyUser]).To(Equal(other))
			Expect(connectTestUser(port, constants.ReadOnlyUser, passwd.ReadOnly())).To(Succeed())
			Expect(op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, current)).To(Succeed())
		}

		By("applying the same plugin idempotently")
		Expect(op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, current)).To(Succeed())
		plugins, err = op.GetUserAuthPlugins(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(plugins[constants.ReadOnlyUser]).To(Equal(current))
		Expect(connectTestUser(port, constants.ReadOnlyUser, passwd.ReadOnly())).To(Succeed())

		By("restoring super_read_only")
		Expect(op.SetSuperReadOnly(ctx, true)).To(Succeed())
		st, err = op.GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.GlobalVariables.SuperReadOnly).To(BeTrue())

		By("checking that no ALTER USER was written to the binlog")
		// AlterUserAuthPlugin must run with sql_log_bin=0; otherwise the
		// statement would be replicated and could create errant GTIDs.
		Expect(st.GlobalVariables.ExecutedGTID).To(Equal(gtidBefore))
	})
})
