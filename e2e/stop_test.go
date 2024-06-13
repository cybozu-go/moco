package e2e

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/common/expfmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/stop.yaml
var stopYAML string

//go:embed testdata/stop_changed.yaml
var stopChangedYAML string

var _ = Context("stop reconciliation and clustering", func() {
	if doUpgrade {
		return
	}

	It("should construct a 3-instance cluster", func() {
		kubectlSafe(fillTemplate(stopYAML), "apply", "-f", "-")
		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())

		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "stop",
			"-u", "moco-writable",
			"--", "-e", "CREATE DATABASE test;")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "stop",
			"-u", "moco-writable",
			"--", "-e", "CREATE TABLE test.t1 (foo int);")
		kubectlSafe(nil, "moco", "mysql", "test",
			"-n", "stop",
			"-u", "moco-writable",
			"--", "-e", "INSERT INTO test.t1 (foo) VALUES (1); COMMIT;")
	})

	It("should stop reconciliation", func() {
		kubectlSafe(nil, "moco", "stop", "reconciliation", "test", "-n", "stop")
		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionReconciliationActive {
					continue
				}
				if cond.Status == metav1.ConditionFalse {
					return nil
				}
				return fmt.Errorf("reconciliation is active: %s", cond.Status)
			}
			return errors.New("no reconciliation condition")
		}).Should(Succeed())

		kubectlSafe(fillTemplate(stopChangedYAML), "apply", "-f", "-")

		timeout := 30 * time.Second
		Consistently(func() error {
			out, err := kubectl(nil, "get", "-n", "stop", "statefulset", "moco-test", "-o", "json")
			if err != nil {
				return err
			}
			sts := &appsv1.StatefulSet{}
			err = json.Unmarshal(out, sts)
			if err != nil {
				return err
			}

			if _, ok := sts.Spec.Template.Labels["foo"]; ok {
				return errors.New("label exists")
			}
			return nil
		}, timeout).Should(Succeed())
	})

	It("should restart reconciliation", func() {
		kubectlSafe(nil, "moco", "start", "reconciliation", "test", "-n", "stop")
		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionReconciliationActive {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("reconciliation is not active: %s", cond.Status)
			}
			return errors.New("no reconciliation condition")
		}).Should(Succeed())

		Eventually(func() error {
			out, err := kubectl(nil, "get", "-n", "stop", "statefulset", "moco-test", "-o", "json")
			if err != nil {
				return err
			}
			sts := &appsv1.StatefulSet{}
			err = json.Unmarshal(out, sts)
			if err != nil {
				return err
			}

			if _, ok := sts.Spec.Template.Labels["foo"]; !ok {
				return errors.New("label does not exists")
			}

			return nil
		}).Should(Succeed())
	})

	It("should stop clustering and prevent failover", func() {
		kubectlSafe(nil, "moco", "stop", "clustering", "test", "-n", "stop")
		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionClusteringActive {
					continue
				}
				if cond.Status == metav1.ConditionFalse {
					return nil
				}
				return fmt.Errorf("reconciliation is active: %s", cond.Status)
			}
			return errors.New("no reconciliation condition")
		}).Should(Succeed())

		cluster, err := getCluster("stop", "test")
		Expect(err).NotTo(HaveOccurred())

		currentPrimaryIndex := cluster.Status.CurrentPrimaryIndex
		kubectlSafe(nil, "exec",
			fmt.Sprintf("moco-test-%d", currentPrimaryIndex),
			"-n", "stop",
			"-c", "mysqld",
			"--", "kill", "1")

		timeout := 3 * time.Minute
		Consistently(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			if currentPrimaryIndex != cluster.Status.CurrentPrimaryIndex {
				return errors.New("failover executed while clustering was stopped")
			}
			return nil
		}, timeout).Should(Succeed())
	})

	It("should resume clustering and execute failover", func() {
		cluster, err := getCluster("stop", "test")
		Expect(err).NotTo(HaveOccurred())

		currentPrimaryIndex := cluster.Status.CurrentPrimaryIndex

		kubectlSafe(nil, "moco", "start", "clustering", "test", "-n", "stop")
		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionReconciliationActive {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("reconciliation is not active: %s", cond.Status)
			}
			return errors.New("no reconciliation condition")
		}).Should(Succeed())

		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			if currentPrimaryIndex == cluster.Status.CurrentPrimaryIndex {
				return errors.New("failover not yet executed")
			}

			for _, cond := range cluster.Status.Conditions {
				if cond.Type != mocov1beta2.ConditionHealthy {
					continue
				}
				if cond.Status == metav1.ConditionTrue {
					return nil
				}
				return fmt.Errorf("cluster is not healthy: %s", cond.Status)
			}
			return errors.New("no health condition")
		}).Should(Succeed())
	})

	It("active metrics", func() {
		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			reconcileCond := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionReconciliationActive)
			if reconcileCond.Status == metav1.ConditionFalse {
				return fmt.Errorf("reconciliation is stopped: %s", reconcileCond.Status)
			}
			clusteringCond := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionClusteringActive)
			if clusteringCond.Status == metav1.ConditionFalse {
				return fmt.Errorf("clustering is stopped: %s", clusteringCond.Status)
			}
			return nil
		}).Should(Succeed())

		out := kubectlSafe(nil, "-n", "moco-system", "get", "pods", "-l", "app.kubernetes.io/component=moco-controller", "-o", "json")
		pods := &corev1.PodList{}
		err := json.Unmarshal(out, pods)
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).To(HaveLen(1))
		addr := pods.Items[0].Status.PodIP
		out, err = runInPod("curl", "-sf", fmt.Sprintf("http://%s:8080/metrics", addr))
		Expect(err).NotTo(HaveOccurred())

		mfs, err := (&expfmt.TextParser{}).TextToMetricFamilies(bytes.NewReader(out))
		Expect(err).NotTo(HaveOccurred())

		clusteringMf := mfs["moco_cluster_clustering_stopped"]
		Expect(clusteringMf).NotTo(BeNil())
		clusteringMetric := findMetric(clusteringMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(clusteringMetric).NotTo(BeNil())
		Expect(clusteringMetric.GetGauge().GetValue()).To(BeNumerically("==", 0))

		reconcileMf := mfs["moco_cluster_reconciliation_stopped"]
		Expect(reconcileMf).NotTo(BeNil())
		reconcileMetric := findMetric(reconcileMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(reconcileMetric).NotTo(BeNil())
		Expect(reconcileMetric.GetGauge().GetValue()).To(BeNumerically("==", 0))
	})

	It("stopped metrics", func() {
		kubectlSafe(nil, "moco", "stop", "clustering", "test", "-n", "stop")
		kubectlSafe(nil, "moco", "stop", "reconciliation", "test", "-n", "stop")

		Eventually(func() error {
			cluster, err := getCluster("stop", "test")
			if err != nil {
				return err
			}
			reconcileCond := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionReconciliationActive)
			if reconcileCond.Status == metav1.ConditionTrue {
				return fmt.Errorf("reconciliation is active: %s", reconcileCond.Status)
			}
			clusteringCond := meta.FindStatusCondition(cluster.Status.Conditions, mocov1beta2.ConditionClusteringActive)
			if clusteringCond.Status == metav1.ConditionTrue {
				return fmt.Errorf("clustering is active: %s", clusteringCond.Status)
			}
			return nil
		}).Should(Succeed())

		out := kubectlSafe(nil, "-n", "moco-system", "get", "pods", "-l", "app.kubernetes.io/component=moco-controller", "-o", "json")
		pods := &corev1.PodList{}
		err := json.Unmarshal(out, pods)
		Expect(err).NotTo(HaveOccurred())
		Expect(pods.Items).To(HaveLen(1))
		addr := pods.Items[0].Status.PodIP
		out, err = runInPod("curl", "-sf", fmt.Sprintf("http://%s:8080/metrics", addr))
		Expect(err).NotTo(HaveOccurred())

		mfs, err := (&expfmt.TextParser{}).TextToMetricFamilies(bytes.NewReader(out))
		Expect(err).NotTo(HaveOccurred())

		clusteringMf := mfs["moco_cluster_clustering_stopped"]
		Expect(clusteringMf).NotTo(BeNil())
		clusteringMetric := findMetric(clusteringMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(clusteringMetric).NotTo(BeNil())
		Expect(clusteringMetric.GetGauge().GetValue()).To(BeNumerically("==", 1))

		reconcileMf := mfs["moco_cluster_reconciliation_stopped"]
		Expect(reconcileMf).NotTo(BeNil())
		reconcileMetric := findMetric(reconcileMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(reconcileMetric).NotTo(BeNil())
		Expect(reconcileMetric.GetGauge().GetValue()).To(BeNumerically("==", 1))

		healthyMf := mfs["moco_cluster_healthy"]
		Expect(healthyMf).NotTo(BeNil())
		healthyMetric := findMetric(healthyMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(healthyMetric).NotTo(BeNil())
		Expect(math.IsNaN(healthyMetric.GetGauge().GetValue())).To(BeTrue())

		availableMf := mfs["moco_cluster_available"]
		Expect(availableMf).NotTo(BeNil())
		availableMetric := findMetric(availableMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(availableMetric).NotTo(BeNil())
		Expect(math.IsNaN(availableMetric.GetGauge().GetValue())).To(BeTrue())

		readyReplicasMf := mfs["moco_cluster_ready_replicas"]
		Expect(readyReplicasMf).NotTo(BeNil())
		readyReplicasMetric := findMetric(readyReplicasMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(readyReplicasMetric).NotTo(BeNil())
		Expect(math.IsNaN(readyReplicasMetric.GetGauge().GetValue())).To(BeTrue())

		errantReplicasMf := mfs["moco_cluster_errant_replicas"]
		Expect(errantReplicasMf).NotTo(BeNil())
		errantReplicasMetric := findMetric(errantReplicasMf, map[string]string{"namespace": "stop", "name": "test"})
		Expect(errantReplicasMetric).NotTo(BeNil())
		Expect(math.IsNaN(errantReplicasMetric.GetGauge().GetValue())).To(BeTrue())
	})

	It("should delete clusters", func() {
		kubectlSafe(nil, "delete", "-n", "stop", "mysqlclusters", "--all")
		verifyAllPodsDeleted("stop")
	})
})
