package operators

import (
	"context"

	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/test_utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

func testTurnOffReadOnly() {
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
	})

	AfterEach(func() {
		test_utils.StopAndRemoveMySQLD(mysqldName1)
		test_utils.StopAndRemoveMySQLD(mysqldName2)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should turn off read only", func() {
		_, infra, cluster := getAccessorInfraCluster()

		db, err := infra.GetDB(0)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec("SET GLOBAL read_only=1")
		Expect(err).ShouldNot(HaveOccurred())

		op := turnOffReadOnlyOp{
			primaryIndex: 0,
		}

		err = op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status, err := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.ReadOnly).Should(BeFalse())
	})
}
