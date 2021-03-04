package accessor

import (
	"context"
	"strconv"

	agentmock "github.com/cybozu-go/moco/agentrpc/mock"
	"github.com/cybozu-go/moco/test_utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Agent Accessor", func() {
	It("Should use cache to connect to agent instance", func() {
		ctx := context.Background()
		defer ctx.Done()
		agentmock.Start(ctx)
		acc := NewAgentAccessor()

		addr := test_utils.Host + ":" + strconv.Itoa(test_utils.AgentPort)
		conn, err := acc.Get(addr)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.conns).Should(HaveLen(1))

		conn2, err := acc.Get(addr)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(conn).Should(Equal(conn2))

		acc.Remove(test_utils.Host)
		Expect(acc.conns).Should(HaveLen(0))
	})
})
