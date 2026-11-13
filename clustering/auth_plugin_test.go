package clustering

// The division of labor among the test layers:
//
//   - pkg/dbop/auth_plugin_test.go: the SQL layer against a real mysqld
//     (password preservation, binlog, super_read_only errors).
//   - this file: which users are picked, which instances are skipped,
//     how observations map to the condition.
//   - manager_test.go: the integration with the clustering loop
//     (runs only in StateHealthy, retries on ticks, updates the API object).

import (
	"context"
	"strings"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

const (
	testTarget = "caching_sha2_password"
	testOther  = "mysql_native_password"
)

// authPluginSt builds the observation of one instance whose users all use
// the given plugin.
func authPluginSt(plugin string, targetActive bool) *InstanceAuthPluginStatus {
	return &InstanceAuthPluginStatus{
		UserPlugins:        map[string]string{constants.AdminUser: plugin},
		TargetPluginActive: targetActive,
	}
}

// expectAllPluginsConverged checks that every MOCO user on every instance of
// the cluster uses the given authentication plugin. The manager integration
// specs (manager_test.go) share this helper.
func expectAllPluginsConverged(of *mockOpFactory, cluster *mocov1beta2.MySQLCluster, plugin string) {
	GinkgoHelper()
	for i := range int(cluster.Spec.Replicas) {
		plugins := of.getAuthPlugins(cluster.PodHostname(i))
		Expect(plugins).NotTo(BeNil())
		for _, u := range constants.MocoUsers {
			Expect(plugins[u]).To(Equal(plugin), "instance %d user %s", i, u)
		}
	}
}

var _ = Describe("authPluginCondition", func() {
	newStatusSet := func(statuses ...*InstanceAuthPluginStatus) *StatusSet {
		target := mocov1beta2.SystemUserAuthPlugin(testTarget)
		ss := &StatusSet{Cluster: &mocov1beta2.MySQLCluster{}}
		ss.Cluster.Spec.SystemUserAuthPlugin = &target
		ss.AuthPluginStatus = statuses
		return ss
	}

	DescribeTable("mapping observations to the condition",
		func(statuses []*InstanceAuthPluginStatus, wantStatus metav1.ConditionStatus, wantReason string) {
			cond := authPluginCondition(newStatusSet(statuses...))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(wantStatus))
			Expect(cond.Reason).To(Equal(wantReason))
		},
		Entry("every instance synced",
			[]*InstanceAuthPluginStatus{authPluginSt(testTarget, true), authPluginSt(testTarget, true)},
			metav1.ConditionTrue, "Synced"),
		Entry("an instance with users to migrate",
			[]*InstanceAuthPluginStatus{authPluginSt(testTarget, true), authPluginSt(testOther, true)},
			metav1.ConditionFalse, "Applying"),
		Entry("an instance where the target plugin is not active",
			[]*InstanceAuthPluginStatus{authPluginSt(testTarget, true), authPluginSt(testOther, false)},
			metav1.ConditionFalse, "PluginUnavailable"),
		Entry("an instance that could not be observed",
			[]*InstanceAuthPluginStatus{authPluginSt(testTarget, true), nil},
			metav1.ConditionUnknown, "InstanceUnreachable"),
		Entry("an inactive plugin with nothing to migrate counts as synced",
			[]*InstanceAuthPluginStatus{authPluginSt(testTarget, false)},
			metav1.ConditionTrue, "Synced"),
		Entry("PluginUnavailable wins over Applying and InstanceUnreachable",
			[]*InstanceAuthPluginStatus{authPluginSt(testOther, true), authPluginSt(testOther, false), nil},
			metav1.ConditionFalse, "PluginUnavailable"),
		Entry("Applying wins over InstanceUnreachable",
			[]*InstanceAuthPluginStatus{authPluginSt(testOther, true), nil},
			metav1.ConditionFalse, "Applying"),
	)

	It("returns nil while the field is unset", func() {
		ss := &StatusSet{Cluster: &mocov1beta2.MySQLCluster{}}
		Expect(authPluginCondition(ss)).To(BeNil())
	})
})

var _ = Describe("convergeAuthPlugin", func() {
	ctx := context.Background()

	var (
		p        *managerProcess
		recorder *record.FakeRecorder
		of       *mockOpFactory
		ss       *StatusSet
		cluster  *mocov1beta2.MySQLCluster
	)

	// setup builds a cluster of mock instances whose users all use
	// mysql_native_password (the mock default). Instance 0 is the primary;
	// unless the cluster replicates from an external source, it runs
	// writable as the real configure step would leave it.
	setup := func(replicas int, externalSource bool) {
		cluster = &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "unit"
		cluster.Name = "converge"
		cluster.Spec.Replicas = int32(replicas)
		target := mocov1beta2.SystemUserAuthPlugin(testTarget)
		cluster.Spec.SystemUserAuthPlugin = &target
		if externalSource {
			source := "source"
			cluster.Spec.ReplicationSourceSecretName = &source
		}

		of = newMockOpFactory()
		recorder = record.NewFakeRecorder(100)
		p = &managerProcess{recorder: recorder}

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		ss = &StatusSet{
			Cluster:          cluster,
			Primary:          0,
			DBOps:            make([]dbop.Operator, replicas),
			AuthPluginStatus: make([]*InstanceAuthPluginStatus, replicas),
		}
		for i := range replicas {
			op, err := of.New(ctx, cluster, passwd, i)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(op.Close)
			ss.DBOps[i] = op
			plugins, err := op.GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			ss.AuthPluginStatus[i] = &InstanceAuthPluginStatus{UserPlugins: plugins, TargetPluginActive: true}
		}
		if !externalSource {
			of.setSuperReadOnly(cluster.PodHostname(0), false)
		}
	}

	eventReasons := func() []string {
		var reasons []string
		for {
			select {
			case e := <-recorder.Events:
				fields := strings.Fields(e)
				// a FakeRecorder event is "<type> <reason> <message>"
				reasons = append(reasons, fields[1])
			default:
				return reasons
			}
		}
	}

	It("converges every instance, toggling super_read_only only on replicas", func() {
		setup(3, false)

		Expect(p.convergeAuthPlugin(ctx, ss)).To(Succeed())

		expectAllPluginsConverged(of, cluster, testTarget)
		// If the primary had been toggled, the deferred restore would have
		// left it with super_read_only on.
		Expect(of.getInstanceStatus(cluster.PodHostname(0)).GlobalVariables.SuperReadOnly).To(BeFalse())
		for i := 1; i < 3; i++ {
			Expect(of.getInstanceStatus(cluster.PodHostname(i)).GlobalVariables.SuperReadOnly).To(BeTrue())
		}
		// The mock rejects ALTER USER while super_read_only is on, so the
		// convergence above also proves the toggle ordering on replicas.
		Expect(eventReasons()).To(Equal([]string{
			event.AuthPluginChanged.Reason,
			event.AuthPluginChanged.Reason,
			event.AuthPluginChanged.Reason,
		}))
	})

	It("toggles super_read_only on an intermediate primary and restores it", func() {
		setup(1, true)

		Expect(p.convergeAuthPlugin(ctx, ss)).To(Succeed())

		expectAllPluginsConverged(of, cluster, testTarget)
		Expect(of.getInstanceStatus(cluster.PodHostname(0)).GlobalVariables.SuperReadOnly).To(BeTrue())
	})

	It("skips an instance whose state was not collected", func() {
		setup(3, false)
		ss.AuthPluginStatus[1] = nil

		Expect(p.convergeAuthPlugin(ctx, ss)).To(Succeed())

		Expect(of.getAuthPlugins(cluster.PodHostname(1))[constants.AdminUser]).To(Equal(testOther))
		Expect(of.getAuthPlugins(cluster.PodHostname(0))[constants.AdminUser]).To(Equal(testTarget))
		Expect(of.getAuthPlugins(cluster.PodHostname(2))[constants.AdminUser]).To(Equal(testTarget))
	})

	It("skips a user that is missing from the observation", func() {
		setup(3, false)
		of.deleteAuthPluginUser(cluster.PodHostname(1), constants.BackupUser)
		plugins, err := ss.DBOps[1].GetUserAuthPlugins(ctx)
		Expect(err).NotTo(HaveOccurred())
		ss.AuthPluginStatus[1].UserPlugins = plugins

		// The mock rejects ALTER USER for an unknown user, so succeeding
		// proves that the missing user was never ALTERed.
		Expect(p.convergeAuthPlugin(ctx, ss)).To(Succeed())

		remaining := of.getAuthPlugins(cluster.PodHostname(1))
		Expect(remaining).NotTo(HaveKey(constants.BackupUser))
		for _, u := range constants.MocoUsers {
			if u == constants.BackupUser {
				continue
			}
			Expect(remaining[u]).To(Equal(testTarget), "user %s", u)
		}
	})

	It("skips an instance where the target plugin is not active, without touching super_read_only", func() {
		setup(3, false)
		ss.AuthPluginStatus[1].TargetPluginActive = false

		Expect(p.convergeAuthPlugin(ctx, ss)).To(Succeed())

		Expect(of.getAuthPlugins(cluster.PodHostname(1))[constants.AdminUser]).To(Equal(testOther))
		Expect(of.getInstanceStatus(cluster.PodHostname(1)).GlobalVariables.SuperReadOnly).To(BeTrue())
		Expect(eventReasons()).To(HaveLen(2))
	})

	It("does not ALTER anything when every user already uses the target", func() {
		setup(3, false)
		Expect(p.convergeAuthPlugin(ctx, ss)).To(Succeed())
		for i := range 3 {
			plugins, err := ss.DBOps[i].GetUserAuthPlugins(ctx)
			Expect(err).NotTo(HaveOccurred())
			ss.AuthPluginStatus[i].UserPlugins = plugins
		}
		_ = eventReasons() // drain the events of the first convergence

		Expect(p.convergeAuthPlugin(ctx, ss)).To(Succeed())

		Expect(eventReasons()).To(BeEmpty())
	})

	It("stops at the first failure, emits an event, and restores super_read_only", func() {
		setup(3, false)
		of.setFailAlterOnce(cluster.PodHostname(1))

		err := p.convergeAuthPlugin(ctx, ss)

		Expect(err).To(MatchError(ContainSubstring("instance 1")))
		// the deferred restore must run even on failure
		Expect(of.getInstanceStatus(cluster.PodHostname(1)).GlobalVariables.SuperReadOnly).To(BeTrue())
		// the instance after the failing one is left to the next tick
		Expect(of.getAuthPlugins(cluster.PodHostname(2))[constants.AdminUser]).To(Equal(testOther))
		Expect(eventReasons()).To(Equal([]string{
			event.AuthPluginChanged.Reason,
			event.AuthPluginChangeFailed.Reason,
		}))
	})
})
