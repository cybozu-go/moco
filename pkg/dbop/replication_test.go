package dbop

import (
	"context"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// In this test, the instance zero represents an external mysqld,
// the instance one represents the initial primary instance, and
// the instance two represents the initial replica respectively.
//
// At first, all replication is asynchronous.
//
// Later, the instance one and two exchange their roles, then the
// instance two will become non-intermediate primary.  At this
// point, the replication between instance one and two becomes
// semi-synchronous.
var _ = Describe("replication", func() {
	ctx := context.Background()

	It("should configure replication", func() {
		By("preparing 3 node cluster")
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "cluster"
		cluster.Spec.Replicas = 3

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		ops := make([]*operator, cluster.Spec.Replicas)
		for i := 0; i < int(cluster.Spec.Replicas); i++ {
			op, err := factory.New(context.Background(), cluster, passwd, i)
			Expect(err).NotTo(HaveOccurred())
			ops[i] = op.(*operator)
		}
		defer func() {
			for _, op := range ops {
				op.Close()
			}
		}()

		By("initializing an external instance 0")
		err = ops[0].SetReadOnly(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec(`CREATE DATABASE foo`)
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec(`CREATE TABLE foo.t1 (pkey INT PRIMARY KEY, data TEXT NOT NULL) ENGINE=InnoDB`)
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec(`INSERT INTO foo.t1 (pkey, data) VALUES (1, "aaa"), (2, "bbb")`)
		Expect(err).NotTo(HaveOccurred())

		By("configuring replication between 0 and 1")
		err = ops[1].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 0),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, false)
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
		Expect(st1.GlobalVariables.SemiSyncMasterEnabled).To(BeFalse())
		Expect(st1.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())

		By("checking WaitForGTID works")
		err = ops[1].StopReplicaIOThread(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec(`INSERT INTO foo.t1 (pkey, data) VALUES (3, "cccc"), (4, "zzz")`)
		Expect(err).NotTo(HaveOccurred())
		st0, err = ops[0].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		err = ops[1].WaitForGTID(ctx, st0.GlobalVariables.ExecutedGTID, 1)
		Expect(err).To(MatchError(ErrTimeout))
		_, err = ops[1].db.Exec(`START REPLICA`)
		Expect(err).NotTo(HaveOccurred())
		err = ops[1].WaitForGTID(ctx, st0.GlobalVariables.ExecutedGTID, 0)
		Expect(err).NotTo(HaveOccurred())
		var count int
		err = ops[1].db.Get(&count, `SELECT COUNT(*) FROM foo.t1`)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(4))

		By("configuring asynchronous replication between 1 and 2")
		err = ops[2].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 1),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, false)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int {
			var count int
			err := ops[2].db.Get(&count, `SELECT COUNT(*) FROM foo.t1`)
			if err != nil {
				return 0
			}
			return count
		}).Should(Equal(4))
		_, err = ops[0].db.Exec(`INSERT INTO foo.t1 (pkey, data) VALUES (5, "gogogo")`)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int {
			var count int
			err := ops[2].db.Get(&count, `SELECT COUNT(*) FROM foo.t1`)
			if err != nil {
				return 0
			}
			return count
		}).Should(Equal(5))

		By("checking status of 1 and 2")
		st1, err = ops[1].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		st2, err := ops[2].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st2.GlobalVariables.ExecutedGTID).To(Equal(st1.GlobalVariables.ExecutedGTID))
		Expect(st1.GlobalVariables.SemiSyncMasterEnabled).To(BeFalse())
		Expect(st1.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())
		Expect(st2.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())

		By("promoting 2 as the new intermediate primary")
		err = ops[2].StopReplicaIOThread(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec(`INSERT INTO foo.t1 (pkey, data) VALUES (6, "six")`)
		Eventually(func() int {
			var count int
			err := ops[1].db.Get(&count, `SELECT COUNT(*) FROM foo.t1`)
			if err != nil {
				return 0
			}
			return count
		}).Should(Equal(6))
		Expect(err).NotTo(HaveOccurred())
		err = ops[1].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 2),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, false)
		Expect(err).NotTo(HaveOccurred())

		err = ops[2].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 0),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, false)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int {
			var count int
			err := ops[2].db.Get(&count, `SELECT COUNT(*) FROM foo.t1`)
			if err != nil {
				return 0
			}
			return count
		}).Should(Equal(6))

		By("changing 2 from intermediate to normal")
		err = ops[2].StopReplicaIOThread(ctx)
		Expect(err).NotTo(HaveOccurred())
		// wait for the retrieved gtid to be executed
		st2, err = ops[2].GetStatus(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(st2.ReplicaStatus.RetrievedGtidSet).NotTo(BeEmpty())
		err = ops[2].WaitForGTID(ctx, st2.ReplicaStatus.RetrievedGtidSet, 10)
		Expect(err).NotTo(HaveOccurred())
		err = ops[2].ConfigurePrimary(ctx, true, 1)
		Expect(err).NotTo(HaveOccurred())
		err = ops[2].SetReadOnly(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		ctx2, cancel := context.WithTimeout(ctx, 1*time.Second)
		defer cancel()
		_, err = ops[2].db.ExecContext(ctx2, `INSERT INTO foo.t1 (pkey, data) VALUES (100, "hundred")`)
		Expect(err).To(MatchError(context.DeadlineExceeded))

		err = ops[1].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 2),
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
		}).Should(Equal(7))
	})
})
