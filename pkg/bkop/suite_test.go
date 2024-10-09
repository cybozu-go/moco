package bkop

import (
	"log"
	"os"
	"testing"

	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBKOp(t *testing.T) {
	if os.Getenv("TEST_MYSQL") != "1" {
		t.Skip("skip")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "BKOp Suite")
}

var _ = BeforeSuite(func() {
	dbop.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags)))
})
