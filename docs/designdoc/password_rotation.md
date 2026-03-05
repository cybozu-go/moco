# System User Password Rotation

## Background

MOCO manages 8 system MySQL users (`moco-admin`, `moco-agent`, `moco-repl`, `moco-clone-donor`, `moco-exporter`, `moco-backup`, `moco-readonly`, `moco-writable`).
Their passwords are generated at cluster creation, stored in a controller-managed Secret in the system namespace, and distributed to per-namespace Secrets.
Once generated, these passwords never change.

If a credential leak occurs, the only recovery option today is recreating the cluster.
This design introduces an in-place password rotation mechanism that avoids downtime.

## Goals and Non-goals

**Goals:**

- Rotate all 8 system user passwords without MySQL downtime
- Idempotent and crash-safe (controller restart resumes correctly)
- Prevent accidental propagation of ALTER USER to cross-cluster replicas
- Operator-initiated via `kubectl moco` (no new CRD required)
- Documented manual recovery for every failure mode

**Non-goals:**

- Automatic periodic rotation (build externally with a CronJob)
- Per-user rotation (all 8 users rotate together)
- End-user credential management

## Overview

Password rotation is a **two-step process** — **rotate** then **discard** — using MySQL's dual password feature (8.0.14+).
The operator explicitly triggers each step, with a verification window in between.

```mermaid
---
config:
  flowchart:
    useMaxWidth: false
---
flowchart TD
    idle1["Phase: Idle"]

    subgraph rotate_op ["Rotate"]
        direction TB
        p1_phase1["Phase → Rotating<br/>(controller: generate pending passwords)"]
        p1_retain["Phase → Retained<br/>(clusterManager: ALTER USER RETAIN<br/>all instances, sql_log_bin=0)"]
        p1_dist["Distribute to per-namespace Secrets"]
        p1_phase2["Phase → Rotated<br/>(controller)"]
        p1_restart["Rolling restart"]
        p1_phase1 --> p1_retain --> p1_dist --> p1_phase2 --> p1_restart
    end

    verify{{"Operator verifies applications<br/>work with new credentials"}}

    subgraph discard_op ["Discard"]
        direction TB
        p2_gate["Wait for rollout completion"]
        p2_phase_disc["Phase → Discarding<br/>(controller)"]
        p2_discard["Phase → Discarded<br/>(clusterManager: DISCARD OLD PASSWORD<br/>+ auth plugin migration<br/>all instances, sql_log_bin=0)"]
        p2_confirm["Promote pending → current in Secret"]
        p2_idle["Phase → Idle<br/>(controller)"]
        p2_gate --> p2_phase_disc --> p2_discard --> p2_confirm --> p2_idle
    end

    idle1 -- "kubectl moco credential rotate" --> rotate_op
    rotate_op --> verify
    verify -- "kubectl moco credential discard" --> discard_op

    style verify fill:#fff3cd,stroke:#ffc107
    style idle1 fill:#e8f5e9,stroke:#4caf50
    style p1_phase1 fill:#e3f2fd,stroke:#1976d2
    style p1_retain fill:#fce4ec,stroke:#c62828
    style p1_phase2 fill:#e3f2fd,stroke:#1976d2
    style p2_phase_disc fill:#e3f2fd,stroke:#1976d2
    style p2_discard fill:#fce4ec,stroke:#c62828
    style p2_idle fill:#e8f5e9,stroke:#4caf50
```

After the rotate operation, both old and new passwords are valid (MySQL dual password).
The operator can verify that applications work with the new credentials before committing to discard the old ones.

## User Interface

```console
# Rotate (generate new passwords and apply RETAIN)
$ kubectl moco credential rotate <cluster-name>

# Discard (after verifying applications work)
$ kubectl moco credential discard <cluster-name>
```

Each command sets an annotation on the MySQLCluster resource that the controller consumes:

| Command | Annotation | Value |
|---------|-----------|-------|
| `rotate` | `moco.cybozu.com/password-rotate` | `<rotationID>` (new UUID) |
| `discard` | `moco.cybozu.com/password-discard` | `<rotationID>` (same UUID from rotate) |

Annotations can also be set directly via `kubectl annotate` for automation.
The CLI is a convenience wrapper that adds validation and user-friendly error messages.

## Key Design Decisions

### Why MySQL Dual Password?

MySQL 8.0.14+ allows a user to have two valid passwords simultaneously:

```sql
-- After this, both old and new passwords are accepted
ALTER USER 'user'@'%' IDENTIFIED BY 'new' RETAIN CURRENT PASSWORD;

-- After this, only the new password is accepted
ALTER USER 'user'@'%' DISCARD OLD PASSWORD;
```

MySQL holds only one secondary password per user.
A second RETAIN overwrites the secondary slot, which is why we must prevent double-execution (see [Crash Safety](#crash-safety)).

### Why `sql_log_bin=0`?

MOCO supports cross-cluster replication.
ALTER USER is a DDL written to the binary log.
If propagated, a downstream cluster would receive the upstream's passwords, breaking its own credentials.

All ALTER USER operations use `SET sql_log_bin=0` (session-scoped, via a dedicated `db.Conn`) to suppress binlog writes.
As a consequence, within-cluster replicas also do not receive the change via replication, so ALTER USER must be executed on **every instance individually**.

### Why Auth Plugin Migration Is in the Discard Operation?

MySQL Error 3894 prevents changing the authentication plugin in a `RETAIN CURRENT PASSWORD` statement.
Instead, plugin migration happens after DISCARD using `ALTER USER ... IDENTIFIED WITH <plugin> BY ...`.

The target plugin is determined from `@@global.authentication_policy` on the primary instance:
- If the first element is a concrete plugin name (e.g. `caching_sha2_password`): use it
- If the first element is `*` or empty: default to `caching_sha2_password`

This enables transparent migration from legacy plugins like `mysql_native_password` during rotation.

### Responsibility Split: Controller vs ClusterManager

The controller handles **K8s resource operations** (Phase transitions, Secret management, annotation handling, rollout checks).
The clusterManager handles **DB operations** (ALTER USER RETAIN, DISCARD OLD PASSWORD, auth plugin migration, dual password pre-checks).

This follows the existing separation in MOCO where the controller manages K8s objects and the clusterManager manages MySQL state.
The phase flow ensures clear handoffs:

```
Idle → Rotating → Retained → Rotated → Discarding → Discarded → Idle
 ↑controller  ↑clusterMgr  ↑controller             ↑clusterMgr  ↑controller
```

## Internal Phase Tracking

The progress is tracked in `.status.systemUserRotation`:

```go
type SystemUserRotationStatus struct {
    RotationID     string        // UUID of the current rotation cycle
    Phase          RotationPhase // Idle, Rotating, Retained, Rotated, Discarding, Discarded
    LastRotationID string        // UUID of the last completed cycle (for stale detection)
}
```

## Rotate

### Controller: Idle → Rotating

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Set Phase to Rotating with the rotationID from the annotation | Status.Patch | controller |
| 2 | Generate pending passwords (e.g. `ADMIN_PASSWORD_PENDING`) in the source Secret | Secret.Update | controller |

### ClusterManager: Rotating → Retained

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Pre-check: scan all instances for pre-existing dual passwords. Wait if found. | - | clusterManager |
| 2 | For each instance: execute `ALTER USER ... RETAIN CURRENT PASSWORD` with `sql_log_bin=0` | MySQL | clusterManager |
| 3 | Set Phase to Retained | Status.Update | clusterManager |

**Instance loop (Step 2):**
Instances are processed sequentially (ordinal 0, 1, 2, ...).
For each instance:

1. Connect using the current (old) password
2. For replicas: temporarily disable `super_read_only`
3. For each user: check `HasDualPassword`. If the user already has a dual password (from a previous partial run), skip RETAIN; otherwise execute RETAIN.
4. For replicas: re-enable `super_read_only` (best-effort; the clustering loop recovers)

If any instance is unreachable, the clusterManager returns an error and retries on the next cycle.

### Controller: Retained → Rotated

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Distribute pending passwords to per-namespace Secrets | Secret.Apply | controller |
| 2 | Set Phase to Rotated; add restart annotation to StatefulSet template | Status.Patch, StatefulSet | controller |
| 3 | Rolling restart (Pods pick up new passwords via `EnvFrom`) | - | StatefulSet controller |
| 4 | Remove the `password-rotate` annotation (best-effort) | Patch | controller |

**Scaled-down clusters (replicas=0):**
Rotation is refused with a Warning Event (`RotateRefused`).
Without running instances, ALTER USER cannot execute, and distributing new passwords would break connectivity when the cluster scales back up.

## Discard

### Controller: Rotated → Discarding

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Validate that Phase is Rotated and pending passwords exist in the source Secret | - | controller |
| 2 | Wait for StatefulSet rollout to complete | - | controller |
| 3 | Set Phase to Discarding | Status.Patch | controller |

### ClusterManager: Discarding → Discarded

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Determine target auth plugin via `GetAuthPlugin` on the primary | MySQL (read-only) | clusterManager |
| 2 | For each instance: execute `DISCARD OLD PASSWORD` and auth plugin migration with `sql_log_bin=0` | MySQL | clusterManager |
| 3 | Set Phase to Discarded | Status.Update | clusterManager |

### Controller: Discarded → Idle

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Promote pending passwords to current in the source Secret | Secret.Update | controller |
| 2 | Reset status to Idle and save LastRotationID | Status.Patch | controller |
| 3 | Remove both annotations (best-effort) | Patch | controller |

**Why wait for the rollout (Rotated → Discarding)?**
The agent sidecar reads passwords from `EnvFrom`, evaluated only at Pod startup.
If old Pods are still running when DISCARD executes, they lose connectivity.
The gate checks: ObservedGeneration, CurrentRevision == UpdateRevision, UpdatedReplicas == Replicas, ReadyReplicas == Replicas.
While waiting, the handler returns `RequeueAfter: 15s` (not an error) to avoid log spam.

**Why connect with the pending password?**
DISCARD removes the old password.
If we connected with the old password, the connection would become invalid immediately after DISCARD succeeds.
Using the pending password also implicitly verifies that distribution was successful.

**Scaled-down clusters (replicas=0):**
Discard is rejected with a Warning Event (`DiscardRefused`) and `RequeueAfter: 15s`.
The operator should scale the cluster up first.

## Crash Safety

### Phase Boundary Safety

| Crash Point | Recovery |
|---|---|
| Phase=Rotating set, pending passwords not yet generated | controller re-reconcile generates them |
| Pending passwords generated, clusterManager RETAIN not started | clusterManager picks up Phase=Rotating on next cycle |
| RETAIN partially applied (some instances only) | HasDualPassword makes re-execution idempotent (skip already-retained users) |
| RETAIN all complete, Phase=Retained not yet set | clusterManager re-runs → all skip → sets Retained |
| Phase=Retained set, Secrets not yet distributed | controller re-reconcile distributes |
| Phase=Discarding set, DISCARD not yet executed | clusterManager picks up Phase=Discarding on next cycle |
| DISCARD all complete, Phase=Discarded not yet set | DISCARD is idempotent → re-execution → sets Discarded |
| Phase=Discarded set, Secret not yet confirmed | ConfirmPendingPasswords is idempotent |

### HasDualPassword Instead of Per-User Status Tracking

MySQL holds only one secondary password per user.
If RETAIN is re-run with the same pending password after a crash, the pending password (now the primary) moves into the secondary slot, evicting the original password.
The controller can no longer connect.

Instead of tracking per-user progress in Kubernetes status (which can fail independently of MySQL), the clusterManager queries MySQL directly: if `mysql.user.User_attributes` contains `additional_password`, RETAIN is skipped.
This makes MySQL the source of truth and is safe on re-execution because the query is read-only.

### Idempotency of DISCARD

`DISCARD OLD PASSWORD` is idempotent in MySQL (no-op when there is no secondary password), so per-user tracking is not needed.

### ConfirmPendingPasswords Idempotency

If the controller crashes after updating the Secret but before patching the status, the Secret has already been promoted (pending passwords are now current) but the Phase is still Discarded.
On re-reconcile, `ConfirmPendingPasswords` finds no pending keys remaining, so it returns nil without making changes.

### Status.Update Conflict Handling

The clusterManager uses `retry.RetryOnConflict` with `Status().Update()` for Phase transitions.
The rotation handler runs within `do()` after `updateStatus()`.
Rotation Phase updates use an independent `retry.RetryOnConflict` block that re-reads the cluster before updating, so conflicts with `updateStatus` are safely retried.

### Stale Annotation Detection

After a completed cycle, the best-effort annotation removal may fail.
Without detection, the next reconcile would start a spurious new rotation.

The controller compares the annotation's rotationID with `LastRotationID`:
- **Match**: the annotation is stale, so the controller removes it silently
- **Mismatch**: the annotation is a fresh trigger, so the controller proceeds with rotation

### Precondition-Not-Met Handling for Discard

If `password-discard` is set but Phase != Rotated:
1. The annotation is consumed (removed)
2. A Warning Event (`DiscardSkipped`) is emitted
3. The user can re-apply after completing the rotate operation

## Interaction with Other Reconcile Steps

### reconcileV1Secret

`reconcileV1Secret` runs before password rotation in the reconcile loop.
During rotation (Phase != Idle), it returns early without distributing — the rotation handler owns distribution.
After discard completes and Phase resets to Idle, the source Secret already contains confirmed passwords, so normal distribution works correctly.

### GatherStatus and ClusterManager Connection

GatherStatus reads passwords from the user Secret (per-namespace).
During rotation phases, the user Secret always contains passwords that MySQL accepts:
- Phase=Rotating/Retained: user Secret has old passwords, MySQL accepts old via dual password
- Phase=Rotated onwards: controller has distributed new passwords to user Secret

The rotation handler reads passwords directly from the source (controller) Secret and creates its own DB connections, independent of GatherStatus.

### Rotate and Discard Never Run in the Same Reconcile

If both annotations are present, rotate runs first and returns immediately.
Discard is evaluated on the next reconcile, preserving the verification window.

## Source Secret Layout

During rotation, the source Secret holds both current and pending passwords:

```
ADMIN_PASSWORD:         <current>
AGENT_PASSWORD:         <current>
...
ADMIN_PASSWORD_PENDING: <new>       # only during rotation
AGENT_PASSWORD_PENDING: <new>       # only during rotation
...
ROTATION_ID:            <uuid>      # only during rotation
```

`HasPendingPasswords` validates that all 8 pending keys (e.g. `ADMIN_PASSWORD_PENDING`) and `ROTATION_ID` are present and that the rotation ID matches the expected value.
If some pending keys are missing or the rotation ID does not match, the function returns an error.

## Assumptions

No MOCO system user has a dual password when rotation starts.
The clusterManager checks this at Phase=Rotating using `HasDualPassword` across all instances and all users.
If a stale dual password is found, the clusterManager waits (emitting a `DualPasswordExists` Warning Event) instead of proceeding.
See [Recovery: Dual Password Exists While Phase=Idle](#dual-password-exists-while-phaseidle).

## Security Considerations

- `RotateUserPassword`, `DiscardOldPassword`, and `MigrateUserAuthPlugin` interpolate user names directly into SQL (MySQL does not support placeholders for `ALTER USER`). User names are always from the fixed constants in `pkg/constants/users.go`.
- `MigrateUserAuthPlugin` interpolates the plugin name into `IDENTIFIED WITH`. The value is validated against `^[a-zA-Z0-9_]+$` and derived from `@@global.authentication_policy`, never from user input.
- All ALTER USER operations use `SET sql_log_bin=0` via a dedicated `db.Conn` to prevent cross-cluster propagation.

## Recovery Procedures

All recovery procedures follow the same principle: **reset MySQL passwords back to the current (old) values known to the controller**.
The key insight is that `ALTER USER ... IDENTIFIED BY` (without RETAIN) sets the primary password and clears any secondary, restoring MySQL to a clean single-password state.

### Common Recovery Steps

The three failure scenarios below share common recovery steps. The differences are noted in each section.

<a id="reset-mysql-passwords"></a>

#### How to Reset MySQL Passwords

Retrieve the current passwords from the source Secret:

```console
$ kubectl -n <system-namespace> get secret <controller-secret-name> \
    -o jsonpath='{.data.ADMIN_PASSWORD}' | base64 -d
# Repeat for: AGENT_PASSWORD, REPLICATION_PASSWORD, CLONE_DONOR_PASSWORD,
# EXPORTER_PASSWORD, BACKUP_PASSWORD, READONLY_PASSWORD, WRITABLE_PASSWORD
```

Identify which instance is the primary:

```console
$ kubectl -n <namespace> exec <pod> -c mysqld -- \
    mysql -u moco-admin -p<admin-password> \
    -e "SELECT @@read_only, @@super_read_only;"
# primary: read_only=0, super_read_only=0
# replica: read_only=1, super_read_only=1
```

Execute on the **primary**:

```console
$ kubectl -n <namespace> exec <primary-pod> -c mysqld -- \
    mysql -u moco-admin -p<admin-password> -e "
  SET SESSION sql_log_bin=0;
  ALTER USER 'moco-admin'@'%' IDENTIFIED BY '<admin-password>';
  ALTER USER 'moco-agent'@'%' IDENTIFIED BY '<agent-password>';
  ALTER USER 'moco-repl'@'%' IDENTIFIED BY '<repl-password>';
  ALTER USER 'moco-clone-donor'@'%' IDENTIFIED BY '<clone-donor-password>';
  ALTER USER 'moco-exporter'@'%' IDENTIFIED BY '<exporter-password>';
  ALTER USER 'moco-backup'@'%' IDENTIFIED BY '<backup-password>';
  ALTER USER 'moco-readonly'@'%' IDENTIFIED BY '<readonly-password>';
  ALTER USER 'moco-writable'@'%' IDENTIFIED BY '<writable-password>';
"
```

Execute on **each replica** (includes `super_read_only` handling):

```console
$ kubectl -n <namespace> exec <replica-pod> -c mysqld -- \
    mysql -u moco-admin -p<admin-password> -e "
  SET SESSION sql_log_bin=0;
  SET GLOBAL super_read_only=OFF;
  ALTER USER 'moco-admin'@'%' IDENTIFIED BY '<admin-password>';
  ALTER USER 'moco-agent'@'%' IDENTIFIED BY '<agent-password>';
  ALTER USER 'moco-repl'@'%' IDENTIFIED BY '<repl-password>';
  ALTER USER 'moco-clone-donor'@'%' IDENTIFIED BY '<clone-donor-password>';
  ALTER USER 'moco-exporter'@'%' IDENTIFIED BY '<exporter-password>';
  ALTER USER 'moco-backup'@'%' IDENTIFIED BY '<backup-password>';
  ALTER USER 'moco-readonly'@'%' IDENTIFIED BY '<readonly-password>';
  ALTER USER 'moco-writable'@'%' IDENTIFIED BY '<writable-password>';
  SET GLOBAL super_read_only=ON;
"
```

> `sql_log_bin=0` must be set before disabling `super_read_only` to prevent intermediate writes from being logged.
> MOCO's clustering loop will re-enable `super_read_only` automatically if the manual re-enable fails.

---

### Stale Pending Passwords (RotationID Mismatch)

**Symptom:** Warning Event `RotationPendingError`

**Cause:** A previous rotation was interrupted, leaving pending password keys (e.g. `ADMIN_PASSWORD_PENDING`) and `ROTATION_ID` from a different rotation cycle in the source Secret.
This typically happens when the status was manually reset to Idle while the Secret still contains pending data.

**Why this needs MySQL cleanup:**
The interrupted rotation may have partially executed RETAIN on some instances.
Without cleanup, a new rotation would see `HasDualPassword=true` on those instances, skip RETAIN, and leave them with stale passwords — causing connectivity loss after DISCARD.

**Recovery order: status → Secret → rollout → MySQL**

This ordering is critical. At this point, some Pods may be running with new (pending) passwords.
MySQL still accepts both via dual password.
If MySQL were reset first, Pods with new passwords would immediately lose connectivity.
By resetting status and Secret first, Pods are rolled back to old passwords before MySQL clears the dual-password state.

```console
# Step 1: Reset rotation status to Idle.
# This unblocks reconcileV1Secret to re-distribute old passwords
# and triggers a rolling restart via StatefulSet template update.
$ kubectl edit mysqlcluster <name>
# Set in status.systemUserRotation:
#   phase: ""
#   rotationID: ""
# Leave lastRotationID unchanged.

# Step 2: Clean the source Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all pending password keys (e.g. ADMIN_PASSWORD_PENDING) and ROTATION_ID

# Step 3: Wait for all Pods to restart with old passwords.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 4: Reset MySQL passwords on all instances.
# See "How to Reset MySQL Passwords" above.
```

### Missing Pending Passwords During Discard

**Symptom:** Warning Event `MissingRotationPending`

**Cause:** The source Secret lost its pending keys (manual edit, restore from backup, etc.) while status still shows Rotated.

**Why this is dangerous:**
At Phase=Rotated, all instances hold dual passwords and Pods may be using the pending passwords.
The pending passwords are irrecoverable (MySQL stores only hashes).
Without recovery, dual-password state persists indefinitely and blocks future rotations.

**Recovery:** Same as stale pending passwords — reset status → Secret → rollout → MySQL.

```console
# Step 1: Reset rotation status to Idle.
$ kubectl edit mysqlcluster <name>
# Set in status.systemUserRotation:
#   phase: ""
#   rotationID: ""
# Leave lastRotationID unchanged.

# Step 2: Clean any remaining pending keys from the source Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete any remaining pending password keys (e.g. ADMIN_PASSWORD_PENDING) and ROTATION_ID

# Step 3: Wait for all Pods to restart with old passwords.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 4: Reset MySQL passwords on all instances.
# See "How to Reset MySQL Passwords" above.
```

### Dual Password Exists While Phase=Idle

**Symptom:** Warning Event `DualPasswordExists`

**Cause:** A MOCO system user has `additional_password` set while rotation status is Idle.
This happens when a previous recovery didn't fully clear MySQL's dual-password state, or someone ran `ALTER USER ... RETAIN CURRENT PASSWORD` manually.

**Why DISCARD OLD PASSWORD must not be used:**
After RETAIN, the primary is the new (potentially unknown) password and the secondary is the old (known) password.
DISCARD removes the secondary, leaving only the unknown primary — breaking connectivity.

**Recovery:** No status or Secret reset needed (already Idle, no pending keys).

```console
# Step 1 (recommended): Verify Pods can connect with current credentials.

# Step 2 (recommended): Wait for any in-progress rollout.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 3: Reset MySQL passwords on all instances.
# See "How to Reset MySQL Passwords" above.

# After recovery, retry: kubectl moco credential rotate <name>
```
