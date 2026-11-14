package dbop

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("semisync", func() {

	ctx := context.Background()

	It("should work with both the source/replica and the master/slave plugins", func() {
		By("preparing a 2 node cluster")
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "semisync"
		cluster.Spec.Replicas = 2

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		ops := make([]*operator, cluster.Spec.Replicas)
		for i := 0; i < int(cluster.Spec.Replicas); i++ {
			op, err := factory.New(ctx, cluster, passwd, i)
			Expect(err).NotTo(HaveOccurred())
			ops[i] = op.(*operator)
		}
		defer func() {
			for _, op := range ops {
				op.Close()
			}
		}()

		By("detecting the source/replica plugins")
		names, err := detectSemiSyncNames(ctx, ops[0].db)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal(sourceReplicaNames))

		By("replacing the plugins with the master/slave plugins")
		for _, op := range ops {
			execWritable(op,
				`UNINSTALL PLUGIN rpl_semi_sync_source`,
				`UNINSTALL PLUGIN rpl_semi_sync_replica`,
				`INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'`,
				`INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'`,
			)
		}
		names, err = detectSemiSyncNames(ctx, ops[0].db)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal(masterSlaveNames))

		By("checking the initial status with the master/slave plugins")
		st0, err := ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st0.GlobalVariables.WaitForReplicaCount).To(Equal(1))
		Expect(st0.GlobalVariables.SemiSyncSourceEnabled).To(BeFalse())
		Expect(st0.GlobalVariables.SemiSyncReplicaEnabled).To(BeFalse())
		Expect(st0.GlobalStatus.SemiSyncSourceWaitSessions).To(Equal(0))

		By("configuring semi-sync replication with the master/slave plugins")
		err = ops[0].ConfigurePrimary(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		err = ops[1].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 0),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, true)
		Expect(err).NotTo(HaveOccurred())

		st0, err = ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st0.GlobalVariables.WaitForReplicaCount).To(Equal(1))
		Expect(st0.GlobalVariables.SemiSyncSourceEnabled).To(BeTrue())
		st1, err := ops[1].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st1.GlobalVariables.SemiSyncSourceEnabled).To(BeFalse())
		Expect(st1.GlobalVariables.SemiSyncReplicaEnabled).To(BeTrue())

		By("detecting no plugin")
		_, err = ops[1].db.Exec(`STOP REPLICA`)
		Expect(err).NotTo(HaveOccurred())
		execWritable(ops[1],
			`UNINSTALL PLUGIN rpl_semi_sync_master`,
			`UNINSTALL PLUGIN rpl_semi_sync_slave`,
		)
		names, err = detectSemiSyncNames(ctx, ops[1].db)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal(sourceReplicaNames))
		_, err = ops[1].GetStatus(ctx)
		Expect(err).To(HaveOccurred())

		By("detecting the new source plugin and the legacy replica plugin")
		execWritable(ops[1],
			`INSTALL PLUGIN rpl_semi_sync_source SONAME 'semisync_source.so'`,
			`INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'`,
		)
		names, err = detectSemiSyncNames(ctx, ops[1].db)
		Expect(err).NotTo(HaveOccurred())
		Expect(names.SourceEnabled).To(Equal(sourceReplicaNames.SourceEnabled))
		Expect(names.WaitForReplicaCount).To(Equal(sourceReplicaNames.WaitForReplicaCount))
		Expect(names.ReplicaEnabled).To(Equal(masterSlaveNames.ReplicaEnabled))

		By("configuring semi-sync replication with the mixed plugins")
		err = ops[1].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 0),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, true)
		Expect(err).NotTo(HaveOccurred())
		st1, err = ops[1].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st1.GlobalVariables.SemiSyncSourceEnabled).To(BeFalse())
		Expect(st1.GlobalVariables.SemiSyncReplicaEnabled).To(BeTrue())
	})
})

func execWritable(op *operator, queries ...string) {
	GinkgoHelper()
	_, err := op.db.Exec(`SET GLOBAL super_read_only=OFF`)
	Expect(err).NotTo(HaveOccurred())
	for _, q := range queries {
		_, err := op.db.Exec(q)
		Expect(err).NotTo(HaveOccurred(), q)
	}
	_, err = op.db.Exec(`SET GLOBAL super_read_only=ON`)
	Expect(err).NotTo(HaveOccurred())
}
