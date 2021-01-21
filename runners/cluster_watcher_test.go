package runners

import (
	"context"
	"time"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/yaml"
)

func testMySQLClusterWatcher() {
	It("should notify generic events", func() {
		ctx := context.Background()
		ch := make(chan event.GenericEvent)
		watcher := NewMySQLClusterWatcher(k8sClient, ch, time.Second)
		go watcher.Start(ctx)

		manifest := `apiVersion: moco.cybozu.com/v1alpha1
kind: MySQLCluster
metadata:
  name: mysqlcluster
  namespace: default
spec:
  replicas: 3
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: mysql:dev
  dataVolumeClaimTemplateSpec:
    accessModes: [ "ReadWriteOnce" ]
    resources:
      requests:
        storage: 1Gi
  mysqlConfigMapName: mycnf
`
		cluster := mocov1alpha1.MySQLCluster{}
		err := yaml.Unmarshal([]byte(manifest), &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		err = k8sClient.Create(context.Background(), &cluster)
		Expect(err).ShouldNot(HaveOccurred())
		cluster.Status.Conditions = []mocov1alpha1.MySQLClusterCondition{
			{
				Type:               mocov1alpha1.ConditionInitialized,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(time.Now()),
			},
		}
		err = k8sClient.Status().Update(context.Background(), &cluster)
		Expect(err).ShouldNot(HaveOccurred())

		var ev event.GenericEvent
		select {
		case ev = <-ch:
			Expect(ev.Object.GetNamespace()).Should(Equal("default"))
			Expect(ev.Object.GetName()).Should(Equal("mysqlcluster"))
		case <-time.After(3 * time.Second):
			Fail("Generic Event wasn't fired!!")
		}
	})
	It("should not notify generic events with a no-initialized cluster", func() {
		// TODO
	})
}
