package clustering

import (
	"context"
	"fmt"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
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
	processingTime  prometheus.Observer

	backupTimestamp    prometheus.Gauge
	backupElapsed      prometheus.Gauge
	backupDumpSize     prometheus.Gauge
	backupBinlogSize   prometheus.Gauge
	backupWorkDirUsage prometheus.Gauge
	backupWarnings     prometheus.Gauge
}

type managerProcess struct {
	client   client.Client
	reader   client.Reader
	recorder record.EventRecorder
	dbf      dbop.OperatorFactory
	agentf   AgentFactory
	name     types.NamespacedName
	cancel   func()

	ch            chan string
	metrics       metricsSet
	deleteMetrics func()
}

func newManagerProcess(c client.Client, r client.Reader, recorder record.EventRecorder, dbf dbop.OperatorFactory, agentf AgentFactory, name types.NamespacedName, cancel func()) *managerProcess {
	return &managerProcess{
		client:   c,
		reader:   r,
		recorder: recorder,
		dbf:      dbf,
		agentf:   agentf,
		name:     name,
		cancel:   cancel,
		ch:       make(chan string, 1),
		metrics: metricsSet{
			checkCount:         metrics.CheckCountVec.WithLabelValues(name.Name, name.Namespace),
			errorCount:         metrics.ErrorCountVec.WithLabelValues(name.Name, name.Namespace),
			available:          metrics.AvailableVec.WithLabelValues(name.Name, name.Namespace),
			healthy:            metrics.HealthyVec.WithLabelValues(name.Name, name.Namespace),
			switchoverCount:    metrics.SwitchoverCountVec.WithLabelValues(name.Name, name.Namespace),
			failoverCount:      metrics.FailoverCountVec.WithLabelValues(name.Name, name.Namespace),
			replicas:           metrics.TotalReplicasVec.WithLabelValues(name.Name, name.Namespace),
			readyReplicas:      metrics.ReadyReplicasVec.WithLabelValues(name.Name, name.Namespace),
			errantReplicas:     metrics.ErrantReplicasVec.WithLabelValues(name.Name, name.Namespace),
			processingTime:     metrics.ProcessingTimeVec.WithLabelValues(name.Name, name.Namespace),
			backupTimestamp:    metrics.BackupTimestamp.WithLabelValues(name.Name, name.Namespace),
			backupElapsed:      metrics.BackupElapsed.WithLabelValues(name.Name, name.Namespace),
			backupDumpSize:     metrics.BackupDumpSize.WithLabelValues(name.Name, name.Namespace),
			backupBinlogSize:   metrics.BackupBinlogSize.WithLabelValues(name.Name, name.Namespace),
			backupWorkDirUsage: metrics.BackupWorkDirUsage.WithLabelValues(name.Name, name.Namespace),
			backupWarnings:     metrics.BackupWarnings.WithLabelValues(name.Name, name.Namespace),
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
			metrics.ProcessingTimeVec.DeleteLabelValues(name.Name, name.Namespace)
			metrics.BackupTimestamp.DeleteLabelValues(name.Name, name.Namespace)
			metrics.BackupElapsed.DeleteLabelValues(name.Name, name.Namespace)
			metrics.BackupDumpSize.DeleteLabelValues(name.Name, name.Namespace)
			metrics.BackupBinlogSize.DeleteLabelValues(name.Name, name.Namespace)
			metrics.BackupWorkDirUsage.DeleteLabelValues(name.Name, name.Namespace)
			metrics.BackupWarnings.DeleteLabelValues(name.Name, name.Namespace)
		},
	}
}

func (p *managerProcess) Update(origin string) {
	select {
	case p.ch <- origin:
	default:
	}
}

func (p *managerProcess) Cancel() {
	p.cancel()
}

func (p *managerProcess) Start(ctx context.Context, rootLog logr.Logger, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer func() {
		tick.Stop()
		p.deleteMetrics()
	}()

	for {
		origin := "interval"
		select {
		case origin = <-p.ch:
		case <-tick.C:
		case <-ctx.Done():
			rootLog.Info("quit")
			return
		}

		log := rootLog.WithValues("operationId", "op-"+rand.String(5))
		log.Info("start operation", "origin", origin)
		p.metrics.checkCount.Inc()
		startTime := time.Now()
		redo, err := p.do(logr.NewContext(ctx, log))
		duration := time.Since(startTime)
		p.metrics.processingTime.Observe(duration.Seconds())
		if err != nil {
			p.metrics.errorCount.Inc()
			log.Error(err, "error", "duration", duration)
			continue
		}
		log.Info("finish", "duration", duration)

		if redo {
			// to update status quickly
			p.Update("redo")
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

	logFromContext(ctx).Info("cluster state is " + ss.State.String())
	switch ss.State {
	case StateCloning:
		if p.isCloning(ctx, ss) {
			return false, nil
		}

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
	bs := &ss.Cluster.Status.Backup
	if !bs.Time.IsZero() {
		p.metrics.backupTimestamp.Set(float64(bs.Time.Unix()))
		p.metrics.backupElapsed.Set(bs.Elapsed.Seconds())
		p.metrics.backupDumpSize.Set(float64(bs.DumpSize))
		p.metrics.backupBinlogSize.Set(float64(bs.BinlogSize))
		p.metrics.backupWorkDirUsage.Set(float64(bs.WorkDirUsage))
		p.metrics.backupWarnings.Set(float64(len(bs.Warnings)))
	}

	ststr := ss.State.String()
	updateCond := func(typ string, val metav1.ConditionStatus) metav1.Condition {
		updated := metav1.Condition{
			Type:    typ,
			Status:  val,
			Reason:  ststr,
			Message: "the current state is " + ststr,
		}
		return updated
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &mocov1beta2.MySQLCluster{}
		if err := p.reader.Get(ctx, p.name, cluster); err != nil {
			return err
		}
		orig := cluster.DeepCopy()

		initialized := metav1.ConditionTrue
		available := metav1.ConditionFalse
		healthy := metav1.ConditionFalse
		switch ss.State {
		case StateCloning, StateRestoring:
			initialized = metav1.ConditionFalse
		case StateHealthy:
			available = metav1.ConditionTrue
			healthy = metav1.ConditionTrue
		case StateDegraded:
			available = metav1.ConditionTrue
		case StateFailed:
		case StateLost:
		case StateIncomplete:
		}

		meta.SetStatusCondition(&cluster.Status.Conditions, updateCond(mocov1beta2.ConditionInitialized, initialized))
		meta.SetStatusCondition(&cluster.Status.Conditions, updateCond(mocov1beta2.ConditionAvailable, available))
		meta.SetStatusCondition(&cluster.Status.Conditions, updateCond(mocov1beta2.ConditionHealthy, healthy))

		if available == metav1.ConditionTrue {
			p.metrics.available.Set(1)
		} else {
			p.metrics.available.Set(0)
		}
		if healthy == metav1.ConditionTrue {
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

		// the completion of initial cloning is recorded in the status
		// to make it possible to determine the cloning status even while
		// the primary instance is down.
		if cluster.Spec.ReplicationSourceSecretName != nil && ss.State != StateCloning {
			cluster.Status.Cloned = true
		}

		// if nothing has changed, skip updating.
		if equality.Semantic.DeepEqual(orig, cluster) {
			return nil
		}

		logFromContext(ctx).Info("update the status information")
		return p.client.Status().Update(ctx, cluster)
	})
}
