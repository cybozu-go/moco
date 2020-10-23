package operators

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Set clone donor list", func() {

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

	It("should configure replication", func() {
		_, infra, cluster := getAccessorInfraCluster()

		host := moco.GetHost(&cluster, *cluster.Status.CurrentPrimaryIndex)
		hostWithPort := fmt.Sprintf("%s:%d", host, moco.MySQLAdminPort)
		op := setCloneDonorListOp{index: []int{0, 1}, donar: hostWithPort}

		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status, err := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(status.InstanceStatus).Should(HaveLen(2))
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.CloneValidDonorList.Valid).Should(BeTrue())
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.CloneValidDonorList.String).Should(Equal(hostWithPort))
		Expect(status.InstanceStatus[1].GlobalVariablesStatus.CloneValidDonorList.Valid).Should(BeTrue())
		Expect(status.InstanceStatus[1].GlobalVariablesStatus.CloneValidDonorList.String).Should(Equal(hostWithPort))
	})
})
