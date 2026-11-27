package dbop

// The specs share one mysqld: each starts from the canonical state
// (super_read_only=ON) and restores what it changes.

import (
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

// The MOCO users are created with the first password the factory sees,
// so every spec reuses this one.
var authPluginPasswd *password.MySQLPassword

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
	var op Operator
	var port int

	BeforeEach(func(ctx SpecContext) {
		if authPluginPasswd == nil {
			var err error
			authPluginPasswd, err = password.NewMySQLPassword()
			Expect(err).NotTo(HaveOccurred())
		}

		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "authplugin"
		cluster.Spec.Replicas = 1

		var err error
		op, err = factory.New(ctx, cluster, authPluginPasswd, 0)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(op.Close()).To(Succeed())
		})
		port = factory.(*testFactory).instanceMap["test_authplugin"][0]
	})

	// derived, not assumed: keeps the specs independent of order and version
	currentPlugin := func(ctx SpecContext) string {
		GinkgoHelper()
		plugins, err := op.GetUserAuthPlugins(ctx)
		Expect(err).NotTo(HaveOccurred())
		current := plugins[constants.ReadOnlyUser]
		Expect(current).NotTo(BeEmpty())
		return current
	}

	otherPlugin := func(current string) string {
		if current == mocov1beta2.AuthPluginMySQLNativePassword.String() {
			return mocov1beta2.AuthPluginCachingSHA2Password.String()
		}
		return mocov1beta2.AuthPluginMySQLNativePassword.String()
	}

	disableSuperReadOnly := func(ctx SpecContext) {
		GinkgoHelper()
		Expect(op.SetSuperReadOnly(ctx, false)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			Expect(op.SetSuperReadOnly(ctx, true)).To(Succeed())
		})
	}

	Describe("GetUserAuthPlugins", func() {
		It("returns the plugin of every MOCO user", func(ctx SpecContext) {
			plugins, err := op.GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(plugins).To(HaveLen(len(constants.MocoUsers)))
			for _, u := range constants.MocoUsers {
				Expect(plugins[u]).NotTo(BeEmpty(), "user %s", u)
			}
		})

		It("ignores non-MOCO accounts and accounts at other hosts", func(ctx SpecContext) {
			current := currentPlugin(ctx)
			disableSuperReadOnly(ctx)

			cfg := mysql.NewConfig()
			cfg.User = constants.AdminUser
			cfg.Passwd = authPluginPasswd.Admin()
			cfg.Net = "tcp"
			cfg.Addr = fmt.Sprintf("localhost:%d", port)
			adminDB, err := sqlx.Connect("mysql", cfg.FormatDSN())
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				adminDB.MustExec(`DROP USER IF EXISTS 'app-user'@'%'`)
				adminDB.MustExec(`DROP USER IF EXISTS 'moco-readonly'@'localhost'`)
				Expect(adminDB.Close()).To(Succeed())
			})
			// keep the fixtures out of the binlog like MOCO's own user management
			adminDB.MustExec(`SET sql_log_bin=0`)
			adminDB.MustExec(`CREATE USER 'app-user'@'%' IDENTIFIED BY 'apppass'`)
			adminDB.MustExec(`CREATE USER 'moco-readonly'@'localhost' IDENTIFIED WITH sha256_password BY 'localpass'`)

			plugins, err := op.GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(plugins).NotTo(HaveKey("app-user"))
			Expect(plugins).To(HaveLen(len(constants.MocoUsers)))
			// a same-named account at another host must not shadow the '%' row
			Expect(plugins[constants.ReadOnlyUser]).To(Equal(current))
		})
	})

	Describe("IsAuthPluginActive", func() {
		It("reports active plugins and an unknown plugin", func(ctx SpecContext) {
			current := currentPlugin(ctx)
			active, err := op.IsAuthPluginActive(ctx, current)
			Expect(err).NotTo(HaveOccurred())
			Expect(active).To(BeTrue())

			// mysql_native_password is enabled even on 8.4 (see RunMySQLOnDocker)
			active, err = op.IsAuthPluginActive(ctx, otherPlugin(current))
			Expect(err).NotTo(HaveOccurred())
			Expect(active).To(BeTrue())

			active, err = op.IsAuthPluginActive(ctx, "no_such_plugin")
			Expect(err).NotTo(HaveOccurred())
			Expect(active).To(BeFalse())
		})
	})

	Describe("AlterUserAuthPlugin", func() {
		It("rejects plugins and users outside the allowlists", func(ctx SpecContext) {
			// assert the reason: the server would reject these under
			// super_read_only too, masking a missing allowlist
			err := op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, "sha256_password")
			Expect(err).To(MatchError(ContainSubstring("unsupported authentication plugin")))
			err = op.AlterUserAuthPlugin(ctx, "root", mocov1beta2.AuthPluginCachingSHA2Password.String())
			Expect(err).To(MatchError(ContainSubstring("unknown MOCO user")))
		})

		It("fails on the server while super_read_only is on", func(ctx SpecContext) {
			err := op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, currentPlugin(ctx))
			merr, ok := errors.AsType[*mysql.MySQLError](err)
			Expect(ok).To(BeTrue())
			// ER_OPTION_PREVENTS_STATEMENT: rejected by the server, not the allowlists
			Expect(merr.Number).To(BeEquivalentTo(1290))
		})

		It("changes the plugin while keeping the password, without writing the binlog", func(ctx SpecContext) {
			current := currentPlugin(ctx)
			other := otherPlugin(current)

			disableSuperReadOnly(ctx)
			st, err := op.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			gtidBefore := st.GlobalVariables.ExecutedGTID

			Expect(op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, other)).To(Succeed())
			plugins, err := op.GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(plugins[constants.ReadOnlyUser]).To(Equal(other))
			Expect(connectTestUser(port, constants.ReadOnlyUser, authPluginPasswd.ReadOnly())).To(Succeed())

			// changing back also proves the reverse direction
			Expect(op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, current)).To(Succeed())
			plugins, err = op.GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(plugins[constants.ReadOnlyUser]).To(Equal(current))
			Expect(connectTestUser(port, constants.ReadOnlyUser, authPluginPasswd.ReadOnly())).To(Succeed())

			// no binlog writes: a replicated ALTER USER could create errant GTIDs
			st, err = op.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.GlobalVariables.ExecutedGTID).To(Equal(gtidBefore))
		})

		It("applies the same plugin idempotently", func(ctx SpecContext) {
			current := currentPlugin(ctx)
			disableSuperReadOnly(ctx)

			Expect(op.AlterUserAuthPlugin(ctx, constants.ReadOnlyUser, current)).To(Succeed())
			plugins, err := op.GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(plugins[constants.ReadOnlyUser]).To(Equal(current))
			Expect(connectTestUser(port, constants.ReadOnlyUser, authPluginPasswd.ReadOnly())).To(Succeed())
		})
	})

	Describe("SetSuperReadOnly", func() {
		It("clears only super_read_only and sets it back", func(ctx SpecContext) {
			disableSuperReadOnly(ctx)
			st, err := op.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.GlobalVariables.SuperReadOnly).To(BeFalse())
			Expect(st.GlobalVariables.ReadOnly).To(BeTrue())

			Expect(op.SetSuperReadOnly(ctx, true)).To(Succeed())
			st, err = op.GetStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(st.GlobalVariables.SuperReadOnly).To(BeTrue())
			Expect(st.GlobalVariables.ReadOnly).To(BeTrue())
		})
	})
})
