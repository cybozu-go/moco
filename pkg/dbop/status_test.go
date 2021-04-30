package dbop

import (
	"context"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("status", func() {
	It("should retrieve status information", func() {
		By("preparing a single node cluster")
		cluster := &mocov1beta1.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "status"
		cluster.Spec.Replicas = 1

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		op, err := factory.New(context.Background(), cluster, passwd, 0)
		Expect(err).NotTo(HaveOccurred())

		By("checking the initial stauts")
		status, err := op.GetStatus(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(status).NotTo(BeNil())
		Expect(status.GlobalVariables.ExecutedGTID).To(BeEmpty())
		Expect(status.GlobalVariables.ReadOnly).To(BeTrue())
		Expect(status.GlobalVariables.SuperReadOnly).To(BeTrue())
		Expect(status.GlobalVariables.WaitForSlaveCount).To(Equal(1))
		Expect(status.GlobalVariables.SemiSyncMasterEnabled).To(BeFalse())
		Expect(status.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())

		By("writing data and checking gtid_executed")
		_, err = op.(*operator).db.Exec("SET GLOBAL read_only=0")
		Expect(err).NotTo(HaveOccurred())
		_, err = op.(*operator).db.Exec("CREATE DATABASE foo")
		Expect(err).NotTo(HaveOccurred())
		status, err = op.GetStatus(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(status).NotTo(BeNil())
		Expect(status.GlobalVariables.ExecutedGTID).NotTo(BeEmpty())
		Expect(status.GlobalVariables.ReadOnly).To(BeFalse())
		Expect(status.GlobalVariables.SuperReadOnly).To(BeFalse())
		Expect(status.GlobalVariables.WaitForSlaveCount).To(Equal(1))
		Expect(status.GlobalVariables.SemiSyncMasterEnabled).To(BeFalse())
		Expect(status.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())

		By("enabling semi-sync master")
		err = op.ConfigurePrimary(context.Background(), 3)
		Expect(err).NotTo(HaveOccurred())
		status, err = op.GetStatus(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(status).NotTo(BeNil())
		Expect(status.GlobalVariables.WaitForSlaveCount).To(Equal(3))
		Expect(status.GlobalVariables.SemiSyncMasterEnabled).To(BeTrue())
		Expect(status.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())

		err = op.Close()
		Expect(err).NotTo(HaveOccurred())
	})
})
