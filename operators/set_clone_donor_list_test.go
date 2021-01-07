package operators

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/test_utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Set clone donor list", func() {

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
	})

	AfterEach(func() {
		test_utils.StopAndRemoveMySQLD(mysqldName1)
		test_utils.StopAndRemoveMySQLD(mysqldName2)
		test_utils.StopAndRemoveMySQLD(mysqldName3)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should configure replication", func() {
		_, _, cluster := getAccessorInfraCluster()
		cluster.Spec.Replicas = 3
		acc := accessor.NewMySQLAccessor(&accessor.MySQLAccessorConfig{
			ConnMaxLifeTime:   30 * time.Minute,
			ConnectionTimeout: 3 * time.Second,
			ReadTimeout:       30 * time.Second,
		})
		infra := accessor.NewInfrastructure(k8sClient, acc, test_utils.Password, []string{test_utils.Host + ":" + strconv.Itoa(mysqldPort1), test_utils.Host + ":" + strconv.Itoa(mysqldPort2), test_utils.Host + ":" + strconv.Itoa(mysqldPort3)})

		host := moco.GetHost(&cluster, *cluster.Status.CurrentPrimaryIndex)
		hostWithPort := fmt.Sprintf("%s:%d", host, moco.MySQLAdminPort)
		op := setCloneDonorListOp{index: []int{0, 2}, donar: hostWithPort}

		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status, err := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(status.InstanceStatus).Should(HaveLen(3))
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.CloneValidDonorList.Valid).Should(BeTrue())
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.CloneValidDonorList.String).Should(Equal(hostWithPort))

		Expect(status.InstanceStatus[2].GlobalVariablesStatus.CloneValidDonorList.Valid).Should(BeTrue())
		Expect(status.InstanceStatus[2].GlobalVariablesStatus.CloneValidDonorList.String).Should(Equal(hostWithPort))

		Expect(status.InstanceStatus[1].GlobalVariablesStatus.CloneValidDonorList.Valid).Should(BeFalse())
	})
})
