package operators

import (
	"context"
	"os"

	corev1 "k8s.io/api/core/v1"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Configure replication", func() {

	ctx := context.Background()

	BeforeEach(func() {
		err := createNetwork()
		Expect(err).ShouldNot(HaveOccurred())

		err = startMySQLD(mysqldName1, mysqldPort1, mysqldServerID1)
		Expect(err).ShouldNot(HaveOccurred())
		err = startMySQLD(mysqldName2, mysqldPort2, mysqldServerID2)
		Expect(err).ShouldNot(HaveOccurred())

		err = initializeMySQL(mysqldPort1)
		Expect(err).ShouldNot(HaveOccurred())
		err = initializeMySQL(mysqldPort2)
		Expect(err).ShouldNot(HaveOccurred())

		ns := corev1.Namespace{}
		ns.Name = systemNamespace
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		secret := corev1.Secret{}
		secret.Namespace = systemNamespace
		secret.Name = namespace + ".test"
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &secret, func() error {
			secret.Data = map[string][]byte{
				moco.ReplicationPasswordKey: []byte(password),
			}
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		os.Setenv("POD_NAMESPACE", systemNamespace)
	})

	AfterEach(func() {
		stopMySQLD(mysqldName1)
		stopMySQLD(mysqldName2)
		removeNetwork()
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should configure replication", func() {
		_, infra, cluster := getAccessorInfraCluster()

		op := configureReplicationOp{
			Index:          0,
			PrimaryHost:    mysqldName2,
			PrimaryPort:    mysqldPort2,
			ReplicatorUser: userName,
		}

		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(status.InstanceStatus).Should(HaveLen(2))
		replicaStatus := status.InstanceStatus[0].ReplicaStatus
		Expect(replicaStatus).ShouldNot(BeNil())
		Expect(replicaStatus.MasterHost).Should(Equal(mysqldName2))
		Expect(replicaStatus.LastIoErrno).Should(Equal(0))
		Expect(replicaStatus.SlaveIORunning).Should(Equal(moco.ReplicaRunConnect))
		Expect(replicaStatus.SlaveSQLRunning).Should(Equal(moco.ReplicaRunConnect))
	})
})
