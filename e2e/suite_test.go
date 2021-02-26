package e2e

import (
	"math/rand"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	lineCount = 10000
)

var (
	skipE2e     = os.Getenv("E2ETEST") == ""
	doUpgrade   = os.Getenv("UPGRADE") != ""
	doBootstrap = os.Getenv("BOOTSTRAP") != ""
)

func TestE2E(t *testing.T) {
	if skipE2e {
		t.Skip("Run under e2e/")
	}

	RegisterFailHandler(Fail)

	SetDefaultEventuallyPollingInterval(time.Second)
	SetDefaultEventuallyTimeout(20 * time.Second)

	rand.Seed(time.Now().UnixNano())

	RunSpecs(t, "kind test")
}

var _ = Describe("MOCO", func() {
	Context("prepare bootstrap", prepareBooptstrap)
	Context("bootstrap", testBootstrap)
	if doBootstrap {
		return
	}

	Context("agent", testAgent)
	Context("controller", testController)
	Context("replicaFailover", testReplicaFailOver)
	Context("primaryFailover", testPrimaryFailOver)
	Context("intermediatePrimary", testIntermediatePrimary)
	Context("kubectl-moco", testKubectlMoco)
	Context("garbageCollector", testGarbageCollector)
})
