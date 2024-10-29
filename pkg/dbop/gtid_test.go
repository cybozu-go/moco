package dbop

import (
	"context"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/password"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FindTopRunner", func() {
	It("should find the top runner instance correctly", func() {
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "find-top-runner"
		cluster.Spec.Replicas = 1

		passwd, err := password.NewMySQLPassword()
		Expect(err).NotTo(HaveOccurred())

		op, err := factory.New(context.Background(), cluster, passwd, 0)
		Expect(err).NotTo(HaveOccurred())
		defer op.Close()

		statuses := make([]*MySQLInstanceStatus, 3)
		_, err = FindTopRunner(context.Background(), op, statuses)
		Expect(err).To(MatchError(ErrNoTopRunner))

		set0 := `8e349184-bc14-11e3-8d4c-0800272864ba:1-29`
		set1 := `8e349184-bc14-11e3-8d4c-0800272864ba:1-30`
		set2 := `8e349184-bc14-11e3-8d4c-0800272864ba:1-31`
		statuses[0] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set0}}
		statuses[1] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set1}}
		statuses[2] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set2}}
		top, err := FindTopRunner(context.Background(), op, statuses)
		Expect(err).NotTo(HaveOccurred())
		Expect(top).To(Equal(2))

		statuses[0] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set2}}
		statuses[1] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set0}}
		statuses[2] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set1}}
		top, err = FindTopRunner(context.Background(), op, statuses)
		Expect(err).NotTo(HaveOccurred())
		Expect(top).To(Equal(0))

		statuses[0] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set1}}
		statuses[1] = nil
		statuses[2] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set2}}
		top, err = FindTopRunner(context.Background(), op, statuses)
		Expect(err).NotTo(HaveOccurred())
		Expect(top).To(Equal(2))

		// errant transactions
		set0 = `8e349184-bc14-11e3-8d4c-0800272864ba:1-30,
8e3648e4-bc14-11e3-8d4c-0800272864ba:1-7`
		set1 = `8e349184-bc14-11e3-8d4c-0800272864ba:1-29,
8e3648e4-bc14-11e3-8d4c-0800272864ba:1-9`
		statuses[0] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set0}}
		statuses[1] = &MySQLInstanceStatus{ReplicaStatus: &ReplicaStatus{RetrievedGtidSet: set1}}
		statuses[2] = nil
		_, err = FindTopRunner(context.Background(), op, statuses)
		Expect(err).To(MatchError(ErrErrantTransactions))
	})
})
