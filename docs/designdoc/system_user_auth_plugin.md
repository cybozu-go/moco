# Declarative authentication plugin migration for MOCO system users

## Context

moco-agent creates the eight system users (`moco-admin` … `moco-writable`) at the first bootstrap of each mysqld instance, and `CREATE USER ... IDENTIFIED BY` picks up the server's default authentication plugin at that moment.
After creation, no path exists that changes the plugin.

Clusters created while the server default was `mysql_native_password` therefore keep native-plugin system users indefinitely.
`mysql_native_password` is disabled by default in MySQL 8.4 and removed in 9.x, so these users must be migrated to `caching_sha2_password` before upgrading.
Changing the server default (`authentication_policy`) does not help: it only affects newly created users, so a mechanism that ALTERs the existing users is needed regardless.
That mechanism needs a target plugin. The natural candidate — following the server default — would couple two decisions with different scopes: `authentication_policy` governs every new account, application accounts included, while this migration concerns only the MOCO-managed users.
The target is therefore a dedicated field on the MySQLCluster, which keeps the two independent, so the system users can be migrated before (or without) a server-wide policy change.
Being a per-cluster field, it also allows gradual rollout and rollback without a mysqld restart.

This design adds a declarative way to converge the authentication plugin of the MOCO system users without changing their passwords and without restarting any pod.
Accounts not managed by MOCO are out of scope.
Clusters that do not set the field behave exactly as before.

## Goals

- Migrate the plugin of the 8 system users while keeping password values unchanged — no Secret change, no pod restart.
- Work on every topology, including intermediate primaries (`spec.replicationSourceSecretName`) while external replication is running.
- Self-heal drift: plugins inherited from an external clone donor or restored from an old backup converge without operator action.

## Non-goals

- Rotating password values.
- Managing plugins of user-created accounts.
- Changing the server-wide default plugin; that remains a my.cnf decision.

## Design

```
MySQLCluster.spec.systemUserAuthPlugin   (new, optional, enum)
        │  nil: do nothing (current behavior)
        ▼
clustering manager periodic loop   (StateHealthy only)
        │  per instance: read plugins from mysql.user → users that differ
        │  per instance: skip unless the target plugin is ACTIVE
        ▼
per instance: ALTER USER ... IDENTIFIED WITH <plugin> BY <current password>
        (sql_log_bin=0; super_read_only instances: temporarily OFF → restore)
```

### API

`spec.systemUserAuthPlugin`: optional string, enum `caching_sha2_password` / `mysql_native_password`.
The enum is limited to these two values so that the API rejects plugins that MOCO's own components cannot authenticate with.
Unset means MOCO does not manage plugins (today's behavior).
The field is mutable in both directions, but moving back to `mysql_native_password` works only while the server still provides that plugin; on MySQL 9.x it no longer exists, so that rollback is impossible.

### Clustering

A new step in the clustering manager's per-cluster loop, executed only in `StateHealthy` — the migration is never urgent, and this keeps user DDL and `super_read_only` toggling out of clusters that are mid-recovery:

1. Return immediately unless the field is set (no extra queries for clusters that do not opt in).
2. For each instance, read the plugins of the 8 users from `mysql.user` and pick the users whose plugin differs from the spec. A user that does not exist at host `'%'` — for example while an external clone has replaced `mysql.user` and `InitExternal` has not yet recreated the users — is skipped and never ALTERed. Ensuring that the users exist is moco-agent's bootstrap responsibility, not this mechanism's.
3. If some users differ, check that the target plugin is ACTIVE on the instance (`information_schema.plugins`). If it is not — `mysql_native_password` is disabled by default on MySQL 8.4 and absent on 9.x — skip the instance without touching `super_read_only`. The `SystemUserAuthPluginSynced` condition reports this state with the `PluginUnavailable` reason. Without this check, an impossible target would make every tick toggle `super_read_only` on all replicas and emit a warning event, forever.
4. If the instance runs with `super_read_only` (every replica; the primary too when the cluster is an intermediate primary), lower only `super_read_only` and restore it with defer. `read_only=1` stays on, so accounts without `CONNECTION_ADMIN`/`SUPER` still cannot write, and the replication SQL thread is unaffected.
5. Execute `ALTER USER ... IDENTIFIED WITH <plugin> BY <current password>` on a dedicated connection with `SET sql_log_bin=0`. MySQL has no statement that changes only the plugin, so the same value is re-specified and the password does not change. Because the value is identical on every instance (one cluster-scoped Secret), a temporary plugin mix during convergence never breaks authentication for clients that support both plugins.

#### Failure handling

The step is idempotent.
Errors are non-fatal: logged, surfaced as events, retried on the next tick, and never allowed to block failover/switchover.
If the process crashes between lowering and restoring `super_read_only`, the cluster drops out of Healthy on the next tick and the existing `configure` path restores the flag.
The ACTIVE check above keeps the common permanent failure (plugin not available on the server) out of this retry loop entirely.
One corner case remains in the loop: `ALTER USER` can still fail when `authentication_policy` pins the first factor to a different plugin. This is a rare, self-inflicted configuration; it stays on the generic retry-and-event path.

### Code changes

- `api/v1beta2`: the new optional field with enum validation.
- `pkg/dbop`: `GetUserAuthPlugins`, `IsAuthPluginActive`, `AlterUserAuthPlugin` (dedicated connection, `sql_log_bin=0`), `SetSuperReadOnly` (the existing `SetReadOnly(false)` also runs STOP/RESET REPLICA and clears `read_only` — promotion semantics, unusable here).
- `clustering`: the convergence step and events for change / failure.
- User documentation: a section in `usage.md` and the metrics table in `metrics.md`.
- No moco-agent change.

### Observability

The migration has three questions to answer, each served by a different mechanism:

- "Is this cluster done, and if not, why?" — the `SystemUserAuthPluginSynced` condition, readable with `kubectl`.
  `False/Applying` is the transient in-progress state; `False/PluginUnavailable` and `Unknown/InstanceUnreachable` are the states that need operator attention.
  Note that `True/Synced` means "every MOCO user that exists at host `'%'` uses the specified plugin"; it does not certify that all 8 users exist. A missing user surfaces as an authentication failure of the component that uses it, not through this condition.
  The condition is removed while the field is unset, so the ~all clusters that never opt in carry no extra status.
- "How far along is the fleet, and which clusters are stuck?" — the per-cluster gauge `moco_cluster_system_user_auth_plugin_synced` (1/0, NaN while unmanaged — NaN matches no comparison, so unmanaged clusters drop out of fleet queries), following the existing `available`/`healthy` gauge pattern. Aggregation across many clusters (count of unsynced, alert on clusters stuck for too long) is a one-line PromQL query; conditions alone would need kube-state-metrics configuration on every consumer.
- "What changed, and when?" — an event per instance on each actual change (and on failure), plus controller logs that include the affected user names. Convergence completes within one tick of becoming Healthy, so no finer-grained progress tracking is needed; what persists and matters is the blocked state, which the condition reasons cover.

## Alternatives considered

- ALTER on the primary, propagate via binlog: simplest for normal clusters, but impossible on intermediate primaries (super_read_only, errant GTID) and blind to replica-only drift. It is also inconsistent with how `mysql.user` rows are managed in MOCO — they are created outside the binlog (before `RESET MASTER` at bootstrap, with `sql_log_bin=OFF` after an external clone). Rejected.
- Apply in moco-agent at startup: requires a rolling restart to trigger, and replicas would need `super_read_only` lifted from outside the clustering manager, which owns that flag. Rejected.
- Derive the target from `authentication_policy`: cannot migrate system users ahead of the server default (the main use case); it would also turn a my.cnf edit into an implicit trigger for ALTER USER, and depends on parsing the policy string. Rejected.
- One-shot trigger via annotation: a one-shot trigger cannot follow drift reintroduced later by restores and external clones. Rejected.

## Cross-cutting concerns

### Client compatibility

`moco-readonly` / `moco-writable` are provided for applications, so changing their plugin affects clients outside MOCO.
An incompatible client fails only at its next reconnect, possibly hours later, which detaches the failure from the configuration change.
MOCO has no way to verify that clients support the target plugin, so this prerequisite cannot be enforced mechanically.

### Interaction with a future password rotation

`ALTER USER ... IDENTIFIED WITH <plugin> BY <password>` re-specifies the password from the cluster's Secret.
"The password does not change" therefore holds as long as `mysql.user` and the Secret agree, which is always the case today.
A future password-rotation mechanism that makes the Secret and `mysql.user` diverge on purpose must take this into account: during such a divergence, a plugin convergence would also reset the password to the Secret value.
The rotation design has to either tolerate this or order the two operations within the clustering loop.

### Why migrating to caching_sha2_password needs no additional work

Replica IO threads reconnecting to a `caching_sha2_password` replication user over non-TLS connections need RSA key exchange.
`GET_MASTER_PUBLIC_KEY=1` / `GET_SOURCE_PUBLIC_KEY=1` has been part of every CHANGE MASTER statement MOCO issues since v1 and is persisted in the replica's connection metadata, so reconnection is safe on existing clusters.
moco-agent and the operator use go-sql-driver, which fetches the server public key automatically.
The existing e2e suite already runs every system user on `caching_sha2_password` (replication, clone, backup, exporter).

### Performance

- Field unset: zero additional queries.
- Field set and converged: one small SELECT per instance per tick.
- After migration, a reconnect storm (primary restart, failover) performs full authentication (RSA) until the server-side auth cache warms; heavier than mysql_native_password. Expected to be negligible, but worth watching on large clusters.
