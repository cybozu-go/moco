package accessor

import (
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("MySQL Accessor", func() {
	It("Should use cache to connect to MySQL instance", func() {
		addr := host + ":" + strconv.Itoa(port)

		acc := NewMySQLAccessor(&MySQLAccessorConfig{
			ConnMaxLifeTime:   30 * time.Minute,
			ConnectionTimeout: 3 * time.Second,
			ReadTimeout:       30 * time.Second,
		})
		_, err := acc.Get(addr, moco.OperatorAdminUser, password)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(1))

		_, err = acc.Get(addr, moco.OperatorUser, "wrong password")
		Expect(err).Should(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(1))

		_, err = acc.Get(addr, moco.OperatorAdminUser, password)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(1))

		_, err = acc.Get(addr, moco.OperatorUser, password)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(2))

		acc.Remove(moco.OperatorAdminUser + ":" + password + "@tcp(" + addr + ")")
		Expect(acc.dbs).Should(HaveLen(1))

		_, err = acc.Get(addr, moco.OperatorAdminUser, password)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(2))

		acc.Remove(addr)
		Expect(acc.dbs).Should(HaveLen(0))
	})
})
