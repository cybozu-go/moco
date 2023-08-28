package e2e

import (
	_ "embed"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestE2e(t *testing.T) {
	if !runE2E {
		t.Skip("no RUN_E2E environment variable")
	}
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(10 * time.Minute)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
	RunSpecs(t, "E2e Suite")
}

//go:embed testdata/client.yaml
var clientYAML string

var _ = BeforeSuite(func() {
	kubectlSafe(fillTemplate(clientYAML), "apply", "-f", "-")
})
