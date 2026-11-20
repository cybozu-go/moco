package dbop

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("status", func() {

	ctx := context.Background()

	It("should retrieve status information", func() {
		By("preparing a 2 node cluster")
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "status"
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

		By("checking the initial status")
		status, err := ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(status).NotTo(BeNil())
		Expect(status.GlobalVariables.ExecutedGTID).To(BeEmpty())
		Expect(status.GlobalVariables.PurgedGTID).To(BeEmpty())
		Expect(status.GlobalVariables.ReadOnly).To(BeTrue())
		Expect(status.GlobalVariables.SuperReadOnly).To(BeTrue())
		Expect(status.GlobalVariables.WaitForReplicaCount).To(Equal(1))
		Expect(status.GlobalVariables.SemiSyncSourceEnabled).To(BeFalse())
		Expect(status.GlobalVariables.SemiSyncReplicaEnabled).To(BeFalse())
		Expect(status.GlobalStatus.SemiSyncSourceWaitSessions).To(Equal(0))

		By("writing data and checking gtid_executed")
		_, err = ops[0].db.Exec("SET GLOBAL read_only=0")
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec("CREATE DATABASE foo")
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec(`CREATE TABLE foo.t1 (pkey INT PRIMARY KEY, data TEXT NOT NULL) ENGINE=InnoDB`)
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec(`INSERT INTO foo.t1 (pkey, data) VALUES (1, "aaa"), (2, "bbb")`)
		Expect(err).NotTo(HaveOccurred())
		status, err = ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(status).NotTo(BeNil())
		Expect(status.GlobalVariables.ExecutedGTID).NotTo(BeEmpty())
		Expect(status.GlobalVariables.PurgedGTID).To(BeEmpty())
		Expect(status.GlobalVariables.ReadOnly).To(BeFalse())
		Expect(status.GlobalVariables.SuperReadOnly).To(BeFalse())
		Expect(status.GlobalVariables.WaitForReplicaCount).To(Equal(1))
		Expect(status.GlobalVariables.SemiSyncSourceEnabled).To(BeFalse())
		Expect(status.GlobalVariables.SemiSyncReplicaEnabled).To(BeFalse())
		Expect(status.GlobalStatus.SemiSyncSourceWaitSessions).To(Equal(0))

		By("enabling semi-sync master")
		err = ops[0].ConfigurePrimary(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		status, err = ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(status).NotTo(BeNil())
		Expect(status.GlobalVariables.WaitForReplicaCount).To(Equal(1))
		Expect(status.GlobalVariables.SemiSyncSourceEnabled).To(BeTrue())
		Expect(status.GlobalVariables.SemiSyncReplicaEnabled).To(BeFalse())
		Expect(status.GlobalStatus.SemiSyncSourceWaitSessions).To(Equal(0))

		By("enabling semi-sync replica")
		err = ops[1].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 0),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, true)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int {
			var count int
			err := ops[1].db.Get(&count, `SELECT COUNT(*) FROM foo.t1`)
			if err != nil {
				return 0
			}
			return count
		}).Should(Equal(2))

		By("checking status of 1")
		st0, err := ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		st1, err := ops[1].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st1.GlobalVariables.ExecutedGTID).To(Equal(st0.GlobalVariables.ExecutedGTID))
		Expect(st1.GlobalVariables.SuperReadOnly).To(BeTrue())
		Expect(st1.GlobalVariables.SemiSyncSourceEnabled).To(BeFalse())
		Expect(st1.GlobalVariables.SemiSyncReplicaEnabled).To(BeTrue())

		By("create hangup transaction")
		err = ops[1].StopReplicaIOThread(ctx)
		Expect(err).NotTo(HaveOccurred())
		commitTrx, cancelTrx := context.WithCancel(ctx)
		go func(ctx context.Context, op *operator) {
			trx, err := op.db.BeginTx(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			_, err = trx.ExecContext(ctx, `INSERT INTO foo.t1 (pkey, data) VALUES (3, "cccc"), (4, "zzz")`)
			Expect(err).NotTo(HaveOccurred())
			// Hangup
			// The error from trx.Commit() is intentionally ignored in this test scenario
			// because the focus is on simulating a hanging transaction and observing its effects.
			_ = trx.Commit()
		}(commitTrx, ops[0])

		By("checking SemiSyncMasterWaitSession of 0")
		st0, err = ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		st1, err = ops[1].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st0.GlobalVariables.ExecutedGTID).To(Equal(st1.GlobalVariables.ExecutedGTID))
		Eventually(func() int {
			st0, err = ops[0].GetStatus(ctx)
			if err != nil {
				return 0
			}
			return st0.GlobalStatus.SemiSyncSourceWaitSessions
		}).ShouldNot(Equal(0))
		cancelTrx()
	})
})
