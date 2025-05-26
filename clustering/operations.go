package clustering

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	agent "github.com/cybozu-go/moco-agent/proto"
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/event"
	"google.golang.org/protobuf/types/known/durationpb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	timeoutSeconds = 50
)

var (
	waitForCloneRestartDuration = 3 * time.Second
	waitForRoleChangeDuration   = 300 * time.Millisecond
)

func init() {
	intervalStr := os.Getenv("MOCO_CLONE_WAIT_DURATION")
	if intervalStr == "" {
		return
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return
	}
	waitForCloneRestartDuration = interval
}

func init() {
	intervalStr := os.Getenv("MOCO_ROLE_WAIT_DURATION")
	if intervalStr == "" {
		return
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return
	}
	waitForRoleChangeDuration = interval
}

func (p *managerProcess) isCloning(ctx context.Context, ss *StatusSet) bool {
	log := logFromContext(ctx)
	pst := ss.MySQLStatus[ss.Primary]
	if pst == nil {
		log.Info("the status of the primary is missing")
		return true
	}
	if pst.CloneStatus != nil && pst.CloneStatus.State.String != "Failed" {
		log.Info("cloning...", "state", pst.CloneStatus.State.String)
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

	log := logFromContext(ctx)
	log.Info("begin cloning data", "source", req.Host)
	if _, err := ag.Clone(ctx, req); err != nil {
		log.Error(err, "clone failed", "source", req.Host)
		return false, fmt.Errorf("failed to clone data from %s: %w", req.Host, err)
	}

	log.Info("clone succeeded", "source", req.Host)

	// wait until the instance restarts after clone
	op := ss.DBOps[ss.Primary]
	time.Sleep(waitForCloneRestartDuration)
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
	log := logFromContext(ctx)
	log.Info("begin switchover the primary", "current", ss.Primary, "next", ss.Candidate)

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

	err = ss.DBOps[ss.Candidate].WaitForGTID(ctx, pst.GlobalVariables.ExecutedGTID, timeoutSeconds)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &mocov1beta2.MySQLCluster{}
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
	log.Info("switchover finished", "primary", ss.Candidate)
	return nil
}

func (p *managerProcess) failover(ctx context.Context, ss *StatusSet) error {
	log := logFromContext(ctx)
	log.Info("begin failover the primary", "current", ss.Primary)

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

	candidate, err := dbop.FindTopRunner(ctx, op, candidates)
	if err != nil {
		return fmt.Errorf("failed to choose the next primary: %w", err)
	}
	ss.Candidate = candidate

	gtid := candidates[candidate].ReplicaStatus.RetrievedGtidSet
	log.Info("waiting for the new primary to execute all retrieved transactions", "index", candidate, "gtid", gtid)
	err = ss.DBOps[candidate].WaitForGTID(ctx, gtid, timeoutSeconds)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &mocov1beta2.MySQLCluster{}
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

	log.Info("failover finished", "primary", candidate)
	return nil
}

func (p *managerProcess) removeRoleLabel(ctx context.Context, ss *StatusSet) ([]int, error) {
	var noRoles []int
	for i, pod := range ss.Pods {
		v := pod.Labels[constants.LabelMocoRole]
		if v == "" {
			noRoles = append(noRoles, i)
			continue
		}

		if i == ss.Primary && v == constants.RolePrimary {
			continue
		}
		if i != ss.Primary && !isErrantReplica(ss, i) && v == constants.RoleReplica {
			continue
		}

		noRoles = append(noRoles, i)
		modified := pod.DeepCopy()
		delete(modified.Labels, constants.LabelMocoRole)
		if err := p.client.Patch(ctx, modified, client.MergeFrom(pod)); err != nil {
			return nil, fmt.Errorf("failed to remove %s label from %s/%s: %w", constants.LabelMocoRole, pod.Namespace, pod.Name, err)
		}
	}
	return noRoles, nil
}

// addRoleLabel adds the appropriate role label to the pods specified by the `alive` slice.
// The `alive` parameter is a slice of pod indices that are alive and eligible for role labeling.
func (p *managerProcess) addRoleLabel(ctx context.Context, ss *StatusSet, alive []int) error {
	for _, i := range alive {
		if isErrantReplica(ss, i) {
			continue
		}

		var newValue string
		if i == ss.Primary {
			newValue = constants.RolePrimary
		} else {
			newValue = constants.RoleReplica
		}

		pod := ss.Pods[i]
		modified := pod.DeepCopy()
		if modified.Labels == nil {
			modified.Labels = make(map[string]string)
		}
		modified.Labels[constants.LabelMocoRole] = newValue
		if err := p.client.Patch(ctx, modified, client.MergeFrom(pod)); err != nil {
			return fmt.Errorf("failed to add %s label to pod %s/%s: %w", constants.LabelMocoRole, pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

func (p *managerProcess) removeAnnPreventDelete(ctx context.Context, ss *StatusSet) error {
	log := logFromContext(ctx)
	for _, pod := range ss.Pods {
		if _, exists := pod.Annotations[constants.AnnPreventDelete]; exists {
			newPod := pod.DeepCopy()
			delete(newPod.Annotations, constants.AnnPreventDelete)
			log.Info("replication delay resolved, allow pod deletion", "pod", pod.Name)
			if err := p.client.Patch(ctx, newPod, client.MergeFrom(pod)); err != nil {
				return fmt.Errorf("failed to remove moco.cybozu.com/prevent-delete annotation: %w", err)
			}
		}
	}
	return nil
}

func (p *managerProcess) addAnnPreventDelete(ctx context.Context, ss *StatusSet) error {
	log := logFromContext(ctx)
	ppod := ss.Pods[ss.Primary]
	newPod := ppod.DeepCopy()
	if newPod.Annotations == nil {
		newPod.Annotations = make(map[string]string)
	}
	if _, exists := newPod.Annotations[constants.AnnPreventDelete]; !exists {
		newPod.Annotations[constants.AnnPreventDelete] = "true"
		log.Info("replication delay detected, prevent pod deletion", "pod", ppod.Name)
		if err := p.client.Patch(ctx, newPod, client.MergeFrom(ppod)); err != nil {
			return fmt.Errorf("failed to add moco.cybozu.com/prevent-delete annotation: %w", err)
		}
	}
	return nil
}

func (p *managerProcess) configure(ctx context.Context, ss *StatusSet) (bool, error) {
	redo := false

	// remove old role label from mysql pods whose role is changed
	// NOTE:
	//   I want to redo if even one pod is updated to refresh pod resources in StatusSet.
	//   But if some mysql instances are down, there is a wait of about 9 seconds at "(*managerProdess).GatherStatus()" after redo.
	//   The wait slows the recovery process, and downtime becomes longer. To prevent that, continue processing without redoing.
	noRoles, err := p.removeRoleLabel(ctx, ss)
	if err != nil {
		return false, err
	}

	// if the role of alive instances is changed, kill the connections on those instances
	var alive []int
	for _, i := range noRoles {
		if ss.MySQLStatus[i] == nil || isErrantReplica(ss, i) {
			continue
		}
		alive = append(alive, i)
	}
	if len(alive) > 0 {
		// I hope the backend pods of primary and replica services will be updated during this sleep.
		time.Sleep(waitForRoleChangeDuration)
	}
	for _, i := range alive {
		if err := ss.DBOps[i].KillConnections(ctx); err != nil {
			return false, fmt.Errorf("failed to kill connections in instance %d: %w", i, err)
		}
	}

	// configure primary instance
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

	// configure replica instances
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

	// add new role label
	err = p.addRoleLabel(ctx, ss, alive)
	if err != nil {
		return false, err
	}

	// make the primary writable if it is not an intermediate primary
	if ss.Cluster.Spec.ReplicationSourceSecretName == nil {
		pst := ss.MySQLStatus[ss.Primary]
		op := ss.DBOps[ss.Primary]
		if pst.GlobalVariables.ReadOnly {
			redo = true
			logFromContext(ctx).Info("set read_only=0", "instance", ss.Primary)
			if err := op.SetReadOnly(ctx, false); err != nil {
				return false, fmt.Errorf("failed to make the primary writable: %w", err)
			}
			event.SetWritable.Emit(ss.Cluster, p.recorder)
		}
	}
	return redo, nil
}

func (p *managerProcess) configureIntermediatePrimary(ctx context.Context, ss *StatusSet) (redo bool, e error) {
	log := logFromContext(ctx)
	pst := ss.MySQLStatus[ss.Primary]
	op := ss.DBOps[ss.Primary]
	if !pst.GlobalVariables.SuperReadOnly {
		redo = true
		log.Info("set super_read_only=1", "instance", ss.Primary)
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
	if pst.ReplicaStatus == nil || pst.ReplicaStatus.ReplicaIORunning != "Yes" || pst.ReplicaStatus.SourceHost != ai.Host {
		redo = true
		log.Info("start replication", "instance", ss.Primary, "semisync", false)
		if err := op.ConfigureReplica(ctx, ai, false); err != nil {
			return false, err
		}
	}
	return
}

func (p *managerProcess) configurePrimary(ctx context.Context, ss *StatusSet) (redo bool, e error) {
	log := logFromContext(ctx)
	pst := ss.MySQLStatus[ss.Primary]
	op := ss.DBOps[ss.Primary]

	// wait for all retrieved transactions to be executed if this used to be an intermediate replica
	if pst.ReplicaStatus != nil && pst.ReplicaStatus.ReplicaIORunning == "Yes" {
		redo = true
		log.Info("stop replica IO thread", "instance", ss.Primary)
		if err := op.StopReplicaIOThread(ctx); err != nil {
			return false, err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pst.ReplicaStatus != nil && pst.ReplicaStatus.RetrievedGtidSet != "" {
		redo = true
		log.Info("waiting for all retrieved transactions to be executed", "instance", ss.Primary, "gtid", pst.ReplicaStatus.RetrievedGtidSet)
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
		log.Info("enable semi-sync primary")
		if err := op.ConfigurePrimary(ctx, waitFor); err != nil {
			return false, err
		}
	}
	return
}

func (p *managerProcess) configureReplica(ctx context.Context, ss *StatusSet, index int) (redo bool, e error) {
	log := logFromContext(ctx)
	st := ss.MySQLStatus[index]
	op := ss.DBOps[index]

	// for an errant replica, stop replication
	if st.IsErrant {
		if st.ReplicaStatus == nil {
			return
		}
		if st.ReplicaStatus.ReplicaIORunning != "Yes" {
			return
		}
		log.Info("stop replica IO thread due to an errant transaction", "instance", index)
		if err := op.StopReplicaIOThread(ctx); err != nil {
			return false, err
		}
		return true, nil
	}

	if !st.GlobalVariables.SuperReadOnly {
		redo = true

		// When a primary is demoted due to network failure, old connections via the primary service may remain.
		// In rare cases, the old connections running write events block `set super_read_only=1`.
		if err := op.KillConnections(ctx); err != nil {
			return false, fmt.Errorf("failed to kill connections in instance %d: %w", index, err)
		}

		log.Info("set super_read_only=1", "instance", index)
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

		log.Info("begin cloning data", "instance", index)
		if _, err := ag.Clone(ctx, req); err != nil {
			event.CloneFailed.Emit(ss.Cluster, p.recorder, index, err)
			log.Error(err, "clone failed", "instance", index)
			return false, fmt.Errorf("failed to clone data on instance %d: %w", index, err)
		}
		event.CloneSucceeded.Emit(ss.Cluster, p.recorder, index)
		log.Info("clone succeeded", "instance", index)

		// wait until the instance restarts after clone
		time.Sleep(waitForCloneRestartDuration)
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

	// if the binlog required by the replica instance is not existing in the new primary instance, some data may be missing when `CHANGE MASTER TO' is executed.
	// use SubtractGTID to ensure no data is missing when switching.
	if sub, err := op.SubtractGTID(ctx, ss.MySQLStatus[ss.Primary].GlobalVariables.PurgedGTID, ss.MySQLStatus[index].GlobalVariables.ExecutedGTID); err != nil {
		return false, err
	} else if sub != "" {
		return false, fmt.Errorf("new primary %d does not have binlog containing transactions %s required for instance %d", ss.Primary, sub, index)
	}

	ai := dbop.AccessInfo{
		Host:     ss.Cluster.PodHostname(ss.Primary),
		Port:     constants.MySQLPort,
		User:     constants.ReplicationUser,
		Password: ss.Password.Replicator(),
	}
	semisync := ss.Cluster.Spec.ReplicationSourceSecretName == nil
	if st.ReplicaStatus == nil || st.ReplicaStatus.ReplicaIORunning != "Yes" || st.ReplicaStatus.SourceHost != ai.Host || st.GlobalVariables.SemiSyncSlaveEnabled != semisync {
		redo = true
		log.Info("start replication", "instance", index, "semisync", semisync)
		if err := op.ConfigureReplica(ctx, ai, semisync); err != nil {
			return false, err
		}
	}
	return
}
