package operators

import (
	"context"
	"errors"
	"strconv"

	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/test_utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var replicationSource = "replication-source"

func testConfigureIntermediatePrimary() {

	ctx := context.Background()

	BeforeEach(func() {
		err := test_utils.StartMySQLD(mysqldName1, mysqldPort1, mysqldServerID1)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.StartMySQLD(mysqldName2, mysqldPort2, mysqldServerID2)
		Expect(err).ShouldNot(HaveOccurred())

		err = test_utils.InitializeMySQL(mysqldPort1)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.InitializeMySQL(mysqldPort2)
		Expect(err).ShouldNot(HaveOccurred())

		ns := corev1.Namespace{}
		ns.Name = namespace
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		secret := corev1.Secret{}
		secret.Namespace = namespace
		secret.Name = replicationSource
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &secret, func() error {
			secret.Data = map[string][]byte{
				"PRIMARY_HOST":     []byte(mysqldName2),
				"PRIMARY_PORT":     []byte(strconv.Itoa(mysqldPort2)),
				"PRIMARY_USER":     []byte(test_utils.RootUser),
				"PRIMARY_PASSWORD": []byte(test_utils.RootUserPassword),
			}
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		test_utils.StopAndRemoveMySQLD(mysqldName1)
		test_utils.StopAndRemoveMySQLD(mysqldName2)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should be intermediate primary", func() {
		_, infra, cluster := getAccessorInfraCluster()
		cluster.Spec.ReplicationSourceSecretName = &replicationSource

		op := configureIntermediatePrimaryOp{
			Index: 0,
			Options: &accessor.IntermediatePrimaryOptions{
				PrimaryHost:     mysqldName2,
				PrimaryUser:     test_utils.RootUser,
				PrimaryPassword: test_utils.RootUserPassword,
				PrimaryPort:     mysqldPort2,
			},
		}

		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status, err := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(status.InstanceStatus).Should(HaveLen(2))
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.ReadOnly).Should(BeTrue())
		replicaStatus := status.InstanceStatus[0].ReplicaStatus
		Expect(replicaStatus).ShouldNot(BeNil())
		Expect(replicaStatus.MasterHost).Should(Equal(mysqldName2))
		Expect(replicaStatus.LastIoErrno).Should(Equal(0))

		Eventually(func() error {
			status, err = accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
			if err != nil {
				return err
			}
			replicaStatus = status.InstanceStatus[0].ReplicaStatus
			if replicaStatus.SlaveIORunning != "Yes" {
				return errors.New("IO thread should be running")
			}
			if replicaStatus.SlaveSQLRunning != "Yes" {
				return errors.New("SQL thread should be running")
			}
			return nil
		}).Should(Succeed())
	})

	It("should stop slave when replicationSecretName is empty", func() {
		_, infra, cluster := getAccessorInfraCluster()

		db, err := infra.GetDB(0)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?`, mysqldName2, mysqldPort2, test_utils.RootUser, test_utils.RootUserPassword)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`START SLAVE`)
		Expect(err).ShouldNot(HaveOccurred())

		op := configureIntermediatePrimaryOp{
			Index:   0,
			Options: nil,
		}

		err = op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status, err := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(status.InstanceStatus).Should(HaveLen(2))
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.ReadOnly).Should(BeFalse())

		replicaStatus := status.InstanceStatus[0].ReplicaStatus
		Expect(replicaStatus.LastIoErrno).Should(Equal(0))

		Eventually(func() error {
			status, err = accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
			if err != nil {
				return err
			}
			replicaStatus = status.InstanceStatus[0].ReplicaStatus
			if replicaStatus.SlaveIORunning != "No" {
				return errors.New("IO thread should not be running")
			}
			if replicaStatus.SlaveSQLRunning != "No" {
				return errors.New("SQL thread should not be running")
			}
			return nil
		}).Should(Succeed())
	})
}
