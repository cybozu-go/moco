package dbop

import (
	"log"
	"os"
	"testing"

	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDBOp(t *testing.T) {
	if os.Getenv("TEST_MYSQL") != "1" {
		t.Skip("skip")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "DBOp Suite")
}

var factory = NewTestFactory()

var _ = BeforeSuite(func() {
	SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags)))
})

var _ = AfterSuite(func() {
	factory.Cleanup()
})
