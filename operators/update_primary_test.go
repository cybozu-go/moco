package operators

import (
	"context"
	"strconv"
	"time"

	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/cybozu-go/moco/test_utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Update primary", func() {

	ctx := context.Background()

	BeforeEach(func() {
		err := test_utils.StartMySQLD(mysqldName1, mysqldPort1, mysqldServerID1)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.StartMySQLD(mysqldName2, mysqldPort2, mysqldServerID2)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.StartMySQLD(mysqldName3, mysqldPort3, mysqldServerID3)
		Expect(err).ShouldNot(HaveOccurred())

		err = test_utils.InitializeMySQL(mysqldPort1)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.InitializeMySQL(mysqldPort2)
		Expect(err).ShouldNot(HaveOccurred())
		err = test_utils.InitializeMySQL(mysqldPort3)
		Expect(err).ShouldNot(HaveOccurred())

		ns := corev1.Namespace{}
		ns.Name = namespace
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		test_utils.StopAndRemoveMySQLD(mysqldName1)
		test_utils.StopAndRemoveMySQLD(mysqldName2)
		test_utils.StopAndRemoveMySQLD(mysqldName3)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should update primary", func() {
		_, _, cluster := getAccessorInfraCluster()
		cluster.Spec.Replicas = 3
		acc := accessor.NewMySQLAccessor(&accessor.MySQLAccessorConfig{
			ConnMaxLifeTime:   30 * time.Minute,
			ConnectionTimeout: 3 * time.Second,
			ReadTimeout:       30 * time.Second,
		})
		infra := accessor.NewInfrastructure(k8sClient, acc, password, []string{host + ":" + strconv.Itoa(mysqldPort1), host + ":" + strconv.Itoa(mysqldPort2), host + ":" + strconv.Itoa(mysqldPort3)})

		_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &cluster, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		db, err := infra.GetDB(0)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?`, mysqldName2, mysqldPort2, userName, password)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`START SLAVE`)
		Expect(err).ShouldNot(HaveOccurred())

		op := updatePrimaryOp{
			newPrimaryIndex: 1,
		}

		status, err := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(err).ShouldNot(HaveOccurred())

		err = op.Run(ctx, infra, &cluster, status)
		Expect(err).ShouldNot(HaveOccurred())

		updateCluster := v1alpha1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Name}, &updateCluster)
		Expect(err).ShouldNot(HaveOccurred())

		Expect(updateCluster.Status.CurrentPrimaryIndex).ShouldNot(BeNil())
		Expect(*updateCluster.Status.CurrentPrimaryIndex).Should(Equal(1))

		Expect(*cluster.Status.CurrentPrimaryIndex).Should(Equal(1))
		status, err = accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(status.InstanceStatus).Should(HaveLen(3))

		primaryStatus := status.InstanceStatus[1]
		Expect(primaryStatus.ReplicaStatus).Should(BeNil())
		Expect(primaryStatus.GlobalVariablesStatus.RplSemiSyncMasterWaitForSlaveCount).Should(Equal(1))
	})
})
