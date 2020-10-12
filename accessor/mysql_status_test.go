package accessor

import (
	"context"
	"time"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	namespace = "test-namespace"
)

var intermediateSecret = "intermediate-primary-secret"

var _ = Describe("Get MySQLCluster status", func() {
	It("should initialize MySQL for testing", func() {
		err := initializeMySQL()
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should get MySQL status", func() {
		_, inf, cluster := getAccessorInfraCluster()

		logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts := GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)

		Expect(sts.InstanceStatus).Should(HaveLen(1))
		Expect(sts.InstanceStatus[0].PrimaryStatus).ShouldNot(BeNil())
		Expect(sts.InstanceStatus[0].ReplicaStatus).ShouldNot(BeNil())
		Expect(sts.InstanceStatus[0].AllRelayLogExecuted).Should(BeTrue())
		Expect(sts.InstanceStatus[0].GlobalVariablesStatus).ShouldNot(BeNil())
		Expect(sts.InstanceStatus[0].CloneStateStatus).ShouldNot(BeNil())
		Expect(*sts.Latest).Should(Equal(0))
	})

	It("should get and validate intermediate primary options", func() {
		_, inf, cluster := getAccessorInfraCluster()
		cluster.Spec.ReplicationSourceSecretName = &intermediateSecret
		err := k8sClient.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		Expect(err).ShouldNot(HaveOccurred())

		By("setting valid options to api server")
		data := map[string][]byte{
			"PRIMARY_HOST":     []byte("dummy-primary"),
			"PRIMARY_PORT":     []byte("3306"),
			"PRIMARY_USER":     []byte("dummy-user"),
			"PRIMARY_PASSWORD": []byte("dummy-password"),
		}
		var ipSecret corev1.Secret
		ipSecret.ObjectMeta.Name = intermediateSecret
		ipSecret.ObjectMeta.Namespace = namespace
		ipSecret.Data = data
		err = k8sClient.Create(context.Background(), &ipSecret)
		Expect(err).ShouldNot(HaveOccurred())

		By("getting and validating intermediate primary options")
		logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts := GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)
		expect := &IntermediatePrimaryOptions{
			PrimaryHost:     "dummy-primary",
			PrimaryPassword: "dummy-password",
			PrimaryPort:     3306,
			PrimaryUser:     "dummy-user",
		}
		Expect(sts.IntermediatePrimaryOptions).Should(Equal(expect))

		By("setting options without PRIMARY_HOST to api server")
		data = map[string][]byte{
			"PRIMARY_PORT": []byte("3306"),
		}
		ipSecret.ObjectMeta.Name = intermediateSecret
		ipSecret.ObjectMeta.Namespace = namespace
		ipSecret.Data = data
		err = k8sClient.Update(context.Background(), &ipSecret)
		Expect(err).ShouldNot(HaveOccurred())

		By("getting and validating intermediate primary options")
		logger = ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts = GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)
		Expect(sts.IntermediatePrimaryOptions).Should(BeNil())

		By("setting options without INVALID_OPTION to api server")
		data = map[string][]byte{
			"PRIMARY_HOST":   []byte("dummy-primary"),
			"PRIMARY_PORT":   []byte("3306"),
			"INVALID_OPTION": []byte("invalid"),
		}
		ipSecret.ObjectMeta.Name = intermediateSecret
		ipSecret.ObjectMeta.Namespace = namespace
		ipSecret.Data = data
		err = k8sClient.Update(context.Background(), &ipSecret)
		Expect(err).ShouldNot(HaveOccurred())

		By("getting and validating intermediate primary options")
		logger = ctrl.Log.WithName("controllers").WithName("MySQLCluster")
		sts = GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)
		Expect(sts.IntermediatePrimaryOptions).Should(BeNil())
	})
})

func getAccessorInfraCluster() (*MySQLAccessor, Infrastructure, mocov1alpha1.MySQLCluster) {
	acc := NewMySQLAccessor(&MySQLAccessorConfig{
		ConnMaxLifeTime:   30 * time.Minute,
		ConnectionTimeout: 3 * time.Second,
		ReadTimeout:       30 * time.Second,
	})
	inf := NewInfrastructure(k8sClient, acc, password, []string{host}, 3306)
	cluster := mocov1alpha1.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			ClusterName: "test-cluster",
			Namespace:   namespace,
			UID:         "test-uid",
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 1,
		},
	}

	return acc, inf, cluster
}
