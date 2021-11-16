package clustering

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	agent "github.com/cybozu-go/moco-agent/proto"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/event"
	"google.golang.org/protobuf/types/known/durationpb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	switchOverTimeoutSeconds = 70
	failOverTimeoutSeconds   = 3600
)

var waitForRestartDuration = 3 * time.Second

func init() {
	intervalStr := os.Getenv("MOCO_WAIT_INTERVAL")
	if intervalStr == "" {
		return
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return
	}
	waitForRestartDuration = interval
}

func (p *managerProcess) isCloning(ctx context.Context, ss *StatusSet) bool {
	pst := ss.MySQLStatus[ss.Primary]
	if pst == nil {
		p.log.Info("the status of the primary is missing")
		return true
	}
	if pst.CloneStatus != nil && pst.CloneStatus.State.String != "Failed" {
		p.log.Info("cloning...", "state", pst.CloneStatus.State.String)
		return true
	}
	return false
}

func (p *managerProcess) clone(ctx context.Context, ss *StatusSet) (bool, error) {
	secret := &corev1.Secret{}
	name := client.ObjectKey{Namespace: ss.Cluster.Namespace, Name: *ss.Cluster.Spec.ReplicationSourceSecretName}
	if err := p.client.Get(ctx, name, secret); err != nil {
		return false, fmt.Errorf("failed to get secret %s: %w", name.String(), err)
	}

	req := &agent.CloneRequest{}
	if val, ok := secret.Data[constants.CloneSourceHostKey]; !ok {
		return false, fmt.Errorf("no %s in secret %s", constants.CloneSourceHostKey, name.String())
	} else {
		req.Host = string(val)
	}
	if val, ok := secret.Data[constants.CloneSourcePortKey]; !ok {
		return false, fmt.Errorf("no %s in secret %s", constants.CloneSourcePortKey, name.String())
	} else {
		n, err := strconv.ParseInt(string(val), 10, 32)
		if err != nil {
			return false, fmt.Errorf("bad port number in secret %s: %w", name.String(), err)
		}
		if n <= 0 {
			return false, fmt.Errorf("bad port number in secret %s", name.String())
		}
		req.Port = int32(n)
	}
	if val, ok := secret.Data[constants.CloneSourceUserKey]; !ok {
		return false, fmt.Errorf("no %s in secret %s", constants.CloneSourceUserKey, name.String())
	} else {
		req.User = string(val)
	}
	if val, ok := secret.Data[constants.CloneSourcePasswordKey]; !ok {
		return false, fmt.Errorf("no %s in secret %s", constants.CloneSourcePasswordKey, name.String())
	} else {
		req.Password = string(val)
	}
	if val, ok := secret.Data[constants.CloneSourceInitUserKey]; !ok {
		return false, fmt.Errorf("no %s in secret %s", constants.CloneSourceInitUserKey, name.String())
	} else {
		req.InitUser = string(val)
	}
	if val, ok := secret.Data[constants.CloneSourceInitPasswordKey]; !ok {
		return false, fmt.Errorf("no %s in secret %s", constants.CloneSourceInitPasswordKey, name.String())
	} else {
		req.InitPassword = string(val)
	}
	req.BootTimeout = durationpb.New(time.Duration(ss.Cluster.Spec.StartupWaitSeconds) * time.Second)

	ag, err := p.agentf.New(ctx, ss.Cluster, ss.Primary)
	if err != nil {
		return false, fmt.Errorf("failed to connect to moco-agent for instance %d: %w", ss.Primary, err)
	}
	defer ag.Close()

	p.log.Info("begin cloning data", "source", req.Host)
	if _, err := ag.Clone(ctx, req); err != nil {
		p.log.Error(err, "clone failed", "source", req.Host)
		return false, fmt.Errorf("failed to clone data from %s: %w", req.Host, err)
	}

	p.log.Info("clone succeeded", "source", req.Host)

	// wait until the instance restarts after clone
	op := ss.DBOps[ss.Primary]
	time.Sleep(waitForRestartDuration)
	for i := 0; i < 60; i++ {
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return false, ctx.Err()
		}

		_, err := op.GetStatus(ctx)
		if err == nil {
			break
		}
	}
	return true, nil
}

func (p *managerProcess) switchover(ctx context.Context, ss *StatusSet) error {
	p.log.Info("begin switchover the primary", "current", ss.Primary, "next", ss.Candidate)

	pdb := ss.DBOps[ss.Primary]
	if err := pdb.SetReadOnly(ctx, true); err != nil {
		return fmt.Errorf("failed to make instance %d read-only: %w", ss.Primary, err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := pdb.KillConnections(ctx); err != nil {
		return fmt.Errorf("failed to kill connections in instance %d: %w", ss.Primary, err)
	}
	pst, err := pdb.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get the primary status: %w", err)
	}

	err = ss.DBOps[ss.Candidate].WaitForGTID(ctx, pst.GlobalVariables.ExecutedGTID, switchOverTimeoutSeconds)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &mocov1beta1.MySQLCluster{}
		if err := p.reader.Get(ctx, p.name, cluster); err != nil {
			return err
		}
		cluster.Status.CurrentPrimaryIndex = ss.Candidate
		return p.client.Status().Update(ctx, cluster)
	})
	if err != nil {
		return fmt.Errorf("failed to set the current primary index: %w", err)
	}

	p.metrics.switchoverCount.Inc()

	ppod := ss.Pods[ss.Primary]
	if _, ok := ppod.Annotations[constants.AnnDemote]; ok {
		newPod := ppod.DeepCopy()
		delete(newPod.Annotations, constants.AnnDemote)
		if err := p.client.Patch(ctx, newPod, client.MergeFrom(ppod)); err != nil {
			return fmt.Errorf("failed to remove moco.cybozu.com/demote annotation: %w", err)
		}
	}
	p.log.Info("switchover finished", "primary", ss.Candidate)
	return nil
}

func (p *managerProcess) failover(ctx context.Context, ss *StatusSet) error {
	p.log.Info("begin failover the primary", "current", ss.Primary)

	// stop all replica IO threads
	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			continue
		}
		if err := ss.DBOps[i].StopReplicaIOThread(ctx); err != nil {
			return fmt.Errorf("failed to stop replica IO thread for instance %d: %w", i, err)
		}
	}

	// recheck the latest replication status
	time.Sleep(100 * time.Millisecond)
	candidates := make([]*dbop.MySQLInstanceStatus, len(ss.MySQLStatus))
	var op dbop.Operator
	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			continue
		}
		if ist.IsErrant {
			continue
		}
		op = ss.DBOps[i]
		newStatus, err := op.GetStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to recheck the status of instance %d: %w", i, err)
		}
		candidates[i] = newStatus
	}

	candidate, err := op.FindTopRunner(ctx, candidates)
	if err != nil {
		return fmt.Errorf("failed to choose the next primary: %w", err)
	}
	ss.Candidate = candidate

	gtid := candidates[candidate].ReplicaStatus.RetrievedGtidSet
	p.log.Info("waiting for the new primary to execute all retrieved transactions", "index", candidate, "gtid", gtid)
	err = ss.DBOps[candidate].WaitForGTID(ctx, gtid, failOverTimeoutSeconds)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &mocov1beta1.MySQLCluster{}
		if err := p.reader.Get(ctx, p.name, cluster); err != nil {
			return err
		}
		cluster.Status.CurrentPrimaryIndex = candidate
		return p.client.Status().Update(ctx, cluster)
	})
	if err != nil {
		return fmt.Errorf("failed to set the current primary index: %w", err)
	}

	p.metrics.failoverCount.Inc()

	p.log.Info("failover finished", "primary", candidate)
	return nil
}

func (p *managerProcess) configure(ctx context.Context, ss *StatusSet) (bool, error) {
	redo := false

	if ss.Cluster.Spec.ReplicationSourceSecretName != nil {
		r, err := p.configureIntermediatePrimary(ctx, ss)
		if err != nil {
			return false, err
		}
		redo = redo || r
	} else {
		r, err := p.configurePrimary(ctx, ss)
		if err != nil {
			return false, err
		}
		redo = redo || r
	}

	for i, ist := range ss.MySQLStatus {
		if i == ss.Primary {
			continue
		}
		if ist == nil {
			continue
		}
		r, err := p.configureReplica(ctx, ss, i)
		if err != nil {
			return false, fmt.Errorf("failed to configure replica instance %d: %w", i, err)
		}
		redo = redo || r
	}

	// update labels
	for i, pod := range ss.Pods {
		if i == ss.Primary {
			if pod.Labels[constants.LabelMocoRole] != constants.RolePrimary {
				redo = true
				modified := pod.DeepCopy()
				if modified.Labels == nil {
					modified.Labels = make(map[string]string)
				}
				modified.Labels[constants.LabelMocoRole] = constants.RolePrimary
				if err := p.client.Patch(ctx, modified, client.MergeFrom(pod)); err != nil {
					return false, fmt.Errorf("failed to set role for pod %s/%s: %w", pod.Namespace, pod.Name, err)
				}
			}
			continue
		}

		if ss.MySQLStatus[i] != nil && ss.MySQLStatus[i].IsErrant {
			if _, ok := pod.Labels[constants.LabelMocoRole]; ok {
				redo = true
				modified := pod.DeepCopy()
				delete(modified.Labels, constants.LabelMocoRole)
				if err := p.client.Patch(ctx, modified, client.MergeFrom(pod)); err != nil {
					return false, fmt.Errorf("failed to set role for pod %s/%s: %w", pod.Namespace, pod.Name, err)
				}
			}
			continue
		}

		if pod.Labels[constants.LabelMocoRole] != constants.RoleReplica {
			redo = true
			modified := pod.DeepCopy()
			if modified.Labels == nil {
				modified.Labels = make(map[string]string)
			}
			modified.Labels[constants.LabelMocoRole] = constants.RoleReplica
			if err := p.client.Patch(ctx, modified, client.MergeFrom(pod)); err != nil {
				return false, fmt.Errorf("failed to set role for pod %s/%s: %w", pod.Namespace, pod.Name, err)
			}
		}
	}

	// make the primary writable if it is not an intermediate primary
	if ss.Cluster.Spec.ReplicationSourceSecretName == nil {
		pst := ss.MySQLStatus[ss.Primary]
		op := ss.DBOps[ss.Primary]
		if pst.GlobalVariables.ReadOnly {
			redo = true
			p.log.Info("set read_only=0", "instance", ss.Primary)
			if err := op.SetReadOnly(ctx, false); err != nil {
				return false, fmt.Errorf("failed to make the primary writable: %w", err)
			}
			event.SetWritable.Emit(ss.Cluster, p.recorder)
		}
	}
	return redo, nil
}

func (p *managerProcess) configureIntermediatePrimary(ctx context.Context, ss *StatusSet) (redo bool, e error) {
	pst := ss.MySQLStatus[ss.Primary]
	op := ss.DBOps[ss.Primary]
	if !pst.GlobalVariables.SuperReadOnly {
		redo = true
		p.log.Info("set super_read_only=1", "instance", ss.Primary)
		if err := op.SetReadOnly(ctx, true); err != nil {
			return false, err
		}
	}

	secret := &corev1.Secret{}
	name := client.ObjectKey{Namespace: ss.Cluster.Namespace, Name: *ss.Cluster.Spec.ReplicationSourceSecretName}
	if err := p.client.Get(ctx, name, secret); err != nil {
		return false, fmt.Errorf("failed to get secret %s: %w", name.String(), err)
	}
	port, err := strconv.Atoi(string(secret.Data[constants.CloneSourcePortKey]))
	if err != nil {
		return false, fmt.Errorf("invalid port number in secret %s: %w", name.String(), err)
	}

	ai := dbop.AccessInfo{
		Host:     string(secret.Data[constants.CloneSourceHostKey]),
		Port:     port,
		User:     string(secret.Data[constants.CloneSourceUserKey]),
		Password: string(secret.Data[constants.CloneSourcePasswordKey]),
	}
	if pst.ReplicaStatus == nil || pst.ReplicaStatus.SlaveIORunning != "Yes" || pst.ReplicaStatus.MasterHost != ai.Host {
		redo = true
		p.log.Info("start replication", "instance", ss.Primary, "semisync", false)
		if err := op.ConfigureReplica(ctx, ai, false); err != nil {
			return false, err
		}
	}
	return
}

func (p *managerProcess) configurePrimary(ctx context.Context, ss *StatusSet) (redo bool, e error) {
	pst := ss.MySQLStatus[ss.Primary]
	op := ss.DBOps[ss.Primary]

	// wait for all retrieved transactions to be executed if this used to be an intermediate replica
	if pst.ReplicaStatus != nil && pst.ReplicaStatus.SlaveIORunning == "Yes" {
		redo = true
		p.log.Info("stop replica IO thread", "instance", ss.Primary)
		if err := op.StopReplicaIOThread(ctx); err != nil {
			return false, err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pst.ReplicaStatus != nil && pst.ReplicaStatus.RetrievedGtidSet != "" {
		redo = true
		p.log.Info("waiting for all retrieved transactions to be executed", "instance", ss.Primary, "gtid", pst.ReplicaStatus.RetrievedGtidSet)
		if err := op.WaitForGTID(ctx, pst.ReplicaStatus.RetrievedGtidSet, 0); err != nil {
			return false, err
		}
	}

	if ss.Cluster.Spec.Replicas == 1 {
		return
	}

	waitFor := int(ss.Cluster.Spec.Replicas / 2)
	if !pst.GlobalVariables.SemiSyncMasterEnabled || pst.GlobalVariables.WaitForSlaveCount != waitFor {
		redo = true
		p.log.Info("enable semi-sync primary")
		if err := op.ConfigurePrimary(ctx, waitFor); err != nil {
			return false, err
		}
	}
	return
}

func (p *managerProcess) configureReplica(ctx context.Context, ss *StatusSet, index int) (redo bool, e error) {
	st := ss.MySQLStatus[index]
	op := ss.DBOps[index]

	// for an errant replica, stop replication
	if st.IsErrant {
		if st.ReplicaStatus == nil {
			return
		}
		if st.ReplicaStatus.SlaveIORunning != "Yes" {
			return
		}
		p.log.Info("stop replica IO thread", "instance", index)
		if err := op.StopReplicaIOThread(ctx); err != nil {
			return false, err
		}
		return true, nil
	}

	if !st.GlobalVariables.SuperReadOnly {
		redo = true
		p.log.Info("set super_read_only=1", "instance", index)
		if err := op.SetReadOnly(ctx, true); err != nil {
			return false, err
		}
	}

	// clone and start replication for all non-errant replicas
	if st.GlobalVariables.ExecutedGTID == "" && ss.ExecutedGTID != "" && st.ReplicaStatus == nil {
		addr := ss.Pods[ss.Primary].Status.PodIP
		if addr == "0.0.0.0" {
			addr = ss.Cluster.PodHostname(ss.Primary)
		}
		if addr == "" {
			return false, fmt.Errorf("pod %s has not been assigned an IP address", ss.Pods[ss.Primary].Name)
		}

		redo = true
		req := &agent.CloneRequest{
			Host:         addr,
			Port:         constants.MySQLAdminPort,
			User:         constants.CloneDonorUser,
			Password:     ss.Password.Donor(),
			InitUser:     constants.AdminUser,
			InitPassword: ss.Password.Admin(),
		}

		ag, err := p.agentf.New(ctx, ss.Cluster, index)
		if err != nil {
			return false, fmt.Errorf("failed to connect moco-agent of instance %d: %w", index, err)
		}
		defer ag.Close()

		p.log.Info("begin cloning data", "instance", index)
		if _, err := ag.Clone(ctx, req); err != nil {
			event.CloneFailed.Emit(ss.Cluster, p.recorder, index, err)
			p.log.Error(err, "clone failed", "instance", index)
			return false, fmt.Errorf("failed to clone data on instance %d: %w", index, err)
		}
		event.CloneSucceeded.Emit(ss.Cluster, p.recorder, index)
		p.log.Info("clone succeeded", "instance", index)

		// wait until the instance restarts after clone
		time.Sleep(waitForRestartDuration)
		for i := 0; i < 60; i++ {
			select {
			case <-time.After(1 * time.Second):
			case <-ctx.Done():
				return false, ctx.Err()
			}

			_, err := op.GetStatus(ctx)
			if err == nil {
				break
			}
		}
	}

	ai := dbop.AccessInfo{
		Host:     ss.Cluster.PodHostname(ss.Primary),
		Port:     constants.MySQLPort,
		User:     constants.ReplicationUser,
		Password: ss.Password.Replicator(),
	}
	semisync := ss.Cluster.Spec.ReplicationSourceSecretName == nil
	if st.ReplicaStatus == nil || st.ReplicaStatus.SlaveIORunning != "Yes" || st.ReplicaStatus.MasterHost != ai.Host || st.GlobalVariables.SemiSyncSlaveEnabled != semisync {
		redo = true
		p.log.Info("start replication", "instance", index, "semisync", semisync)
		if err := op.ConfigureReplica(ctx, ai, semisync); err != nil {
			return false, err
		}
	}
	return
}
