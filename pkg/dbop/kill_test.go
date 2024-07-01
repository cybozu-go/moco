package dbop

import (
	"context"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("kill", func() {
	ctx := context.Background()

	It("should kill non-system processes only", func() {
		By("preparing 3 node cluster")
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "kill"
		cluster.Spec.Replicas = 3

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		ops := make([]*operator, cluster.Spec.Replicas)
		for i := 0; i < int(cluster.Spec.Replicas); i++ {
			op, err := factory.New(context.Background(), cluster, passwd, i)
			Expect(err).NotTo(HaveOccurred())
			ops[i] = op.(*operator)
		}
		defer func() {
			for _, op := range ops {
				op.Close()
			}
		}()

		By("configuring replication between 0 and 1")
		err = ops[1].ConfigureReplica(ctx, AccessInfo{
			Host:     testContainerName(cluster, 0),
			Port:     3306,
			User:     constants.ReplicationUser,
			Password: passwd.Replicator(),
		}, false)
		Expect(err).NotTo(HaveOccurred())

		By("creating a user and making a connection with the user")
		_, err = ops[0].db.Exec("SET GLOBAL read_only=0")
		Expect(err).NotTo(HaveOccurred())
		_, err = ops[0].db.Exec("CREATE USER 'foo'@'%' IDENTIFIED BY 'bar'")
		Expect(err).NotTo(HaveOccurred())
		db, err := factory.(*testFactory).newConn(context.Background(), cluster, "foo", "bar", 0)
		Expect(err).NotTo(HaveOccurred())
		defer db.Close()

		By("getting process list in primary")
		var procs []Process
		err = ops[0].db.Select(&procs, `SELECT ID, USER, HOST FROM information_schema.PROCESSLIST`)
		Expect(err).NotTo(HaveOccurred())

		fooFound := false
		for _, p := range procs {
			fmt.Printf("process %d for %s from %s\n", p.ID, p.User, p.Host)
			if p.User == "foo" {
				fooFound = true
			}
		}
		Expect(fooFound).To(BeTrue())

		By("killing user process in primary")
		err = ops[0].KillConnections(context.Background())
		Expect(err).NotTo(HaveOccurred())

		var procs2 []Process
		err = ops[0].db.Select(&procs2, `SELECT ID, USER, HOST FROM information_schema.PROCESSLIST`)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int {
			return len(procs) - len(procs2)
		}).Should(BeNumerically("==", 1))

		fooFound = false
		for _, p := range procs2 {
			fmt.Printf("process %d for %s from %s\n", p.ID, p.User, p.Host)
			if p.User == "foo" {
				fooFound = true
			}
		}
		Expect(fooFound).To(BeFalse())

		By("getting process list in replica")
		var procs3 []Process
		err = ops[1].db.Select(&procs3, `SELECT ID, USER, HOST FROM information_schema.PROCESSLIST`)
		Expect(err).NotTo(HaveOccurred())

		By("system user is not killed")
		err = ops[1].KillConnections(context.Background())
		Expect(err).NotTo(HaveOccurred())

		var procs4 []Process
		err = ops[1].db.Select(&procs4, `SELECT ID, USER, HOST FROM information_schema.PROCESSLIST`)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int {
			return len(procs3) - len(procs4)
		}).Should(BeNumerically("==", 0))
	})
})
