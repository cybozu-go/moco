package operators

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cybozu-go/moco"
	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Clone operator", func() {

	ctx := context.Background()
	_, infra, cluster := getAccessorInfraCluster()
	replicaIndex := 1
	replicaHost := moco.GetHost(&cluster, replicaIndex)
	primaryHost := moco.GetHost(&cluster, *cluster.Status.CurrentPrimaryIndex)
	uri := fmt.Sprintf("http://%s:%d/clone", replicaHost, moco.AgentPort)

	It("should call /clone API", func() {
		httpmock.Activate()
		defer httpmock.DeactivateAndReset()

		httpmock.RegisterResponderWithQuery(http.MethodGet, uri, map[string]string{
			moco.CloneParamDonorHostName: primaryHost,
			moco.CloneParamDonorPort:     strconv.Itoa(moco.MySQLAdminPort),
			moco.AgentTokenParam:         token,
		}, httpmock.NewStringResponder(http.StatusOK, ""))

		op := cloneOp{replicaIndex: replicaIndex}
		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		Expect(httpmock.GetTotalCallCount()).Should(Equal(1))
	})

	It("should be error when it receives error responce", func() {
		httpmock.Activate()
		defer httpmock.DeactivateAndReset()

		httpmock.RegisterResponderWithQuery(http.MethodGet, uri, map[string]string{
			moco.CloneParamDonorHostName: primaryHost,
			moco.CloneParamDonorPort:     strconv.Itoa(moco.MySQLAdminPort),
			moco.AgentTokenParam:         token,
		}, httpmock.NewStringResponder(http.StatusTooManyRequests, ""))

		op := cloneOp{replicaIndex: replicaIndex}
		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).Should(HaveOccurred())

		Expect(httpmock.GetTotalCallCount()).Should(Equal(1))
	})
})
