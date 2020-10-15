package operators

import (
	"context"
	"strconv"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var _ = Describe("Configure intermediate primary operator", func() {

	ctx := context.Background()

	BeforeEach(func() {
		err := createNetwork()
		Expect(err).ShouldNot(HaveOccurred())

		err = startMySQLD(primaryName, primaryPort, primaryServerID)
		Expect(err).ShouldNot(HaveOccurred())
		err = startMySQLD(replicaName, replicaPort, replicaServerID)
		Expect(err).ShouldNot(HaveOccurred())

		err = initializeMySQL(primaryPort)
		Expect(err).ShouldNot(HaveOccurred())
		err = initializeMySQL(replicaPort)
		Expect(err).ShouldNot(HaveOccurred())

		ns := corev1.Namespace{}
		ns.Name = namespace
		err = k8sClient.Create(ctx, &ns)
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		stopMySQLD(primaryName)
		stopMySQLD(replicaName)
		removeNetwork()

		ns := corev1.Namespace{}
		ns.Name = namespace
		k8sClient.Delete(ctx, &ns)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("", func() {
		_, infra, cluster := getAccessorInfraCluster()
		source := "replication-source"
		cluster.Spec.ReplicationSourceSecretName = &source

		secret := corev1.Secret{}
		secret.Namespace = namespace
		secret.Name = source
		_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &secret, func() error {
			secret.Data = map[string][]byte{
				"PRIMARY_HOST":     []byte(replicaName),
				"PRIMARY_PORT":     []byte(strconv.Itoa(replicaPort)),
				"PRIMARY_USER":     []byte("root"),
				"PRIMARY_PASSWORD": []byte(password),
			}
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		op := configureIntermediatePrimaryOp{
			Index: 0,
			Options: &accessor.IntermediatePrimaryOptions{
				PrimaryHost:     replicaName,
				PrimaryUser:     "root",
				PrimaryPassword: password,
				PrimaryPort:     replicaPort,
			},
		}

		err = op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(status.InstanceStatus).Should(HaveLen(1))
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.ReadOnly).Should(BeTrue())
		replicaStatus := status.InstanceStatus[0].ReplicaStatus
		Expect(replicaStatus).ShouldNot(BeNil())
		Expect(replicaStatus.MasterHost).Should(Equal(replicaName))
		Expect(replicaStatus.LastIoErrno).Should(Equal(0))
		Expect(replicaStatus.SlaveIORunning).Should(Equal(moco.ReplicaRunConnect))
		Expect(replicaStatus.SlaveSQLRunning).Should(Equal(moco.ReplicaRunConnect))
	})
})
