package clustering

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func testSetupResources(ctx context.Context, replicas int32, sourceSecret string) {
	err := k8sClient.DeleteAllOf(ctx, &mocov1beta2.MySQLCluster{}, client.InNamespace("test"))
	Expect(err).NotTo(HaveOccurred())
	err = k8sClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace("test"))
	Expect(err).NotTo(HaveOccurred())
	err = k8sClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace("test"))
	Expect(err).NotTo(HaveOccurred())
	err = k8sClient.DeleteAllOf(ctx, &corev1.Event{}, client.InNamespace("test"))
	Expect(err).NotTo(HaveOccurred())

	cluster := &mocov1beta2.MySQLCluster{}
	cluster.Namespace = "test"
	cluster.Name = "test"
	cluster.Spec.Replicas = replicas
	cluster.Spec.ServerIDBase = 10
	cluster.Spec.VolumeClaimTemplates = []mocov1beta2.PersistentVolumeClaim{{}}
	cluster.Spec.PodTemplate.Spec = (mocov1beta2.PodSpecApplyConfiguration)(*corev1ac.PodSpec().WithContainers(
		corev1ac.Container().WithName("mysqld")),
	)
	if sourceSecret != "" {
		cluster.Spec.ReplicationSourceSecretName = &sourceSecret
	}
	err = k8sClient.Create(ctx, cluster)
	Expect(err).NotTo(HaveOccurred())

	for i := 0; i < int(replicas); i++ {
		pod := &corev1.Pod{}
		pod.Namespace = "test"
		pod.Name = cluster.PodName(i)
		pod.Labels = map[string]string{
			constants.LabelAppName:     constants.AppNameMySQL,
			constants.LabelAppInstance: cluster.Name,
		}
		pod.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "mysql"}}
		err = k8sClient.Create(ctx, pod)
		Expect(err).NotTo(HaveOccurred())
		pod.Status.PodIP = "0.0.0.0"
		err = k8sClient.Status().Update(ctx, pod)
		Expect(err).NotTo(HaveOccurred())
	}

	passwd := mysqlPassword.ToSecret()
	passwd.Namespace = "test"
	passwd.Name = cluster.UserSecretName()
	err = k8sClient.Create(ctx, passwd)
	Expect(err).NotTo(HaveOccurred())
}

func testGetCluster(ctx context.Context) (*mocov1beta2.MySQLCluster, error) {
	c := &mocov1beta2.MySQLCluster{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "test"}, c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

var _ = Describe("manager", func() {
	ctx := context.Background()
	var (
		stopFunc func()
		mgr      manager.Manager
		af       *mockAgentFactory
		of       *mockOpFactory
		ms       metricsSet
	)
	BeforeEach(func() {
		resetGTIDMap()
		af = &mockAgentFactory{}
		of = newMockOpFactory()

		reg := prometheus.NewRegistry()
		metrics.Register(reg)

		ms.checkCount = metrics.CheckCountVec.WithLabelValues("test", "test")
		ms.errorCount = metrics.ErrorCountVec.WithLabelValues("test", "test")
		ms.available = metrics.AvailableVec.WithLabelValues("test", "test")
		ms.healthy = metrics.HealthyVec.WithLabelValues("test", "test")
		ms.switchoverCount = metrics.SwitchoverCountVec.WithLabelValues("test", "test")
		ms.failoverCount = metrics.FailoverCountVec.WithLabelValues("test", "test")
		ms.replicas = metrics.TotalReplicasVec.WithLabelValues("test", "test")
		ms.readyReplicas = metrics.ReadyReplicasVec.WithLabelValues("test", "test")
		ms.errantReplicas = metrics.ErrantReplicasVec.WithLabelValues("test", "test")
		ms.backupTimestamp = metrics.BackupTimestamp.WithLabelValues("test", "test")
		ms.backupElapsed = metrics.BackupElapsed.WithLabelValues("test", "test")
		ms.backupDumpSize = metrics.BackupDumpSize.WithLabelValues("test", "test")
		ms.backupBinlogSize = metrics.BackupBinlogSize.WithLabelValues("test", "test")
		ms.backupWorkDirUsage = metrics.BackupWorkDirUsage.WithLabelValues("test", "test")
		ms.backupWarnings = metrics.BackupWarnings.WithLabelValues("test", "test")

		var err error
		mgr, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme:             scheme,
			LeaderElection:     false,
			MetricsBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(ctx)
		stopFunc = cancel
		go func() {
			err := mgr.Start(ctx)
			if err != nil {
				panic(err)
			}
		}()
		time.Sleep(10 * time.Millisecond)
	})
	JustAfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			cluster, err := testGetCluster(ctx)
			if err == nil {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "    ")
				enc.Encode(cluster)
			}
			return
		}

		By("checking connection leaks")
		Eventually(func() bool {
			return af.allClosed()
		}).Should(BeTrue())
		Eventually(func() bool {
			return of.allClosed()
		}).Should(BeTrue())
	})
	AfterEach(func() {
		stopFunc()
		of.Cleanup()
	})

	It("should setup one-instance cluster and clean up metrics when the cluster is deleted", func() {
		testSetupResources(ctx, 1, "")

		cm := NewClusterManager(1*time.Second, mgr, of, af, stdr.New(nil))
		defer cm.StopAll()

		cluster, err := testGetCluster(ctx)
		Expect(err).NotTo(HaveOccurred())
		cm.Update(client.ObjectKeyFromObject(cluster))

		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("not healthy")
			}
			return fmt.Errorf("no health condition")
		}).Should(Succeed())

		var isAvailable, isInitialized bool
		for _, cond := range cluster.Status.Conditions {
			switch cond.Type {
			case mocov1beta2.ConditionAvailable:
				isAvailable = cond.Status == corev1.ConditionTrue
			case mocov1beta2.ConditionInitialized:
				isInitialized = cond.Status == corev1.ConditionTrue
			}
		}
		Expect(isAvailable).To(BeTrue())
		Expect(isInitialized).To(BeTrue())

		Expect(cluster.Status.ErrantReplicaList).To(BeEmpty())
		Expect(cluster.Status.ErrantReplicas).To(Equal(0))
		Expect(cluster.Status.SyncedReplicas).To(Equal(1))

		events := &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		Expect(events.Items).To(HaveLen(1))
		Expect(events.Items[0].Reason).To(Equal(event.SetWritable.Reason))

		pod := &corev1.Pod{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PodName(0)}, pod)
		Expect(err).NotTo(HaveOccurred())
		Expect(pod.Labels[constants.LabelMocoRole]).To(Equal(constants.RolePrimary))

		st := of.getInstanceStatus(cluster.PodHostname(0))
		Expect(st).NotTo(BeNil())
		Expect(st.GlobalVariables.ReadOnly).To(BeFalse())

		Expect(ms.checkCount).To(MetricsIs(">", 0))
		Expect(ms.available).To(MetricsIs("==", 1))
		Expect(ms.healthy).To(MetricsIs("==", 1))
		Expect(ms.replicas).To(MetricsIs("==", 1))
		Expect(ms.readyReplicas).To(MetricsIs("==", 1))
		Expect(ms.errantReplicas).To(MetricsIs("==", 0))

		By("set the instance 0 failing")
		of.setFailing(cluster.PodHostname(0), true)
		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionFalse {
					return nil
				}
				return fmt.Errorf("cluster is still healthy")
			}
			return fmt.Errorf("no health status")
		}).Should(Succeed())

		for _, cond := range cluster.Status.Conditions {
			switch cond.Type {
			case mocov1beta2.ConditionAvailable:
				isAvailable = cond.Status == corev1.ConditionTrue
			case mocov1beta2.ConditionInitialized:
				isInitialized = cond.Status == corev1.ConditionTrue
			}
		}
		Expect(isAvailable).To(BeFalse())
		Expect(isInitialized).To(BeTrue())

		By("stopping the manager process")
		cm.Stop(client.ObjectKeyFromObject(cluster))
		time.Sleep(400 * time.Millisecond)
		of.setFailing(cluster.PodHostname(0), false)

		Eventually(func() bool {
			ch := make(chan prometheus.Metric, 2)
			metrics.ErrantReplicasVec.Collect(ch)
			select {
			case <-ch:
				return true
			default:
				return false
			}
		}).Should(BeFalse())

		cluster, err = testGetCluster(ctx)
		Expect(err).NotTo(HaveOccurred())
		for _, cond := range cluster.Status.Conditions {
			if cond.Type != mocov1beta2.ConditionHealthy {
				continue
			}
			Expect(cond.Status).To(Equal(corev1.ConditionFalse))
		}
	})

	It("should manage an intermediate primary, switchover, and scaling out the cluster", func() {
		testSetupResources(ctx, 1, "source")

		cm := NewClusterManager(1*time.Second, mgr, of, af, stdr.New(nil))
		defer cm.StopAll()

		cluster, err := testGetCluster(ctx)
		Expect(err).NotTo(HaveOccurred())
		cm.Update(client.ObjectKeyFromObject(cluster))
		defer func() {
			cm.Stop(client.ObjectKeyFromObject(cluster))
			time.Sleep(400 * time.Millisecond)
			Eventually(func() bool {
				ch := make(chan prometheus.Metric, 2)
				metrics.ErrantReplicasVec.Collect(ch)
				select {
				case <-ch:
					return true
				default:
					return false
				}
			}).Should(BeFalse())
		}()

		By("checking cloning status")
		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionInitialized {
					continue
				}
				if cond.Reason == StateCloning.String() {
					return nil
				}
				return fmt.Errorf("not cloning")
			}
			return fmt.Errorf("no initialized condition")
		}).Should(Succeed())

		Consistently(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}

			if cluster.Status.Cloned {
				return errors.New("status.cloned is set to true")
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionInitialized {
					continue
				}
				if cond.Reason == StateCloning.String() {
					return nil
				}
				return fmt.Errorf("not cloning")
			}
			return fmt.Errorf("no initialized condition")
		}, 3).Should(Succeed())

		var isAvailable, isHealthy bool
		for _, cond := range cluster.Status.Conditions {
			switch cond.Type {
			case mocov1beta2.ConditionAvailable:
				isAvailable = cond.Status == corev1.ConditionTrue
			case mocov1beta2.ConditionHealthy:
				isHealthy = cond.Status == corev1.ConditionTrue
			}
		}
		Expect(isAvailable).To(BeFalse())
		Expect(isHealthy).To(BeFalse())

		Expect(ms.checkCount).To(MetricsIs(">", 0))
		Expect(ms.errorCount).To(MetricsIs(">", 0))
		Expect(ms.available).To(MetricsIs("==", 0))
		Expect(ms.healthy).To(MetricsIs("==", 0))
		Expect(ms.replicas).To(MetricsIs("==", 1))
		Expect(ms.readyReplicas).To(MetricsIs("==", 0))

		By("creating source secret")
		sourceSecret := &corev1.Secret{}
		sourceSecret.Namespace = "test"
		sourceSecret.Name = "source"
		sourceSecret.Data = map[string][]byte{
			constants.CloneSourceHostKey:         []byte("external"),
			constants.CloneSourcePortKey:         []byte("3306"),
			constants.CloneSourceUserKey:         []byte("external-donor"),
			constants.CloneSourcePasswordKey:     []byte("p1"),
			constants.CloneSourceInitUserKey:     []byte("external-init"),
			constants.CloneSourceInitPasswordKey: []byte("init"),
		}
		err = k8sClient.Create(ctx, sourceSecret)
		Expect(err).NotTo(HaveOccurred())

		By("checking the cluster to become healthy")
		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("not healthy")
			}
			return fmt.Errorf("no health condition")
		}).Should(Succeed())

		Expect(cluster.Status.Cloned).To(BeTrue())

		events := &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		Expect(events.Items).NotTo(BeEmpty())
		var cloneErrors, cloneSuccesses, otherEvents int
		for _, ev := range events.Items {
			switch ev.Reason {
			case event.InitCloneSucceeded.Reason:
				cloneSuccesses++
			case event.InitCloneFailed.Reason:
				cloneErrors++
			default:
				otherEvents++
			}
		}
		Expect(cloneErrors).To(BeNumerically(">", 0))
		Expect(cloneSuccesses).To(Equal(1))
		Expect(otherEvents).To(Equal(0))

		By("scaling out the cluster from 1 to 3 instances")
		cluster.Spec.Replicas = 3
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		for i := 1; i < 3; i++ {
			pod := &corev1.Pod{}
			pod.Namespace = "test"
			pod.Name = cluster.PodName(i)
			pod.Labels = map[string]string{
				constants.LabelAppName:     constants.AppNameMySQL,
				constants.LabelAppInstance: cluster.Name,
			}
			pod.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "mysql"}}
			err = k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
			pod.Status.PodIP = "0.0.0.0"
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
		}

		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}

			if cluster.Status.SyncedReplicas != 3 {
				return errors.New("status is not updated")
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("not healthy")
			}
			return fmt.Errorf("no health condition")
		}).Should(Succeed())

		Expect(ms.available).To(MetricsIs("==", 1))
		Expect(ms.healthy).To(MetricsIs("==", 1))
		Expect(ms.replicas).To(MetricsIs("==", 3))
		Expect(ms.readyReplicas).To(MetricsIs("==", 3))

		for i := 0; i < 3; i++ {
			pod := &corev1.Pod{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PodName(i)}, pod)
			Expect(err).NotTo(HaveOccurred())
			switch i {
			case 0:
				Expect(pod.Labels[constants.LabelMocoRole]).To(Equal(constants.RolePrimary))
			default:
				Expect(pod.Labels[constants.LabelMocoRole]).To(Equal(constants.RoleReplica))
			}
		}

		st0 := of.getInstanceStatus(cluster.PodHostname(0))
		Expect(st0).NotTo(BeNil())
		Expect(st0.GlobalVariables.SuperReadOnly).To(BeTrue())
		Expect(st0.GlobalVariables.SemiSyncMasterEnabled).To(BeFalse())
		for i := 1; i < 3; i++ {
			st := of.getInstanceStatus(cluster.PodHostname(1))
			Expect(st.GlobalVariables.SuperReadOnly).To(BeTrue())
			Expect(st.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())
			Expect(st.ReplicaStatus).NotTo(BeNil())
			Expect(st.ReplicaStatus.SlaveIORunning).To(Equal("Yes"))
		}

		By("doing a switchover")
		// advance the executed GTID set on the source and the primary
		testSetGTID("external", "ex:1,ex:2,ex:3,ex:4,ex:5")
		testSetGTID(cluster.PodHostname(0), "ex:1,ex:2,ex:3,ex:4,ex:5")
		pod0 := &corev1.Pod{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PodName(0)}, pod0)
		Expect(err).NotTo(HaveOccurred())
		pod0.Annotations = map[string]string{constants.AnnDemote: "true"}
		err = k8sClient.Update(ctx, pod0)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}
			if cluster.Status.CurrentPrimaryIndex == 0 {
				return errors.New("the primary is not switched yet")
			}
			return nil
		}).Should(Succeed())
		newPrimary := cluster.Status.CurrentPrimaryIndex

		// check that MOCO waited for the GTID
		gtidNew, _ := testGetGTID(cluster.PodHostname(newPrimary))
		Expect(gtidNew).To(Equal("ex:1,ex:2,ex:3,ex:4,ex:5"))

		Eventually(func() int {
			st := of.getInstanceStatus(cluster.PodHostname(newPrimary))
			if st == nil {
				return 0
			}
			return len(st.ReplicaHosts)
		}).Should(Equal(2))

		events = &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		var switchOverEvents, cloneEvents int
		for _, ev := range events.Items {
			switch ev.Reason {
			case event.CloneSucceeded.Reason:
				cloneEvents++
			case event.SwitchOverSucceeded.Reason:
				switchOverEvents++
			}
		}
		Expect(cloneEvents).To(Equal(2))
		Expect(switchOverEvents).To(Equal(1))

		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("not healthy")
			}
			return fmt.Errorf("no health condition")
		}).Should(Succeed())

		Expect(cluster.Status.SyncedReplicas).To(Equal(3))
		Expect(ms.available).To(MetricsIs("==", 1))
		Expect(ms.healthy).To(MetricsIs("==", 1))
		Expect(ms.replicas).To(MetricsIs("==", 3))
		Expect(ms.readyReplicas).To(MetricsIs("==", 3))
		Expect(ms.errantReplicas).To(MetricsIs("==", 0))
		Expect(ms.switchoverCount).To(MetricsIs("==", 1))
		Expect(ms.failoverCount).To(MetricsIs("==", 0))

		for i := 0; i < 3; i++ {
			pod := &corev1.Pod{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PodName(i)}, pod)
			Expect(err).NotTo(HaveOccurred())
			if i == newPrimary {
				Expect(pod.Annotations).NotTo(HaveKey(constants.AnnDemote))
				Expect(pod.Labels[constants.LabelMocoRole]).To(Equal(constants.RolePrimary))
			} else {
				Expect(pod.Labels[constants.LabelMocoRole]).To(Equal(constants.RoleReplica))
			}
		}

		for i := 0; i < 3; i++ {
			st := of.getInstanceStatus(cluster.PodHostname(i))
			Expect(st).NotTo(BeNil())
			Expect(st.GlobalVariables.SuperReadOnly).To(BeTrue())
			Expect(st.GlobalVariables.SemiSyncSlaveEnabled).To(BeFalse())
			Expect(st.ReplicaStatus).NotTo(BeNil())
			if i == newPrimary {
				Expect(st.ReplicaStatus.MasterHost).To(Equal("external"))
			} else {
				Expect(st.ReplicaStatus.MasterHost).To(Equal(cluster.PodHostname(newPrimary)))
			}
		}

		By("stopping replication from external mysqld")
		// advance the source GTID beforehand
		testSetGTID("external", "ex:1,ex:2,ex:3,ex:4,ex:5,ex:6")

		cluster.Spec.ReplicationSourceSecretName = nil
		err = k8sClient.Update(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			st := of.getInstanceStatus(cluster.PodHostname(newPrimary))
			if st == nil {
				return errors.New("failed to get status")
			}
			if !st.GlobalVariables.ReadOnly {
				return nil
			}
			return errors.New("the primary is still read-only")
		}).Should(Succeed())

		for i := 0; i < 3; i++ {
			st := of.getInstanceStatus(cluster.PodHostname(i))
			Expect(st).NotTo(BeNil())
			if i == newPrimary {
				Expect(st.GlobalVariables.ReadOnly).To(BeFalse())
				Expect(st.GlobalVariables.SemiSyncMasterEnabled).To(BeTrue())
				Expect(st.ReplicaStatus).To(BeNil())
				Expect(st.ReplicaHosts).To(HaveLen(2))
			} else {
				Expect(st.GlobalVariables.SuperReadOnly).To(BeTrue())
				Expect(st.GlobalVariables.SemiSyncSlaveEnabled).To(BeTrue())
				Expect(st.ReplicaStatus).NotTo(BeNil())
				Expect(st.ReplicaStatus.MasterHost).To(Equal(cluster.PodHostname(newPrimary)))
			}
		}
	})

	It("should handle failover and errant replicas", func() {
		testSetupResources(ctx, 5, "")

		cm := NewClusterManager(1*time.Second, mgr, of, af, stdr.New(nil))
		defer cm.StopAll()

		cluster, err := testGetCluster(ctx)
		Expect(err).NotTo(HaveOccurred())
		cm.Update(client.ObjectKeyFromObject(cluster))
		defer func() {
			cm.Stop(client.ObjectKeyFromObject(cluster))
			time.Sleep(400 * time.Millisecond)
			Eventually(func() bool {
				ch := make(chan prometheus.Metric, 2)
				metrics.ErrantReplicasVec.Collect(ch)
				select {
				case <-ch:
					return true
				default:
					return false
				}
			}).Should(BeFalse())
		}()

		Eventually(func() error {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == corev1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("not healthy")
			}
			return fmt.Errorf("no health condition")
		}).Should(Succeed())

		By("making an errant replica")
		// When the primary load is high, sometimes the gtid_executed of a replica precedes the primary.
		// pod(4) is intended for such situations.
		testSetGTID(cluster.PodHostname(0), "p0:1,p0:2,p0:3") // primary
		testSetGTID(cluster.PodHostname(1), "p0:1,p0:2,p1:1") // errant replica
		testSetGTID(cluster.PodHostname(2), "p0:1")
		testSetGTID(cluster.PodHostname(3), "p0:1,p0:2,p0:3")
		testSetGTID(cluster.PodHostname(4), "p0:1,p0:2,p0:3,p0:4")
		Eventually(func() int {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return 0
			}
			return cluster.Status.ErrantReplicas
		}).Should(Equal(1))

		var isHealthy, isAvailable bool
		for _, cond := range cluster.Status.Conditions {
			switch cond.Type {
			case mocov1beta2.ConditionHealthy:
				isHealthy = cond.Status == corev1.ConditionTrue
			case mocov1beta2.ConditionAvailable:
				isAvailable = cond.Status == corev1.ConditionTrue
			}
		}
		Expect(isHealthy).To(BeFalse())
		Expect(isAvailable).To(BeTrue())

		Expect(ms.available).To(MetricsIs("==", 1))
		Expect(ms.healthy).To(MetricsIs("==", 0))
		Expect(ms.errantReplicas).To(MetricsIs("==", 1))

		Eventually(func() bool {
			pod := &corev1.Pod{}
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PodName(1)}, pod)
			if err != nil {
				return true
			}
			_, ok := pod.Labels[constants.LabelMocoRole]
			return ok
		}).Should(BeFalse())

		st1 := of.getInstanceStatus(cluster.PodHostname(1))
		Expect(st1).NotTo(BeNil())
		if st1.ReplicaHosts != nil {
			Expect(st1.ReplicaStatus.SlaveIORunning).NotTo(Equal("Yes"))
		}

		By("triggering a failover")
		of.setRetrievedGTIDSet(cluster.PodHostname(2), "p0:1")
		of.setRetrievedGTIDSet(cluster.PodHostname(3), "p0:1,p0:2,p0:3,p0:4")
		of.setRetrievedGTIDSet(cluster.PodHostname(4), "p0:1,p0:2,p0:3,p0:4")
		of.setFailing(cluster.PodHostname(0), true)

		Eventually(func() int {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return -1
			}
			return cluster.Status.CurrentPrimaryIndex
		}).Should(Equal(3))

		// confirm that MOCO waited fot the retrieved GTID set to be executed
		st3 := of.getInstanceStatus(cluster.PodHostname(3))
		Expect(st3.GlobalVariables.ExecutedGTID).To(Equal("p0:1,p0:2,p0:3,p0:4"))

		Eventually(func() bool {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return false
			}
			for _, cond := range cluster.Status.Conditions {
				switch cond.Type {
				case mocov1beta2.ConditionHealthy:
					isHealthy = cond.Status == corev1.ConditionTrue
				case mocov1beta2.ConditionAvailable:
					if cond.Status == corev1.ConditionTrue {
						return true
					}
				}
			}
			return false
		}).Should(BeTrue())
		Expect(isHealthy).To(BeFalse())

		st3 = of.getInstanceStatus(cluster.PodHostname(3))
		Expect(st3.GlobalVariables.ReadOnly).To(BeFalse())

		Expect(cluster.Status.ErrantReplicas).To(Equal(1))
		Expect(cluster.Status.ErrantReplicaList).To(Equal([]int{1}))

		for i := 0; i < 5; i++ {
			pod := &corev1.Pod{}
			err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: cluster.PodName(i)}, pod)
			Expect(err).NotTo(HaveOccurred())
			switch i {
			case 0, 2, 4:
				Expect(pod.Labels).To(HaveKeyWithValue(constants.LabelMocoRole, constants.RoleReplica))
			case 1:
				Expect(pod.Labels).NotTo(HaveKey(constants.LabelMocoRole))
			case 3:
				Expect(pod.Labels).To(HaveKeyWithValue(constants.LabelMocoRole, constants.RolePrimary))
			}
		}

		Expect(ms.available).To(MetricsIs("==", 1))
		Expect(ms.healthy).To(MetricsIs("==", 0))
		Expect(ms.replicas).To(MetricsIs("==", 5))
		Expect(ms.errantReplicas).To(MetricsIs("==", 1))
		Expect(ms.switchoverCount).To(MetricsIs("==", 0))
		Expect(ms.failoverCount).To(MetricsIs("==", 1))

		events := &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		var failOverEvents int
		for _, ev := range events.Items {
			switch ev.Reason {
			case event.FailOverSucceeded.Reason:
				failOverEvents++
			}
		}
		Expect(failOverEvents).To(Equal(1))

		By("re-initializing the errant replica")
		testSetGTID(cluster.PodHostname(1), "")
		Eventually(func() interface{} {
			return ms.errantReplicas
		}).Should(MetricsIs("==", 0))

		By("stopping instances to make the cluster lost")
		of.setFailing(cluster.PodHostname(3), true)
		of.setFailing(cluster.PodHostname(1), true)

		Eventually(func() bool {
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return false
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionAvailable {
					continue
				}
				if cond.Status != corev1.ConditionFalse {
					continue
				}
				return cond.Reason == StateLost.String()
			}
			return false
		}).Should(BeTrue())
	})

	It("should export backup related metrics", func() {
		testSetupResources(ctx, 1, "")

		cm := NewClusterManager(1*time.Second, mgr, of, af, stdr.New(nil))
		defer cm.StopAll()

		var cluster *mocov1beta2.MySQLCluster
		Eventually(func() error {
			var err error
			cluster, err = testGetCluster(ctx)
			if err != nil {
				return err
			}
			cluster.Status.Backup.Time = metav1.Now()
			cluster.Status.Backup.Elapsed = metav1.Duration{Duration: time.Minute}
			cluster.Status.Backup.DumpSize = 10
			cluster.Status.Backup.BinlogSize = 20
			cluster.Status.Backup.WorkDirUsage = 30
			cluster.Status.Backup.Warnings = []string{"aaa", "bbb"}
			return k8sClient.Status().Update(ctx, cluster)
		}).Should(Succeed())

		cm.Update(client.ObjectKeyFromObject(cluster))

		Eventually(func() interface{} {
			return ms.backupTimestamp
		}).ShouldNot(MetricsIs("==", 0))
		time.Sleep(10 * time.Millisecond)
		Expect(ms.backupElapsed).To(MetricsIs("==", 60))
		Expect(ms.backupDumpSize).To(MetricsIs("==", 10))
		Expect(ms.backupBinlogSize).To(MetricsIs("==", 20))
		Expect(ms.backupWorkDirUsage).To(MetricsIs("==", 30))
		Expect(ms.backupWarnings).To(MetricsIs("==", 2))
	})
})
