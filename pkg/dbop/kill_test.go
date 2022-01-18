package dbop

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("kill", func() {
	It("should kill non-system processes only", func() {
		By("preparing a single node cluster")
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "kill"
		cluster.Spec.Replicas = 1

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		op, err := factory.New(context.Background(), cluster, passwd, 0)
		Expect(err).NotTo(HaveOccurred())

		By("creating a user and making a connection with the user")
		_, err = op.(*operator).db.Exec("SET GLOBAL read_only=0")
		Expect(err).NotTo(HaveOccurred())
		_, err = op.(*operator).db.Exec("CREATE USER 'foo'@'%' IDENTIFIED BY 'bar'")
		Expect(err).NotTo(HaveOccurred())
		db, err := factory.(*testFactory).newConn(context.Background(), cluster, "foo", "bar", 0)
		Expect(err).NotTo(HaveOccurred())
		defer db.Close()

		By("getting process list")
		var procs []Process
		err = op.(*operator).db.Select(&procs, `SELECT ID, USER, HOST FROM information_schema.PROCESSLIST`)
		Expect(err).NotTo(HaveOccurred())

		fooFound := false
		for _, p := range procs {
			fmt.Printf("process %d for %s from %s\n", p.ID, p.User, p.Host)
			if p.User == "foo" {
				fooFound = true
			}
		}
		Expect(fooFound).To(BeTrue())

		By("killing user process")
		err = op.KillConnections(context.Background())
		Expect(err).NotTo(HaveOccurred())

		var procs2 []Process
		err = op.(*operator).db.Select(&procs2, `SELECT ID, USER, HOST FROM information_schema.PROCESSLIST`)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(procs) - len(procs2)).To(Equal(1))

		fooFound = false
		for _, p := range procs2 {
			fmt.Printf("process %d for %s from %s\n", p.ID, p.User, p.Host)
			if p.User == "foo" {
				fooFound = true
			}
		}
		Expect(fooFound).To(BeFalse())
	})
})
