package operators

import (
	"context"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/agentrpc"
	agentmock "github.com/cybozu-go/moco/agentrpc/mock"
	"github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func testClone() {
	var ctx context.Context
	var infra accessor.Infrastructure
	var cluster v1alpha1.MySQLCluster
	var replicaIndex int
	var primaryHost string

	BeforeEach(func() {
		ctx = context.Background()
		_, infra, cluster = getAccessorInfraCluster()
		replicaIndex = 0
		primaryHost = moco.GetHost(&cluster, *cluster.Status.CurrentPrimaryIndex)
	})

	It("should call clone API", func() {
		op := cloneOp{replicaIndex: replicaIndex}
		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		expect := &agentrpc.CloneRequest{
			Token:     token,
			DonorHost: primaryHost,
			DonorPort: moco.MySQLAdminPort,
		}
		Expect(agentmock.CompareWithLastCloneRequest(expect)).Should(Equal(""))
	})

	It("should call clone API with fromExternal flag", func() {
		op := cloneOp{replicaIndex: replicaIndex, fromExternal: true}
		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		expect := &agentrpc.CloneRequest{
			Token:    token,
			External: true,
		}
		Expect(agentmock.CompareWithLastCloneRequest(expect)).Should(Equal(""))
	})

	It("should be error when it receives error responce", func() {
		agentmock.M.ReturnErr = true
		op := cloneOp{replicaIndex: replicaIndex}
		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).Should(HaveOccurred())
	})
}
