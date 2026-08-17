package dbop

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("password rotation", func() {
	ctx := context.Background()

	It("should rotate, verify, discard, and migrate system user passwords", func() {
		By("preparing a 1 node cluster")
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "password-rotation"
		cluster.Spec.Replicas = 1

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		opIface, err := factory.New(ctx, cluster, passwd, 0)
		Expect(err).NotTo(HaveOccurred())
		op := opIface.(*operator)
		defer op.Close()

		user := constants.AgentUser
		oldPwd := passwd.Agent()
		const newPwd = "moco-rotation-test-new-password"

		By("rejecting user names and plugin names outside the fixed sets")
		Expect(op.RotateUserPassword(ctx, "foo", newPwd)).NotTo(Succeed())
		Expect(op.DiscardOldPassword(ctx, "foo")).NotTo(Succeed())
		_, err = op.HasDualPassword(ctx, "foo")
		Expect(err).To(HaveOccurred())
		_, err = op.VerifyUserPassword(ctx, "foo", newPwd)
		Expect(err).To(HaveOccurred())
		_, err = op.GetUserAuthPlugin(ctx, "foo")
		Expect(err).To(HaveOccurred())
		Expect(op.MigrateUserAuthPlugin(ctx, "foo", newPwd, defaultAuthPlugin)).NotTo(Succeed())
		Expect(op.MigrateUserAuthPlugin(ctx, user, newPwd, "bad-plugin; DROP TABLE mysql.user")).NotTo(Succeed())

		By("reading the server and user auth plugins")
		serverPlugin, err := op.GetAuthPlugin(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(validPluginName.MatchString(serverPlugin)).To(BeTrue())
		userPlugin, err := op.GetUserAuthPlugin(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(userPlugin).NotTo(BeEmpty())

		By("verifying the initial password state")
		hasDual, err := op.HasDualPassword(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasDual).To(BeFalse())
		ok, err := op.VerifyUserPassword(ctx, user, oldPwd)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, err = op.VerifyUserPassword(ctx, user, "wrong-password")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		By("refusing RETAIN while super_read_only is on")
		// The test instance is configured with super_read_only=ON like a
		// replica; the rotation flow must toggle it off first.
		Expect(op.RotateUserPassword(ctx, user, newPwd)).NotTo(Succeed())

		By("running RETAIN with the super_read_only toggle like the rotation flow")
		Expect(op.SetSuperReadOnly(ctx, false)).To(Succeed())
		Expect(op.RotateUserPassword(ctx, user, newPwd)).To(Succeed())
		Expect(op.SetSuperReadOnly(ctx, true)).To(Succeed())

		var superReadOnly bool
		Expect(op.db.GetContext(ctx, &superReadOnly, "SELECT @@super_read_only")).To(Succeed())
		Expect(superReadOnly).To(BeTrue())

		By("checking the dual password state after RETAIN")
		hasDual, err = op.HasDualPassword(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasDual).To(BeTrue())
		ok, err = op.VerifyUserPassword(ctx, user, oldPwd)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "the retained old password must keep authenticating")
		ok, err = op.VerifyUserPassword(ctx, user, newPwd)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "the new primary password must authenticate")

		By("checking the crash-resume skip condition the rotation flow relies on")
		// retainInstanceUsers skips RETAIN on resume when the user already
		// holds a dual password AND the pending password verifies — exactly
		// the state asserted above. Re-running RETAIN instead would replace
		// the secondary with the current (pending) password and drop the old
		// one; the flow must never do that, so this test does not either.

		By("running DISCARD OLD PASSWORD, twice for idempotency")
		Expect(op.SetSuperReadOnly(ctx, false)).To(Succeed())
		Expect(op.DiscardOldPassword(ctx, user)).To(Succeed())
		Expect(op.DiscardOldPassword(ctx, user)).To(Succeed())

		hasDual, err = op.HasDualPassword(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasDual).To(BeFalse())
		ok, err = op.VerifyUserPassword(ctx, user, oldPwd)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "the discarded old password must no longer authenticate")
		ok, err = op.VerifyUserPassword(ctx, user, newPwd)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		By("migrating the auth plugin and re-hashing the password")
		Expect(op.MigrateUserAuthPlugin(ctx, user, newPwd, serverPlugin)).To(Succeed())
		Expect(op.SetSuperReadOnly(ctx, true)).To(Succeed())
		userPlugin, err = op.GetUserAuthPlugin(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(userPlugin).To(Equal(serverPlugin))
		ok, err = op.VerifyUserPassword(ctx, user, newPwd)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "the password must survive the plugin migration")

		By("documenting why the resume path must not re-run RETAIN")
		// A second RETAIN with the same password retains the then-current
		// password as the new secondary, silently dropping the previous one.
		// This is the MySQL semantic the HasDualPassword guard in
		// retainInstanceUsers protects against.
		const newPwd2 = "moco-rotation-test-second-password"
		Expect(op.SetSuperReadOnly(ctx, false)).To(Succeed())
		Expect(op.RotateUserPassword(ctx, user, newPwd2)).To(Succeed())
		Expect(op.RotateUserPassword(ctx, user, newPwd2)).To(Succeed())
		Expect(op.SetSuperReadOnly(ctx, true)).To(Succeed())
		ok, err = op.VerifyUserPassword(ctx, user, newPwd)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "a re-run RETAIN replaces the secondary and drops the previous current password")
		ok, err = op.VerifyUserPassword(ctx, user, newPwd2)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		By("checking that none of the ALTER USER statements reached the binlog")
		var infos []struct {
			Info string `db:"Info"`
		}
		Expect(op.db.Unsafe().SelectContext(ctx, &infos, "SHOW BINLOG EVENTS")).To(Succeed())
		for _, ev := range infos {
			Expect(ev.Info).NotTo(ContainSubstring("ALTER USER"),
				"rotation SQL must run with sql_log_bin=0")
		}
	})
})
