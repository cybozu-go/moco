package clustering

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/event"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InstanceAuthPluginStatus is the authentication plugin state of the MOCO
// users observed on one instance.
type InstanceAuthPluginStatus struct {
	// UserPlugins holds the authentication plugin of each MOCO user in
	// `mysql.user`, keyed by user name.
	UserPlugins map[string]string

	// TargetPluginActive reports whether the plugin specified in
	// `spec.systemUserAuthPlugin` is ACTIVE on the instance. It is checked
	// only when the instance has users to migrate; it is true otherwise.
	TargetPluginActive bool
}

// convergeAuthPlugin converges the authentication plugin of the MOCO system
// users to `spec.systemUserAuthPlugin` on every instance, keeping
// the password values unchanged.
//
// This is called only in StateHealthy. Errors are not fatal; the caller just
// logs them and the convergence is retried on the next tick.
func (p *managerProcess) convergeAuthPlugin(ctx context.Context, ss *StatusSet) error {
	if ss.Cluster.Spec.SystemUserAuthPlugin == nil {
		return nil
	}
	target := ss.Cluster.Spec.SystemUserAuthPlugin.String()
	log := logFromContext(ctx)

	for index, st := range ss.AuthPluginStatus {
		if st == nil {
			// The state could not be collected; handled on a later tick.
			continue
		}
		users := usersWithPluginMismatch(st.UserPlugins, target)
		if len(users) == 0 {
			continue
		}
		if !st.TargetPluginActive {
			// The SystemUserAuthPluginSynced condition reports this state
			// with the PluginUnavailable reason.
			log.Info("the target authentication plugin is not active on the instance; skipping", "instance", index, "plugin", target)
			continue
		}
		if err := p.convergeInstanceAuthPlugin(ctx, ss, index, target, users); err != nil {
			event.AuthPluginChangeFailed.Emit(ss.Cluster, p.recorder, index, err)
			return fmt.Errorf("failed to change the authentication plugin on instance %d: %w", index, err)
		}
		log.Info("changed the authentication plugin of MOCO users", "instance", index, "plugin", target, "users", users)
		event.AuthPluginChanged.Emit(ss.Cluster, p.recorder, index, target)
	}
	return nil
}

func (p *managerProcess) convergeInstanceAuthPlugin(ctx context.Context, ss *StatusSet, index int, target string, users []string) (e error) {
	op := ss.DBOps[index]

	// Replicas always run with super_read_only. So does the primary when the
	// cluster replicates from an external source (intermediate primary).
	// Clear only super_read_only and restore it; read_only stays on, so
	// accounts without CONNECTION_ADMIN/SUPER still cannot write.
	if index != ss.Primary || ss.Cluster.Spec.ReplicationSourceSecretName != nil {
		if err := op.SetSuperReadOnly(ctx, false); err != nil {
			return fmt.Errorf("failed to clear super_read_only: %w", err)
		}
		defer func() {
			if err := op.SetSuperReadOnly(ctx, true); err != nil {
				e = errors.Join(e, fmt.Errorf("failed to restore super_read_only: %w", err))
			}
		}()
	}

	for _, user := range users {
		if err := op.AlterUserAuthPlugin(ctx, user, target); err != nil {
			return err
		}
	}
	return nil
}

// usersWithPluginMismatch returns the users in userPlugins whose
// authentication plugin differs from targetPlugin. A MOCO user that does
// not exist in `mysql.user` never appears in userPlugins, so it is never
// counted; ensuring that the users exist is moco-agent's responsibility.
func usersWithPluginMismatch(userPlugins map[string]string, targetPlugin string) []string {
	var users []string
	for u, p := range userPlugins {
		if p != targetPlugin {
			users = append(users, u)
		}
	}
	sort.Strings(users)
	return users
}

// authPluginCondition computes the SystemUserAuthPluginSynced condition from
// the observations in ss. It returns nil while `spec.systemUserAuthPlugin` is
// unset, which means the condition must be removed.
func authPluginCondition(ss *StatusSet) *metav1.Condition {
	if ss.Cluster.Spec.SystemUserAuthPlugin == nil {
		return nil
	}
	target := ss.Cluster.Spec.SystemUserAuthPlugin.String()

	var unknown, mismatched, unavailable int
	for _, st := range ss.AuthPluginStatus {
		if st == nil {
			unknown++
			continue
		}
		if len(usersWithPluginMismatch(st.UserPlugins, target)) == 0 {
			continue
		}
		if !st.TargetPluginActive {
			unavailable++
			continue
		}
		mismatched++
	}

	cond := metav1.Condition{
		Type:    mocov1beta2.ConditionSystemUserAuthPluginSynced,
		Status:  metav1.ConditionTrue,
		Reason:  mocov1beta2.ReasonAuthPluginSynced,
		Message: fmt.Sprintf("all MOCO users use %s", target),
	}
	switch {
	case unavailable > 0:
		cond.Status = metav1.ConditionFalse
		cond.Reason = mocov1beta2.ReasonAuthPluginUnavailable
		cond.Message = fmt.Sprintf("%s is not active on %d instance(s)", target, unavailable)
	case mismatched > 0:
		cond.Status = metav1.ConditionFalse
		cond.Reason = mocov1beta2.ReasonAuthPluginApplying
		cond.Message = fmt.Sprintf("%d instance(s) have users that do not use %s yet", mismatched, target)
	case unknown > 0:
		cond.Status = metav1.ConditionUnknown
		cond.Reason = mocov1beta2.ReasonAuthPluginInstanceUnreachable
		cond.Message = fmt.Sprintf("the plugins of %d instance(s) could not be collected", unknown)
	}
	return &cond
}

// updateAuthPluginStatus applies authPluginCondition to the cluster's status
// conditions and the metric.
func (p *managerProcess) updateAuthPluginStatus(ss *StatusSet, cluster *mocov1beta2.MySQLCluster) {
	cond := authPluginCondition(ss)
	if cond == nil {
		meta.RemoveStatusCondition(&cluster.Status.Conditions, mocov1beta2.ConditionSystemUserAuthPluginSynced)
		p.metrics.systemUserAuthPluginSynced.Set(math.NaN())
		return
	}
	meta.SetStatusCondition(&cluster.Status.Conditions, *cond)
	if cond.Status == metav1.ConditionTrue {
		p.metrics.systemUserAuthPluginSynced.Set(1)
	} else {
		p.metrics.systemUserAuthPluginSynced.Set(0)
	}
}
