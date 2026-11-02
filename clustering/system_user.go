package clustering

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco/pkg/event"
)

// configureSystemUserAuthenticationPlugin converges the authentication plugin
// of MOCO system users on every instance.  It is a no-op when the feature is
// not configured, and only runs for a healthy cluster.
func (p *managerProcess) configureSystemUserAuthenticationPlugin(ctx context.Context, ss *StatusSet) error {
	plugin := ss.Cluster.Spec.SystemUserAuthenticationPlugin
	if plugin == "" || ss.State != StateHealthy || ss.NeedSwitch {
		return nil
	}

	log := logFromContext(ctx)
	for i, op := range ss.DBOps {
		if op == nil {
			continue
		}

		changed, err := op.SetSystemUserAuthenticationPlugin(ctx, plugin)
		if err != nil {
			event.SystemUserAuthenticationPluginFailed.Emit(ss.Cluster, p.recorder, i, plugin, err)
			return fmt.Errorf("failed to change system user authentication plugin on instance %d: %w", i, err)
		}
		if len(changed) == 0 {
			log.V(1).Info("system user authentication plugin is already configured", "instance", i, "plugin", plugin)
			continue
		}

		log.Info("changed system user authentication plugin", "instance", i, "plugin", plugin, "users", changed)
		event.SystemUserAuthenticationPluginChanged.Emit(ss.Cluster, p.recorder, i, plugin, changed)
	}
	return nil
}
