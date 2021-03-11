package accessor

import (
	"strconv"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/test_utils"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("MySQL Accessor", func() {
	It("Should use cache to connect to MySQL instance", func() {
		addr := test_utils.Host + ":" + strconv.Itoa(mysqldPort)

		acc := NewMySQLAccessor(&MySQLAccessorConfig{
			ConnMaxLifeTime:   30 * time.Minute,
			ConnectionTimeout: 3 * time.Second,
			ReadTimeout:       30 * time.Second,
		})

		_, err := acc.Get(addr, moco.AdminUser, "wrong password")
		Expect(err).Should(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(0))

		_, err = acc.Get(addr, moco.AdminUser, test_utils.OperatorAdminUserPassword)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(1))

		// Use cached connection
		_, err = acc.Get(addr, moco.AdminUser, test_utils.OperatorAdminUserPassword)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(1))

		acc.Remove(moco.AdminUser + ":" + test_utils.OperatorAdminUserPassword + "@tcp(" + addr + ")")
		Expect(acc.dbs).Should(HaveLen(0))

		_, err = acc.Get(addr, moco.AdminUser, test_utils.OperatorAdminUserPassword)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(acc.dbs).Should(HaveLen(1))

		acc.Remove(addr)
		Expect(acc.dbs).Should(HaveLen(0))
	})
})
