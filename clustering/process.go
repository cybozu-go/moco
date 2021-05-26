package clustering

import (
	"context"
	"fmt"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metricsSet struct {
	checkCount      prometheus.Counter
	errorCount      prometheus.Counter
	available       prometheus.Gauge
	healthy         prometheus.Gauge
	switchoverCount prometheus.Counter
	failoverCount   prometheus.Counter
	replicas        prometheus.Gauge
	readyReplicas   prometheus.Gauge
	errantReplicas  prometheus.Gauge
}

type managerProcess struct {
	client   client.Client
	reader   client.Reader
	recorder record.EventRecorder
	dbf      dbop.OperatorFactory
	agentf   AgentFactory
	name     types.NamespacedName
	log      logr.Logger
	cancel   func()

	ch            chan struct{}
	metrics       metricsSet
	deleteMetrics func()
}

func newManagerProcess(c client.Client, r client.Reader, recorder record.EventRecorder, dbf dbop.OperatorFactory, agentf AgentFactory, name types.NamespacedName, log logr.Logger, cancel func()) *managerProcess {
	return &managerProcess{
		client:   c,
		reader:   r,
		recorder: recorder,
		dbf:      dbf,
		agentf:   agentf,
		name:     name,
		log:      log,
		cancel:   cancel,
		ch:       make(chan struct{}, 1),
		metrics: metricsSet{
			checkCount:      metrics.CheckCountVec.WithLabelValues(name.Name, name.Namespace),
			errorCount:      metrics.ErrorCountVec.WithLabelValues(name.Name, name.Namespace),
			available:       metrics.AvailableVec.WithLabelValues(name.Name, name.Namespace),
			healthy:         metrics.HealthyVec.WithLabelValues(name.Name, name.Namespace),
			switchoverCount: metrics.SwitchoverCountVec.WithLabelValues(name.Name, name.Namespace),
			failoverCount:   metrics.FailoverCountVec.WithLabelValues(name.Name, name.Namespace),
			replicas:        metrics.TotalReplicasVec.WithLabelValues(name.Name, name.Namespace),
			readyReplicas:   metrics.ReadyReplicasVec.WithLabelValues(name.Name, name.Namespace),
			errantReplicas:  metrics.ErrantReplicasVec.WithLabelValues(name.Name, name.Namespace),
		},
		deleteMetrics: func() {
			metrics.CheckCountVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.ErrorCountVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.AvailableVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.HealthyVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.SwitchoverCountVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.FailoverCountVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.TotalReplicasVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.ReadyReplicasVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.ErrantReplicasVec.DeleteLabelValues(name.Name, name.Namespace)
		},
	}
}

func (p *managerProcess) Update() {
	select {
	case p.ch <- struct{}{}:
	default:
	}
}

func (p *managerProcess) Cancel() {
	p.cancel()
}

func (p *managerProcess) Start(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer func() {
		tick.Stop()
		p.deleteMetrics()
	}()

	for {
		select {
		case <-p.ch:
		case <-tick.C:
		case <-ctx.Done():
			p.log.Info("quit")
			return
		}

		p.metrics.checkCount.Inc()
		redo, err := p.do(ctx)
		if err != nil {
			p.metrics.errorCount.Inc()
			p.log.Error(err, "error")
			continue
		}

		if redo {
			// to update status quickly
			p.Update()
		}
	}
}

func (p *managerProcess) do(ctx context.Context) (bool, error) {
	ss, err := p.GatherStatus(ctx)
	if err != nil {
		return false, err
	}
	defer ss.Close()

	if err := p.updateStatus(ctx, ss); err != nil {
		return false, fmt.Errorf("failed to update status fields in MySQLCluster: %w", err)
	}

	p.log.Info("cluster state is " + ss.State.String())
	switch ss.State {
	case StateCloning:
		redo, err := p.clone(ctx, ss)
		if err != nil {
			event.InitCloneFailed.Emit(ss.Cluster, p.recorder, err)
			return false, fmt.Errorf("failed to clone data: %w", err)
		}
		event.InitCloneSucceeded.Emit(ss.Cluster, p.recorder)
		return redo, nil

	case StateRestoring:
		return false, nil

	case StateHealthy, StateDegraded:
		if ss.NeedSwitch {
			if err := p.switchover(ctx, ss); err != nil {
				event.SwitchOverFailed.Emit(ss.Cluster, p.recorder, err)
				return false, fmt.Errorf("failed to switchover: %w", err)
			}
			event.SwitchOverSucceeded.Emit(ss.Cluster, p.recorder, ss.Candidate)
			// do not configure the cluster after a switchover.
			return true, nil
		}
		if ss.State == StateDegraded {
			return p.configure(ctx, ss)
		}
		return false, nil

	case StateFailed:
		// in this case, only applicable operation is a failover.
		if err := p.failover(ctx, ss); err != nil {
			event.FailOverFailed.Emit(ss.Cluster, p.recorder, err)
			return false, fmt.Errorf("failed to failover: %w", err)
		}
		event.FailOverSucceeded.Emit(ss.Cluster, p.recorder, ss.Candidate)
		return true, nil

	case StateLost:
		// nothing can be done
		return false, nil

	case StateIncomplete:
		return p.configure(ctx, ss)
	}

	return false, nil
}

func (p *managerProcess) updateStatus(ctx context.Context, ss *StatusSet) error {
	now := metav1.Now()
	ststr := ss.State.String()
	updateCond := func(typ mocov1beta1.MySQLClusterConditionType, val corev1.ConditionStatus, current []mocov1beta1.MySQLClusterCondition) mocov1beta1.MySQLClusterCondition {
		updated := mocov1beta1.MySQLClusterCondition{
			Type:               typ,
			Status:             val,
			Reason:             ststr,
			Message:            "the current state is " + ststr,
			LastTransitionTime: now,
		}

		for _, cond := range current {
			if cond.Type != typ {
				continue
			}
			if cond.Status == val {
				updated.LastTransitionTime = cond.LastTransitionTime
			}
			break
		}
		return updated
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &mocov1beta1.MySQLCluster{}
		if err := p.reader.Get(ctx, p.name, cluster); err != nil {
			return err
		}
		orig := cluster.DeepCopy()

		initialized := corev1.ConditionTrue
		available := corev1.ConditionFalse
		healthy := corev1.ConditionFalse
		switch ss.State {
		case StateCloning, StateRestoring:
			initialized = corev1.ConditionFalse
		case StateHealthy:
			available = corev1.ConditionTrue
			healthy = corev1.ConditionTrue
		case StateDegraded:
			available = corev1.ConditionTrue
		case StateFailed:
		case StateLost:
		case StateIncomplete:
			idx := ss.Cluster.Status.CurrentPrimaryIndex
			if ss.MySQLStatus[idx] != nil && isPodReady(ss.Pods[idx]) {
				available = corev1.ConditionTrue
			}
		}
		conditions := []mocov1beta1.MySQLClusterCondition{
			updateCond(mocov1beta1.ConditionInitialized, initialized, cluster.Status.Conditions),
			updateCond(mocov1beta1.ConditionAvailable, available, cluster.Status.Conditions),
			updateCond(mocov1beta1.ConditionHealthy, healthy, cluster.Status.Conditions),
		}
		cluster.Status.Conditions = conditions
		if available == corev1.ConditionTrue {
			p.metrics.available.Set(1)
		} else {
			p.metrics.available.Set(0)
		}
		if healthy == corev1.ConditionTrue {
			p.metrics.healthy.Set(1)
		} else {
			p.metrics.healthy.Set(0)
		}

		var syncedReplicas int
		for _, pod := range ss.Pods {
			if isPodReady(pod) {
				syncedReplicas++
			}
		}
		cluster.Status.SyncedReplicas = syncedReplicas
		cluster.Status.ErrantReplicas = len(ss.Errants)
		cluster.Status.ErrantReplicaList = ss.Errants
		p.metrics.replicas.Set(float64(len(ss.Pods)))
		p.metrics.readyReplicas.Set(float64(syncedReplicas))
		p.metrics.errantReplicas.Set(float64(len(ss.Errants)))

		cluster.Status.Conditions = conditions
		if equality.Semantic.DeepEqual(orig, cluster) {
			return nil
		}

		p.log.Info("update the status information")
		return p.client.Status().Update(ctx, cluster)
	})
}
