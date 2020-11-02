package e2e

import (
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	if os.Getenv("E2ETEST") == "" {
		t.Skip("Run under e2e/")
	}

	RegisterFailHandler(Fail)

	SetDefaultEventuallyPollingInterval(time.Second)
	SetDefaultEventuallyTimeout(20 * time.Second)

	RunSpecs(t, "kind test")
}

var _ = Describe("MOCO", func() {
	Context("bootstrap", testBootstrap)
	Context("agent", testAgent)
	Context("controller", testController)
	Context("replicaFailover", testReplicaFailOver)
	Context("primaryFailover", testPrimaryFailOver)
	Context("intermediatePrimary", testIntermediatePrimary)
	Context("kubectl-moco", testKubectlMoco)
	Context("garbageCollector", testGarbageCollector)
})
