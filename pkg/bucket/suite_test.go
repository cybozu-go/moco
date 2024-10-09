package bucket

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBucket(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bucket Suite")
}
