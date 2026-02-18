# System User Password Rotation

## Context

MOCO creates 8 system MySQL users (moco-admin, moco-agent, moco-repl, moco-clone-donor, moco-exporter, moco-backup, moco-readonly, moco-writable) when a MySQLCluster is initialized.
Their passwords are stored in a controller-managed Secret in the system namespace and distributed to per-namespace Secrets.
Once generated, these passwords never change for the lifetime of the cluster.

If a credential leak occurs, recovery requires recreating the cluster (e.g. from a backup or via cross-cluster replication to a new cluster).
To allow in-place credential renewal without downtime, MOCO needs a password rotation mechanism.

## Goals

* Rotate all 8 system user passwords without MySQL downtime
* Idempotent and crash-safe: any controller restart during rotation must resume correctly
* Prevent double-execution of ALTER USER RETAIN (MySQL holds only one secondary password)
* Do not propagate ALTER USER to cross-cluster replicas via binlog
* Operator-initiated via `kubectl-moco` subcommand (or raw annotation); no new CRD required
* Manual recovery path documented for every failure mode

## Non-goals

* Automatic periodic rotation (can be built on top with an external CronJob)
* Rotation of individual users (all 8 rotate together)
* End-user credential management (this covers MOCO system users only)

## Assumptions

* No MOCO system user has a dual password (`ALTER USER ... RETAIN CURRENT PASSWORD`) when rotation starts. The controller uses `HasDualPassword` to detect whether RETAIN has already been applied during crash recovery. A pre-existing dual password (regardless of origin — manual operation, previous interrupted rotation, incomplete recovery, or bug) is indistinguishable from one set by the controller, causing RETAIN to be skipped and leaving the controller's new password unapplied in MySQL. To enforce this assumption, the controller runs a pre-check at rotation start (Phase=Idle) that scans all instances and all MOCO system users using `HasDualPassword`. If a stale dual password is found, the rotation is refused with a `DualPasswordExists` Warning Event and the Phase stays Idle. See [Dual password exists while Phase=Idle](#dual-password-exists-while-phaseidle) for the recovery procedure.

## Actual Design

### User Interface

Users trigger rotation via `kubectl-moco` subcommands:

```console
# Phase 1: Generate new passwords, ALTER USER RETAIN, distribute
$ kubectl moco credential rotate <name>

# Phase 2: DISCARD OLD PASSWORD, confirm source Secret
$ kubectl moco credential discard <name>
```

The `rotate` subcommand generates a unique rotationID (UUID) and sets it as the annotation value:

```
moco.cybozu.com/password-rotate: <rotationID>
```

The `discard` subcommand sets the same rotationID as the annotation value:

```
moco.cybozu.com/password-discard: <rotationID>
```

The rotationID must match the active rotation (stored in `status.systemUserRotation.rotationID`).
This prevents stale discard annotations from a previous rotation cycle from accidentally firing.

Annotations can also be set directly via `kubectl annotate` for automation use cases.
When setting annotations manually, the value must be the rotationID of the target rotation cycle.

The existing `kubectl moco credential` command is restructured as a parent command with subcommands:

| Subcommand | Description |
|------------|-------------|
| `credential show CLUSTER_NAME` | Fetch the credential of a specified user (previously `credential CLUSTER_NAME`) |
| `credential rotate CLUSTER_NAME` | Trigger Phase 1 (password rotation) |
| `credential discard CLUSTER_NAME` | Trigger Phase 2 (discard old passwords) |

The two-step design is intentional.
After Phase 1, both old and new passwords are valid (MySQL dual password).
The operator can verify that applications work with the new credentials before discarding the old ones.

### MySQL Dual Password

MySQL 8.0.14+ supports dual passwords via:

```sql
ALTER USER 'user'@'%' IDENTIFIED BY 'new_password' RETAIN CURRENT PASSWORD;
ALTER USER 'user'@'%' DISCARD OLD PASSWORD;
```

After `RETAIN`, both old and new passwords are accepted for authentication.
After `DISCARD`, only the new password is valid.
MySQL keeps only one secondary password per user; a second `RETAIN` with different credentials overwrites the secondary slot.

### Cross-Cluster Replication Safety

MOCO supports cross-cluster replication, where one MySQLCluster replicates from another.
ALTER USER is a DDL that is written to the binary log.
If it propagated via binlog, a downstream cluster would receive the upstream cluster's system user passwords, which are cluster-specific and must not be shared.

To prevent this, all ALTER USER operations (RETAIN and DISCARD) are executed with `SET sql_log_bin=0`.
This is a session-scoped variable; a dedicated connection (`db.Conn`) is used to ensure the setting applies to the same session as the ALTER USER statement.

Because binlog is disabled, within-cluster replicas do **not** receive the password change via replication.
The controller therefore executes ALTER USER on every instance individually (primary + all replicas).
For replicas, `super_read_only` is temporarily disabled before ALTER USER and re-enabled afterwards.
If re-enabling fails, MOCO's clustering loop periodically restores `super_read_only=ON`, so the replica self-recovers.

### State Machine

```
                 annotation:
                 password-rotate=<rotationID>
                        |
                        v
  +------+       +-----------+       +-------------+       +------+
  | Idle | ----> | Rotating  | ----> | Distributed | ----> | Idle |
  +------+       +-----------+       +-------------+       +------+
                  |                    |                      ^
                  | ALTER USER RETAIN  | annotation:          |
                  | on ALL instances   | password-discard     |
                  | (sql_log_bin=0)    |       |              |
                  | (Phase 1)          v       v              |
                  |              DISCARD OLD PASSWORD         |
                  |              on ALL instances             |
                  |              confirm Secret               |
                  |              reset status (save           |
                  |              LastRotationID)              |
                  +------------------------------------------+
                        (on crash: resume from current phase)
```

The rotation state is stored in `.status.systemUserRotation`:

```go
type SystemUserRotationStatus struct {
    RotationID     string        `json:"rotationID,omitempty"`
    Phase          RotationPhase `json:"phase,omitempty"`
    RotateApplied  bool          `json:"rotateApplied,omitempty"`
    DiscardApplied bool          `json:"discardApplied,omitempty"`
    LastRotationID string        `json:"lastRotationID,omitempty"`
}
```

`LastRotationID` records the rotationID of the most recently completed cycle.
It is used to detect stale rotate annotations (see [Stale Annotation Detection](#stale-annotation-detection)).

### Phase 1: handlePasswordRotate

| Step | Action | Persistence |
|------|--------|-------------|
| 0 | Pre-check: if Phase=Idle and replicas>0, scan all instances × all MOCO system users via `HasDualPassword`. Refuse with `DualPasswordExists` Event if any dual password is found. | - |
| 1 | If Phase=Idle, use rotationID from annotation, set Phase=Rotating | **Status.Patch** (immediate) |
| 2 | Generate `*_PENDING` passwords in source Secret | Secret.Update |
| 3 | For each instance (0..replicas-1), for each user: check `HasDualPassword`, skip if true, else ALTER USER RETAIN with sql_log_bin=0 | MySQL |
| 4 | Set RotateApplied=true | **Status.Patch** (immediate) |
| 5 | Distribute pending passwords to per-namespace Secrets | Secret.Apply |
| 6 | Set Phase=Distributed | **Status.Patch** (immediate) |
| 7 | Remove `password-rotate` annotation | Patch (best-effort) |

**Instance loop detail (Step 3):**

Instances are processed sequentially in ordinal order (0, 1, 2, ...).
For each instance:

1. Connect to the instance using the current (original) password
2. If the instance is a replica: `SET GLOBAL super_read_only=OFF`
3. For each user in `constants.MocoUsers`:
   a. Query `mysql.user.User_attributes` via `HasDualPassword` to check if the user already has an `additional_password`
   b. If yes: skip (RETAIN was already applied on a previous attempt)
   c. If no: execute `ALTER USER ... RETAIN CURRENT PASSWORD` (with `sql_log_bin=0`)
4. If the instance is a replica: `SET GLOBAL super_read_only=ON` (best-effort; clustering loop recovers on failure)

Phase advances only after **all instances** complete.
If any instance is unreachable, the reconcile returns an error and retries.

**Scaled-down clusters (replicas=0):**

When `spec.replicas` is 0, rotation is **refused** with a Warning Event (`RotateRefused`) and the handler returns an error.
Without running instances, ALTER USER cannot be executed. If rotation proceeded, new passwords would be distributed but MySQL would still accept only the old passwords, breaking connectivity when the cluster scales back up.

**Why HasDualPassword is used instead of per-user Status.Patch:**

MySQL's dual-password mechanism holds only one secondary password per user.
Re-running `ALTER USER ... RETAIN CURRENT PASSWORD` with the same pending password
would move the pending password (now the primary) into the secondary slot, evicting
the original password. After that, the controller can no longer connect using the
original password on the next retry.

Instead of tracking progress in Kubernetes status (which can fail independently of
MySQL), we query MySQL directly: if a user already has a dual password
(`mysql.user.User_attributes` contains `additional_password`), RETAIN is skipped.
This makes MySQL the source of truth, eliminates per-user `Status.Patch` calls,
and is safe on re-reconcile because the SELECT is read-only.

**Why super_read_only is toggled per-instance:**

Replicas have `super_read_only=ON`, which blocks all writes including ALTER USER.
The controller temporarily disables it before ALTER USER and re-enables it afterwards.
If the re-enable fails (e.g., due to a crash), MOCO's clustering loop periodically
sets `super_read_only=ON` on replicas, so recovery is automatic.

**Rolling restart on Distributed:**

When Phase transitions to Distributed, the `reconcileV1StatefulSet` function adds a Pod template annotation (`moco.cybozu.com/password-rotation-restart: <rotationID>`) to the StatefulSet.
This triggers a rolling restart of all Pods so that the agent sidecar (which reads passwords via `EnvFrom`) picks up the new credentials.

The annotation value is derived from `RotationID` (during active rotation) or `LastRotationID` (after completion).
This ensures the annotation value stays stable across the Distributed→Idle transition and does not cause spurious rollouts:

| State | RotationID | LastRotationID | Annotation Value | Effect |
|-------|-----------|---------------|-----------------|--------|
| Never rotated | "" | "" | (absent) | No change |
| Distributed | "abc" | "" | "abc" | Triggers rollout |
| Idle (after completion) | "" | "abc" | "abc" | Same value, no change |
| 2nd Distributed | "def" | "abc" | "def" | New rollout |

**Why Phase=Distributed removes the annotation silently:**

If the `password-rotate` annotation is still present after Phase 1 completes (e.g. the best-effort annotation removal failed), the next reconcile should just clean it up without emitting an Event or re-running any logic.

**Why rotate and discard never run in the same reconcile:**

If both `password-rotate` and `password-discard` annotations are present, the controller handles rotate first and returns immediately.
Discard is evaluated on the next reconcile.
This preserves the two-phase design: after rotate completes (Phase=Distributed), operators have a verification window before discard runs.

### Phase 2: handlePasswordDiscard

| Step | Action | Persistence |
|------|--------|-------------|
| 0 | Validate preconditions (Phase=Distributed, RotateApplied=true) | - |
| 0b | Validate pending passwords exist in source Secret (once) | - |
| 0c | Gate: wait for StatefulSet rollout to complete | - |
| 1 | DISCARD OLD PASSWORD on ALL instances with sql_log_bin=0 (using pending password) | MySQL |
| 2 | Set DiscardApplied=true | **Status.Patch** (immediate) |
| 3 | ConfirmPendingPasswords: copy pending to current, delete pending keys | Secret.Update |
| 4 | Reset status to Idle, save LastRotationID | **Status.Patch** (immediate) |
| 5 | Remove both annotations | Patch (best-effort) |

**All-instance execution (Step 1):**

Like Phase 1, DISCARD is executed on every instance (primary + all replicas) with `sql_log_bin=0` to prevent binlog propagation.
For replicas, `super_read_only` is temporarily disabled.
DISCARD OLD PASSWORD is idempotent in MySQL (no-op when there is no secondary password), so per-user tracking is not needed for crash safety.

**Why the rollout gate exists (Step 0c):**

The agent sidecar reads passwords from `EnvFrom`, which is only evaluated at Pod startup.
After Phase 1 distributes new passwords and triggers a rolling restart, we must wait for all Pods to restart before discarding old passwords.
Otherwise, Pods still running with old credentials would fail to connect to MySQL after `DISCARD OLD PASSWORD`.

The gate checks all of the following conditions:

* `ObservedGeneration >= Generation` — the StatefulSet controller has observed the latest spec
* `CurrentRevision == UpdateRevision` — all Pods are on the new revision
* `UpdatedReplicas == spec.replicas` — all Pods have been updated
* `ReadyReplicas == spec.replicas` — all Pods are ready

If the rollout is not complete, the handler returns `ctrl.Result{RequeueAfter: 15s}` (not an error) to avoid error-level log spam while ensuring periodic re-checks even if a StatefulSet watch event is missed.
This check is inside the `!DiscardApplied` block: once DISCARD has succeeded, the gate is no longer needed and only Secret confirmation remains.

**Scaled-down clusters (replicas=0):**

When `spec.replicas` is 0, discard is **rejected** with a Warning Event (`DiscardRefused`) and the handler returns `RequeueAfter: 15s` (`rotationRequeueInterval`).
Without running Pods, we cannot verify that the new passwords actually work, so discarding old passwords would be unsafe.
The operator should scale the cluster up first; once all Pods are running with the new template, the discard gate will pass normally.

**Why LastRotationID is saved at Step 4:**

Before resetting to Idle, `rotation.LastRotationID = rotation.RotationID` is persisted.
If the best-effort annotation removal at Step 5 fails, the stale annotation's rotationID will match `LastRotationID`, allowing the controller to detect and discard it on the next reconcile without starting a spurious new rotation.

**Why discard connects with the pending password:**

DISCARD removes the old password from MySQL.
If we connected with the old password and DISCARD succeeded, the connection credential would become invalid immediately.
Connecting with the pending (new) password also serves as an implicit verification that the new password was distributed correctly.

**Why ConfirmPendingPasswords is idempotent:**

A crash between Secret.Update (Step 3) and Status.Patch (Step 4) leaves the source Secret already confirmed but `DiscardApplied=true` in status.
On re-reconcile, Confirm runs again but finds no pending keys and returns nil (no-op).

**Why HasPendingPasswords is checked only once at the top:**

Checking again between steps could create a "DISCARD succeeded but Secret not confirmed" state if the check fails due to a transient issue.
Once the precondition is validated, all subsequent steps proceed under that assumption.

### Source Secret Layout

The controller-managed source Secret (in the system namespace) holds both current and pending passwords during rotation:

```
ADMIN_PASSWORD:         <current>
AGENT_PASSWORD:         <current>
...
ADMIN_PASSWORD_PENDING: <new>      # only during rotation
AGENT_PASSWORD_PENDING: <new>      # only during rotation
...
ROTATION_ID:            <uuid>     # only during rotation
```

`ROTATION_ID` ties the pending passwords to a specific rotation.
`HasPendingPasswords` validates that all 8 `*_PENDING` keys and `ROTATION_ID` are present and match the expected rotation ID.
Partial or mismatched state is treated as an error.

### Interaction with reconcileV1Secret

`reconcileV1Secret` runs before `reconcileV1PasswordRotation` in the reconcile loop.
During rotation (Phase != Idle), `reconcileV1Secret` returns early without distributing.
The rotation handler owns distribution responsibility:

* Phase=Rotating/Distributed: rotation handler distributes pending passwords
* Phase=Idle: `reconcileV1Secret` distributes current passwords normally

This ordering is safe because the Phase guard in `reconcileV1Secret` prevents it from overwriting pending passwords with stale current passwords.
The Phase transition from Idle to Rotating is persisted immediately via `Status.Patch` before any MySQL operation, so within a single reconcile, `reconcileV1Secret` always observes the correct Phase.

After discard completes and status resets to Idle, the source Secret's current passwords are already the new ones (confirmed by `ConfirmPendingPasswords`), so normal distribution produces the correct result.

### Annotation Removal

Annotations are removed on a best-effort basis.
The removal Patch may conflict with a concurrent Status.Patch on the same resource.
If it fails, the annotation will be removed on the next reconcile.
This is why `removeAnnotation` does not return an error.

### Stale Annotation Detection

After a completed rotation cycle, the best-effort annotation removal may fail, leaving the `password-rotate` annotation with the rotationID of the just-completed cycle.
Without detection, the next reconcile would interpret this as a fresh rotation request and start a new cycle.

The controller detects stale annotations by comparing the annotation's rotationID with `LastRotationID` in status:

* **`annotationID == LastRotationID`**: The annotation is stale. Remove it (best-effort) without starting a new rotation.
* **`annotationID != LastRotationID`**: The annotation is a fresh trigger. Proceed with rotation.

This approach follows the design principle that status is the source of truth and annotations are one-shot triggers.
Annotation removal failure does not block or compromise the rotation lifecycle.

### Precondition-Not-Met Handling

When `password-discard` is set but the preconditions are not met (Phase != Distributed or RotateApplied != true):

1. The annotation is removed (consumed)
2. A Warning Event (`DiscardSkipped`) is emitted explaining the precondition and that the annotation was removed
3. The user can re-apply the annotation after running `password-rotate`

## Recovery Procedures

### Stale pending passwords (rotationID mismatch)

**Symptom:** Warning Event `StaleRotationPending`

**Cause:** A previous rotation was interrupted, leaving `*_PENDING` and `ROTATION_ID` from a different rotation.
This typically happens when the status was manually reset to Idle while the source Secret still contains pending data from the interrupted rotation.

**Why MySQL cleanup is required:**

If the interrupted rotation had partially executed ALTER USER RETAIN on some instances, those instances still hold dual passwords from the old rotation.
Without cleaning up MySQL first, a subsequent rotation would see `HasDualPassword=true` on those instances and skip RETAIN, leaving them with stale passwords from the old rotation while other instances receive new passwords from the new rotation.
After DISCARD, the stale instances would only accept the old rotation's password, which the controller does not know, breaking connectivity.

Note that `DISCARD OLD PASSWORD` **cannot** be used for this cleanup.
After RETAIN, the primary is the new (pending) password and the secondary is the old (current) password.
DISCARD removes the secondary (old), leaving only the new password — but the source Secret's current keys still hold the old password, so the controller and Pods would lose connectivity.
The pending passwords may not have been distributed to per-namespace Secrets either (distribution is a later step in Phase 1), so Pods would also be unable to connect.

Instead, reset each user's password back to the current (old) value using `ALTER USER ... IDENTIFIED BY` without RETAIN.
This sets the primary back to the old password and clears any secondary, restoring MySQL to the pre-rotation state.

**Recovery ordering:**

The Secret and status must be cleaned up **before** MySQL is modified.
At the time of this error, the controller has already set Phase=Rotating (with the new rotationID) in status, so `reconcileV1Secret` is skipping distribution.
If the interrupted rotation had distributed pending (new) passwords, Pods may be running with them.
While MySQL still holds dual passwords, both old and new are accepted, so there is no connectivity loss during the transition.
If MySQL were reset to old-only first, Pods still running with the new passwords would immediately lose connectivity.

The correct sequence is status → Secret → rollout → MySQL.
This ordering guarantees that Pods switch back to old credentials before MySQL clears dual-password state, ensuring continuous connectivity.

1. Reset status to return to Idle → `reconcileV1Secret` resumes and re-distributes old (current) passwords
2. Clean the source Secret (delete `*_PENDING` and `ROTATION_ID`)
3. Wait for the StatefulSet rollout to complete (all Pods running with old passwords)
4. Reset MySQL to old-only on all instances

During steps 1–3, MySQL still accepts both old and new passwords via dual password, so no connectivity is lost regardless of whether Pods have old or new passwords.

**Recovery:**

```console
# Step 1: Reset rotation status to return to Idle.
# This unblocks reconcileV1Secret, which will re-distribute the current (old)
# passwords to per-namespace Secrets. It also updates the StatefulSet template
# annotation, triggering a rolling restart so Pods pick up the old passwords.
#
# IMPORTANT: Do not set status.systemUserRotation to {} — that would also
# clear LastRotationID, breaking stale annotation detection for future rotations.
# Only reset the rotation-in-progress fields:
$ kubectl edit mysqlcluster <name>
# Set the following fields in status.systemUserRotation:
#   phase: ""
#   rotationID: ""
#   rotateApplied: false
#   discardApplied: false
# Leave lastRotationID unchanged.

# Step 2: Clean the source Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_PENDING keys and ROTATION_ID

# Step 3: Wait for the StatefulSet rollout to complete.
# All Pods must be running with the old passwords before MySQL is modified.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 4: Reset MySQL passwords to the current (old) values on ALL instances.
#
# Retrieve the current passwords from the source Secret:
$ kubectl -n <system-namespace> get secret <controller-secret-name> \
    -o jsonpath='{.data.ADMIN_PASSWORD}' | base64 -d
# (repeat for each user key: ADMIN_PASSWORD, AGENT_PASSWORD, REPLICATION_PASSWORD,
#  CLONE_DONOR_PASSWORD, EXPORTER_PASSWORD, BACKUP_PASSWORD, READONLY_PASSWORD, WRITABLE_PASSWORD)
#
# Connect to each instance (0, 1, ..., replicas-1) using moco-admin and execute:
# (The old password still works — it is accepted as either the primary or secondary.)
#
# First, identify which instance is the primary:
$ kubectl -n <namespace> exec <pod> -c mysqld -- mysql -u moco-admin -p<admin-password> \
    -e "SELECT @@read_only, @@super_read_only;"
# primary: read_only=0, super_read_only=0
# replica: read_only=1, super_read_only=1
#
# For REPLICA instances: sql_log_bin=0 MUST be set before disabling super_read_only
# to prevent any intermediate writes from being logged to the binlog.
# MOCO's clustering loop will re-enable super_read_only automatically if the
# manual re-enable fails.
#
# For the PRIMARY instance: omit the super_read_only commands.
#
# --- Primary ---
$ kubectl -n <namespace> exec <primary-pod> -c mysqld -- mysql -u moco-admin -p<admin-password> -e "
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
# --- Each Replica ---
$ kubectl -n <namespace> exec <replica-pod> -c mysqld -- mysql -u moco-admin -p<admin-password> -e "
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
# ALTER USER without RETAIN sets the primary password and clears any secondary.
# This is safe to run on instances where RETAIN was never applied (no-op equivalent).
```

### Missing pending passwords during discard

**Symptom:** Warning Event `MissingRotationPending`

**Cause:** The source Secret lost its pending keys (manual edit, restore from backup, etc.) while status still shows Distributed.

**Why this is dangerous:**

At Phase=Distributed, Phase 1 has completed:
all instances hold dual passwords (primary=pending, secondary=old), pending passwords have been distributed to per-namespace Secrets, and Pods may already be running with the pending passwords.
The pending passwords are irrecoverable from MySQL (passwords are stored as hashes) and the source Secret has lost them.

Simply resetting the status to Idle and cleaning the Secret would cause `reconcileV1Secret` to re-distribute the old (current) passwords, and Pods would eventually switch back to old.
MySQL still accepts old as the secondary, so connectivity is maintained during the transition.
However, MySQL's primary remains the unknown pending password and dual password state persists indefinitely.
Any subsequent rotation would see `HasDualPassword=true` on all instances, skip RETAIN, and produce an inconsistent state.

Because the controller has no knowledge of the lost pending password and cannot reconstruct it from MySQL (passwords are stored as hashes only), completing the rotation forward is impossible.
The only safe recovery is to roll back MySQL to the old passwords (clearing dual password state), following the same pattern as the stale pending recovery.
The ordering (status → Secret → rollout → MySQL) guarantees that Pods switch back to old credentials before MySQL clears dual-password state, ensuring continuous connectivity.

**Recovery:**

```console
# Step 1: Reset rotation status to return to Idle.
# This unblocks reconcileV1Secret, which will re-distribute the current (old)
# passwords to per-namespace Secrets and update the StatefulSet template,
# triggering a rolling restart so Pods pick up the old passwords.
#
# IMPORTANT: Do not set status.systemUserRotation to {} — that would also
# clear LastRotationID, breaking stale annotation detection for future rotations.
# Only reset the rotation-in-progress fields:
$ kubectl edit mysqlcluster <name>
# Set the following fields in status.systemUserRotation:
#   phase: ""
#   rotationID: ""
#   rotateApplied: false
#   discardApplied: false
# Leave lastRotationID unchanged.

# Step 2: Clean the source Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete any remaining *_PENDING keys and ROTATION_ID

# Step 3: Wait for the StatefulSet rollout to complete.
# All Pods must be running with the old passwords before MySQL is modified.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 4: Reset MySQL passwords to the current (old) values on ALL instances.
#
# Retrieve the current passwords from the source Secret:
$ kubectl -n <system-namespace> get secret <controller-secret-name> \
    -o jsonpath='{.data.ADMIN_PASSWORD}' | base64 -d
# (repeat for each user key: ADMIN_PASSWORD, AGENT_PASSWORD, REPLICATION_PASSWORD,
#  CLONE_DONOR_PASSWORD, EXPORTER_PASSWORD, BACKUP_PASSWORD, READONLY_PASSWORD, WRITABLE_PASSWORD)
#
# Connect to each instance (0, 1, ..., replicas-1) using moco-admin and execute:
# (The old password still works — it is accepted as either the primary or secondary.)
#
# First, identify which instance is the primary:
$ kubectl -n <namespace> exec <pod> -c mysqld -- mysql -u moco-admin -p<admin-password> \
    -e "SELECT @@read_only, @@super_read_only;"
# primary: read_only=0, super_read_only=0
# replica: read_only=1, super_read_only=1
#
# For REPLICA instances: sql_log_bin=0 MUST be set before disabling super_read_only
# to prevent any intermediate writes from being logged to the binlog.
# MOCO's clustering loop will re-enable super_read_only automatically if the
# manual re-enable fails.
#
# For the PRIMARY instance: omit the super_read_only commands.
#
# --- Primary ---
$ kubectl -n <namespace> exec <primary-pod> -c mysqld -- mysql -u moco-admin -p<admin-password> -e "
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
# --- Each Replica ---
$ kubectl -n <namespace> exec <replica-pod> -c mysqld -- mysql -u moco-admin -p<admin-password> -e "
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
# ALTER USER without RETAIN sets the primary password and clears any secondary.
# This is safe to run on instances where RETAIN was never applied (no-op equivalent).
```

### Dual password exists while Phase=Idle

**Symptom:** Warning Event `DualPasswordExists`

**Cause:** A MOCO system user has a dual password (i.e. `additional_password` is set in `mysql.user.User_attributes`) while the rotation status is Idle. This can happen when a previous rotation was interrupted and the manual recovery did not fully clear dual password state in MySQL, or when someone ran `ALTER USER ... RETAIN CURRENT PASSWORD` manually.

**Why DISCARD OLD PASSWORD must not be used:**

After RETAIN, the primary password is the new (potentially unknown) password and the secondary is the old (current) password known to the controller. Running `DISCARD OLD PASSWORD` would remove the secondary (old), leaving only the unknown primary, causing the controller and Pods to lose connectivity immediately.

**Recovery:**

The status is already Idle, so no status reset is needed. The source Secret should not have `*_PENDING` keys (since rotation never started). The goal is to reset MySQL to match the current passwords in the source Secret.

```console
# Step 1 (optional but recommended): Verify the distributed Secrets match
# the source Secret's current passwords. If a previous rotation had partially
# distributed pending passwords, a reconcile in Phase=Idle will have already
# re-distributed the current passwords. Confirm by checking that Pods can
# connect to MySQL with the current credentials.

# Step 2 (optional but recommended): Wait for any in-progress rollout to
# complete, ensuring all Pods are running with the current passwords.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 3: Reset MySQL passwords to the current (old) values on ALL instances.
#
# Retrieve the current passwords from the source Secret:
$ kubectl -n <system-namespace> get secret <controller-secret-name> \
    -o jsonpath='{.data.ADMIN_PASSWORD}' | base64 -d
# (repeat for each user key: ADMIN_PASSWORD, AGENT_PASSWORD, REPLICATION_PASSWORD,
#  CLONE_DONOR_PASSWORD, EXPORTER_PASSWORD, BACKUP_PASSWORD, READONLY_PASSWORD, WRITABLE_PASSWORD)
#
# First, identify which instance is the primary:
$ kubectl -n <namespace> exec <pod> -c mysqld -- mysql -u moco-admin -p<admin-password> \
    -e "SELECT @@read_only, @@super_read_only;"
# primary: read_only=0, super_read_only=0
# replica: read_only=1, super_read_only=1
#
# --- Primary ---
$ kubectl -n <namespace> exec <primary-pod> -c mysqld -- mysql -u moco-admin -p<admin-password> -e "
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
# --- Each Replica ---
$ kubectl -n <namespace> exec <replica-pod> -c mysqld -- mysql -u moco-admin -p<admin-password> -e "
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
# ALTER USER without RETAIN sets the primary password and clears any secondary.
# This is safe to run on instances where RETAIN was never applied (no-op equivalent).
#
# After this, retry the rotation: kubectl moco credential rotate <name>
```

## Security Considerations

`RotateUserPassword` and `DiscardOldPassword` interpolate the user name directly into SQL because MySQL does not support placeholders for the user position of `ALTER USER`.
The user name is always one of the 8 fixed constants from `pkg/constants/users.go`.
Callers must never pass arbitrary or user-supplied strings.

All ALTER USER operations use `SET sql_log_bin=0` to prevent password changes from propagating to cross-cluster replicas via the binary log.
A dedicated connection (`db.Conn`) is used to ensure `sql_log_bin=0` applies to the same session as the ALTER USER statement.
