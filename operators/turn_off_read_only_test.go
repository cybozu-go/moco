package operators

import (
	"context"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Turn off read only", func() {

	ctx := context.Background()

	BeforeEach(func() {
		err := moco.StartMySQLD(mysqldName1, mysqldPort1, mysqldServerID1)
		Expect(err).ShouldNot(HaveOccurred())

		err = moco.InitializeMySQL(mysqldPort1)
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		moco.StopAndRemoveMySQLD(mysqldName1)
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

		status := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.ReadOnly).Should(BeFalse())
	})
})
