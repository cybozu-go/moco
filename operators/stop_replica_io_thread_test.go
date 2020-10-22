package operators

import (
	"context"
	"errors"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Stop replica IO thread", func() {

	ctx := context.Background()

	BeforeEach(func() {
		err := moco.StartMySQLD(mysqldName1, mysqldPort1, mysqldServerID1)
		Expect(err).ShouldNot(HaveOccurred())
		err = moco.StartMySQLD(mysqldName2, mysqldPort2, mysqldServerID2)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.InitializeMySQL(mysqldPort1)
		Expect(err).ShouldNot(HaveOccurred())
		err = moco.InitializeMySQL(mysqldPort2)
		Expect(err).ShouldNot(HaveOccurred())

	})

	AfterEach(func() {
		moco.StopAndRemoveMySQLD(mysqldName1)
		moco.StopAndRemoveMySQLD(mysqldName2)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should stop IO thread", func() {
		_, infra, cluster := getAccessorInfraCluster()
		db, err := infra.GetDB(0)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?`, mysqldName2, mysqldPort2, userName, password)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`START SLAVE`)
		Expect(err).ShouldNot(HaveOccurred())

		op := stopReplicaIOThread{
			Index: 0,
		}

		err = op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(status.InstanceStatus).Should(HaveLen(2))
		replicaStatus := status.InstanceStatus[0].ReplicaStatus
		Expect(replicaStatus).ShouldNot(BeNil())
		Expect(replicaStatus.MasterHost).Should(Equal(mysqldName2))
		Expect(replicaStatus.LastIoErrno).Should(Equal(0))

		Eventually(func() error {
			status = accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
			replicaStatus = status.InstanceStatus[0].ReplicaStatus
			if replicaStatus.SlaveIORunning != moco.ReplicaNotRun {
				return errors.New("IO thread should not be running")
			}
			if replicaStatus.SlaveSQLRunning != moco.ReplicaRunConnect {
				return errors.New("SQL thread should be running")
			}
			return nil
		}).Should(Succeed())
	})
})
