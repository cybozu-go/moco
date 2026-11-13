package e2e

import (
	_ "embed"
	"slices"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func newTextParser() *expfmt.TextParser {
	p := expfmt.NewTextParser(model.LegacyValidation)
	return &p
}

// Long-running Ordered contexts are annotated with SpecPriority so that the
// scheduler starts them first; otherwise --randomize-all can place them at
// the tail and extend the suite. 100 = the longest chains (partition,
// PreventDelete), 90 = the next tier (stop).
func TestE2e(t *testing.T) {
	if !runE2E {
		t.Skip("no RUN_E2E environment variable")
	}
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
	RunSpecs(t, "E2e Suite")
}

// Spec priorities reduce the wall-clock time of the parallel suite.
// Higher priority specs are scheduled first when running with --randomize-all.
const (
	priorityReplicationDedicated = 60
	priorityReplicationShared    = 50
)

//go:embed testdata/client.yaml
var clientYAML string

var _ = SynchronizedBeforeSuite(func() {
	kubectlSafe(fillTemplate(clientYAML), "apply", "-f", "-")
}, func() {})

// Shared replication clusters are not needed by specs without the replication label.
// Delete them before such a spec runs to free the memory they use.
var _ = BeforeEach(func() {
	if slices.Contains(CurrentSpecReport().Labels(), replicationLabel) {
		return
	}
	cleanupSharedClusters()
})

var _ = AfterSuite(func() {
	cleanupSharedClusters()
})
