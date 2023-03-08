package dbop

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type process struct {
	ID    uint64 `db:"ID"`
	User  string `db:"USER"`
	Host  string `db:"HOST"`
	State string `db:"STATE"` // for debugging
}

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
		var procs []process
		err = op.(*operator).db.Select(&procs, `SELECT ID, USER, HOST, STATE FROM information_schema.PROCESSLIST`)
		Expect(err).NotTo(HaveOccurred())

		for _, p := range procs {
			fmt.Printf("process %d for %s from %s: %s\n", p.ID, p.User, p.Host, p.State)
		}
		Expect(procs).To(ContainElement(HaveField("User", "foo")))

		By("killing user process")
		err = op.KillConnections(context.Background())
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			var procs2 []process
			err := op.(*operator).db.Select(&procs2, `SELECT ID, USER, HOST, STATE FROM information_schema.PROCESSLIST`)
			g.Expect(err).NotTo(HaveOccurred())

			// For debugging, print process list before confirming.
			for _, p := range procs2 {
				fmt.Printf("process %d for %s from %s: %s\n", p.ID, p.User, p.Host, p.State)
			}
			fmt.Println("")

			g.Expect(procs2).To(HaveLen(len(procs) - 1))
			g.Expect(procs2).NotTo(ContainElement(HaveField("User", "foo")))
		}).Should(Succeed())
	})
})
