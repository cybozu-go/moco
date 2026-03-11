# System User Password Rotation with CredentialRotation CRD

## Background

MOCO manages 8 system MySQL users (`moco-admin`, `moco-agent`, `moco-repl`, `moco-clone-donor`, `moco-exporter`, `moco-backup`, `moco-readonly`, `moco-writable`).
Their passwords are generated at cluster creation, stored in a controller-managed Secret in the system namespace, and distributed to per-namespace Secrets.
Once generated, these passwords never change.

If a credential leak occurs, the only recovery option today is recreating the cluster.
This design introduces an in-place password rotation mechanism that avoids downtime, using a dedicated **CredentialRotation** CRD with its own controller.

## Why a Dedicated CRD?

Password rotation could be implemented as part of `MySQLClusterReconciler`, but a dedicated CRD is preferable for several reasons:

1. **Blast radius** — If the rotation handler stalls or panics, a dedicated controller isolates the failure. `MySQLClusterReconciler` continues to handle StatefulSet reconciliation, Service management, and backup CronJob creation without interruption.

2. **Status bloat** — `MySQLCluster.Status` already contains conditions, backup status, reconcile info, replica counts, and more. A separate CRD keeps rotation state out of `MySQLCluster`.

3. **Testability** — `MySQLClusterReconciler` is already large and complex. A separate controller makes rotation logic easier to test in isolation.

4. **Separation of concerns** — Password rotation is an operator-initiated, infrequent operation with its own lifecycle. A dedicated CRD naturally represents this lifecycle as a Kubernetes resource.

KubeDB takes a similar approach with `MySQLOpsRequest` (type: RotateAuth) as a separate CRD for credential rotation operations.

## Goals and Non-goals

**Goals:**

- Rotate all 8 system user passwords without MySQL downtime
- Isolate rotation processing in a dedicated CRD and controller
- Idempotent and crash-safe (controller restart resumes correctly)
- Prevent accidental propagation of ALTER USER to cross-cluster replicas
- Operator-initiated via `kubectl moco`
- Documented manual recovery for every failure mode

**Non-goals:**

- Automatic periodic rotation (build externally with a CronJob that increments `rotationGeneration`)
- Per-user rotation (all 8 users rotate together)
- End-user credential management

## Overview

Password rotation is a **two-step process** — **rotate** then **discard** — using MySQL's dual password feature (8.0.14+).
The operator explicitly triggers each step, with a verification window in between.

```
  User bumps spec.rotationGeneration
               |
               v
  +--- Rotate operation ----------------------+
  |                                            |
  |  [""/Completed]                            |
  |    | CredentialRotationReconciler:         |
  |    | generate pending passwords            |
  |    v                                       |
  |  [Rotating]                                |
  |    | ClusterManager:                       |
  |    | ALTER USER RETAIN on all instances    |
  |    v                                       |
  |  [Retained]                                |
  |    | CredentialRotationReconciler:         |
  |    | distribute Secrets + rolling restart   |
  |    v                                       |
  |  [Rotated]                                 |
  +----+---------------------------------------+
       |
       v
  Operator verifies apps work with new passwords
       |
  kubectl moco credential discard
       |
       v
  +--- Discard operation ---------------------+
  |                                            |
  |  Wait for StatefulSet rollout              |
  |    |                                       |
  |    v                                       |
  |  [Discarding]                              |
  |    | ClusterManager:                       |
  |    | DISCARD OLD PASSWORD                  |
  |    | + auth plugin migration               |
  |    v                                       |
  |  [Discarded]                               |
  |    | CredentialRotationReconciler:         |
  |    | confirm Secret                        |
  |    v                                       |
  |  [Completed]                               |
  |                                            |
  +--------------------------------------------+
```

After the rotate operation, both old and new passwords are valid (MySQL dual password).
The operator can verify that applications work with the new credentials before committing to discard the old ones.

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

### Why Auth Plugin Migration Is in the Discard Phase?

MySQL Error 3894 prevents changing the authentication plugin in a `RETAIN CURRENT PASSWORD` statement.
Instead, plugin migration happens after DISCARD using `ALTER USER ... IDENTIFIED WITH <plugin> BY ...`.

The target plugin is determined from `@@global.authentication_policy` on the primary instance:
- If the first element is a concrete plugin name (e.g. `caching_sha2_password`): use it
- If the first element is `*` or empty: default to `caching_sha2_password`

This enables transparent migration from legacy plugins like `mysql_native_password` during rotation.

### Responsibility Split: CredentialRotationReconciler vs ClusterManager

The CredentialRotationReconciler handles **K8s resource operations** (Phase transitions, Secret management, rollout checks).
The ClusterManager handles **DB operations** (ALTER USER RETAIN, DISCARD OLD PASSWORD, auth plugin migration, dual password pre-checks).

This follows the existing separation in MOCO where controllers manage K8s objects and the ClusterManager manages MySQL state.
The phase flow ensures clear handoffs:

```
""/Completed → Rotating → Retained → Rotated → Discarding → Discarded → Completed
    ↑Reconciler  ↑ClusterMgr  ↑Reconciler            ↑ClusterMgr  ↑Reconciler
```

## CRD Definition

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata:
  name: my-cluster            # Must match the target MySQLCluster name
  namespace: my-namespace     # Must match the target MySQLCluster namespace
  ownerReferences:
    - apiVersion: moco.cybozu.com/v1beta2
      kind: MySQLCluster
      name: my-cluster
      uid: ...
spec:
  rotationGeneration: 1       # Bump to trigger a new rotation
  discardOldPassword: false   # User sets true to trigger discard phase
status:
  phase: ""                   # Current rotation phase
  rotationID: ""              # UUID for this rotation cycle
  observedRotationGeneration: 0  # Last completed rotationGeneration
```

### Naming Convention

The CredentialRotation resource name **must match** the target MySQLCluster name (same name, same namespace).
This naturally enforces at most one active rotation per cluster and simplifies lookups — both the controller and ClusterManager can find the CR by the cluster name without a separate reference field.

The CR is **long-lived** — it is created once and persists across multiple rotation cycles.
To start a new rotation, the user increments `spec.rotationGeneration` and sets `spec.discardOldPassword` to false.
The controller compares `spec.rotationGeneration` with `status.observedRotationGeneration` to detect new rotation requests.

### OwnerReference

CredentialRotation sets an ownerReference to the target MySQLCluster.
This ensures garbage collection when the MySQLCluster is deleted.

### Go Type Definitions

```go
// api/v1beta2/credentialrotation_types.go

// CredentialRotationSpec defines the desired state of CredentialRotation.
// The target MySQLCluster is identified by the CR's own name and namespace
// (CredentialRotation name must equal MySQLCluster name).
type CredentialRotationSpec struct {
    // RotationGeneration is a monotonically increasing counter.
    // Incrementing this value triggers a new rotation cycle.
    // +optional
    RotationGeneration int64 `json:"rotationGeneration,omitempty"`

    // DiscardOldPassword triggers the discard phase.
    // Can only be set to true when Phase is Rotated.
    // Must be reset to false when incrementing rotationGeneration.
    // +optional
    DiscardOldPassword bool `json:"discardOldPassword,omitempty"`
}

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
    // Phase is the current rotation phase.
    // +optional
    Phase RotationPhase `json:"phase,omitempty"`

    // RotationID is the UUID for this rotation cycle.
    // +optional
    RotationID string `json:"rotationID,omitempty"`

    // ObservedRotationGeneration is the last rotationGeneration
    // that completed successfully.
    // +optional
    ObservedRotationGeneration int64 `json:"observedRotationGeneration,omitempty"`
}

// RotationPhase represents the phase of a credential rotation.
type RotationPhase string

const (
    RotationPhaseRotating   RotationPhase = "Rotating"
    RotationPhaseRetained   RotationPhase = "Retained"
    RotationPhaseRotated    RotationPhase = "Rotated"
    RotationPhaseDiscarding RotationPhase = "Discarding"
    RotationPhaseDiscarded  RotationPhase = "Discarded"
    RotationPhaseCompleted  RotationPhase = "Completed"
)
```

### Validation Webhook

```go
func (r *CredentialRotation) ValidateCreate(ctx context.Context, ...) {
    // 1. MySQLCluster with the same name must exist in the same namespace
    // 2. MySQLCluster replicas must be > 0
    // 3. rotationGeneration must be > 0
    // 4. discardOldPassword must be false
}

func (r *CredentialRotation) ValidateUpdate(ctx context.Context, ...) {
    // 1. rotationGeneration must be >= old value (monotonically increasing)
    // 2. rotationGeneration can only increase when Phase is "" or Completed
    // 3. When rotationGeneration increases, discardOldPassword must be false
    // 4. When rotationGeneration is unchanged, discardOldPassword can only go false→true
    // 5. discardOldPassword=true requires Phase==Rotated
}
```

## User Interface

```console
# Rotate: create CR (first time) or bump rotationGeneration
$ kubectl moco credential rotate <cluster-name>

# Check status
$ kubectl get credentialrotation <cluster-name>
NAME         PHASE     GENERATION   OBSERVED   AGE
my-cluster   Rotated   1            0          5m

# Discard: set discardOldPassword=true
$ kubectl moco credential discard <cluster-name>

# Show current credentials
$ kubectl moco credential show <cluster-name>
```

### kubectl moco behavior

| Command | Action |
|---------|--------|
| `credential rotate` | If CR does not exist: create with `rotationGeneration: 1`. If CR exists: validate Phase is `""` or `Completed`, then increment `rotationGeneration` and set `discardOldPassword: false`. |
| `credential discard` | Validate Phase=Rotated → Patch `spec.discardOldPassword=true` |
| `credential show` | Read per-namespace user Secret |

The CLI validates preconditions (MySQLCluster with the same name exists, replicas > 0, no in-progress rotation).

Users can also interact with the CR directly via `kubectl`:

```console
# First rotation
$ kubectl apply -f credential-rotation.yaml
# credential-rotation.yaml:
#   spec:
#     rotationGeneration: 1
#     discardOldPassword: false

# Trigger discard
$ kubectl patch credentialrotation my-cluster --type=merge \
    -p '{"spec":{"discardOldPassword":true}}'

# Start next rotation (after previous one completed)
$ kubectl patch credentialrotation my-cluster --type=merge \
    -p '{"spec":{"rotationGeneration":2,"discardOldPassword":false}}'
```

### GitOps / ArgoCD

The CR is long-lived and purely declarative, so it works naturally with GitOps:

```yaml
# 1. First rotation: commit this manifest
spec:
  rotationGeneration: 1
  discardOldPassword: false

# 2. After verifying apps work: update and commit
spec:
  rotationGeneration: 1
  discardOldPassword: true

# 3. Next rotation: update and commit
spec:
  rotationGeneration: 2
  discardOldPassword: false
```

Each Git commit triggers an ArgoCD sync that advances the rotation lifecycle.
No imperative `kubectl` commands or CR deletion is required.

## Rotate

### CredentialRotationReconciler: ""/Completed → Rotating

Triggered when `spec.rotationGeneration > status.observedRotationGeneration` and Phase is `""` or `Completed`.

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Generate UUID as rotationID | - | Reconciler |
| 2 | Generate pending passwords (e.g. `ADMIN_PASSWORD_PENDING`) in the source Secret | Secret.Update | Reconciler |
| 3 | Set Phase to Rotating with the rotationID | Status.Update | Reconciler |

### ClusterManager: Rotating → Retained

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Pre-check: scan all instances for pre-existing dual passwords. Wait if found. Skip if `RETAIN_STARTED` marker is set (crash recovery). | - | ClusterManager |
| 1b | Set `RETAIN_STARTED` marker (rotationID) in source Secret | Secret.Update | ClusterManager |
| 2 | For each instance: execute `ALTER USER ... RETAIN CURRENT PASSWORD` with `sql_log_bin=0` | MySQL | ClusterManager |
| 3 | Set CredentialRotation Phase to Retained | Status.Update | ClusterManager |

**Pre-check and RETAIN_STARTED marker (Step 1):**
The pre-check scans all instances for pre-existing dual passwords (from outside this rotation cycle).
If any are found, the ClusterManager emits a `DualPasswordExists` Warning Event and waits.

To make the pre-check crash-safe, a `RETAIN_STARTED` marker is persisted in the source Secret before executing any RETAIN statements.
If the controller crashes after partial RETAIN and restarts, the marker indicates that the pre-check already passed for this rotation cycle — so the pre-check is skipped, and RETAIN resumes using per-user `HasDualPassword` checks for idempotency (Step 2).

**Instance loop (Step 2):**
Instances are processed sequentially (ordinal 0, 1, 2, ...).
For each instance:

1. Connect using the current (old) password
2. For replicas: temporarily disable `super_read_only`
3. For each user: check `HasDualPassword`. If the user already has a dual password (from a previous partial run), skip RETAIN; otherwise execute RETAIN.
4. For replicas: re-enable `super_read_only` (best-effort; the clustering loop recovers)

If any instance is unreachable, the ClusterManager returns an error and retries on the next cycle.

### CredentialRotationReconciler: Retained → Rotated

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Distribute pending passwords to per-namespace Secrets (user Secret + my.cnf Secret) | Secret.Apply | Reconciler |
| 2 | Add restart annotation (`moco.cybozu.com/password-rotation-restart: <rotationID>`) to StatefulSet Pod template. The value is the rotationID (UUID) to ensure each rotation triggers a new rollout. | StatefulSet.Patch | Reconciler |
| 3 | Set Phase to Rotated | Status.Update | Reconciler |
| 4 | Rolling restart (Pods pick up new passwords via `EnvFrom`) | - | StatefulSet controller |

**Scaled-down clusters (replicas=0):**
Rotation is refused by the validation webhook (rejects CR creation or `rotationGeneration` bump when replicas=0), or the Reconciler emits a Warning Event and does not advance past the initial phase.
Without running instances, ALTER USER cannot execute, and distributing new passwords would break connectivity when the cluster scales back up.

## Discard

### CredentialRotationReconciler: Rotated → Discarding

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Validate `spec.discardOldPassword=true` | - | Reconciler |
| 2 | Wait for StatefulSet rollout to complete | - | Reconciler |
| 3 | Set Phase to Discarding | Status.Update | Reconciler |

**Why wait for the rollout?**
The agent sidecar reads passwords from `EnvFrom`, evaluated only at Pod startup.
If old Pods are still running when DISCARD executes, they lose connectivity.
The gate checks: ObservedGeneration, CurrentRevision == UpdateRevision, UpdatedReplicas == Replicas, ReadyReplicas == Replicas.
While waiting, the handler returns `RequeueAfter: 15s` to avoid log spam.

### ClusterManager: Discarding → Discarded

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Determine target auth plugin via `GetAuthPlugin` on the primary | MySQL (read-only) | ClusterManager |
| 2 | For each instance: execute `DISCARD OLD PASSWORD` and auth plugin migration with `sql_log_bin=0` | MySQL | ClusterManager |
| 3 | Set CredentialRotation Phase to Discarded | Status.Update | ClusterManager |

**Why connect with the pending password?**
DISCARD removes the old password.
If we connected with the old password, the connection would become invalid immediately after DISCARD succeeds.
Using the pending password also implicitly verifies that distribution was successful.

**Scaled-down clusters (replicas=0):**
Discard is rejected with a Warning Event (`DiscardRefused`) and `RequeueAfter: 15s`.
The operator should scale the cluster up first.

### CredentialRotationReconciler: Discarded → Completed

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Promote pending passwords to current in the source Secret via `password.ConfirmPendingPasswords()` | Secret.Update | Reconciler |
| 2 | Set Phase to Completed, set `observedRotationGeneration = spec.rotationGeneration` | Status.Update | Reconciler |

## Source Secret Layout

During rotation, the source Secret (in the controller namespace) holds both current and pending passwords:

```
ADMIN_PASSWORD:         <current>
AGENT_PASSWORD:         <current>
...
ADMIN_PASSWORD_PENDING: <new>       # only during rotation
AGENT_PASSWORD_PENDING: <new>       # only during rotation
...
ROTATION_ID:            <uuid>      # only during rotation
RETAIN_STARTED:         <uuid>      # only during Rotating phase (crash-safety marker)
```

`HasPendingPasswords` validates that all 8 pending keys and `ROTATION_ID` are present and that the rotation ID matches the expected value.

### Why Embed Pending Passwords in the Source Secret?

An alternative is to store pending passwords in a separate Secret (owned by the CredentialRotation CR).
Pending passwords are embedded in the source Secret instead, for the following reasons:

1. **Crash safety of the confirm step** — `ConfirmPendingPasswords` (Discarded → Completed) promotes pending passwords to current by renaming keys within a single Secret. This is a single-object update. With a separate Secret, the confirm step would need to copy data between two Secrets. If the controller crashes between reading the pending Secret and writing the source Secret, and the pending Secret is subsequently lost (accidental deletion, failed GC), the new passwords become irrecoverable.

2. **Simpler failure modes** — With a single Secret, the only question on crash recovery is "did the update succeed?" With two Secrets, every phase must consider whether the two objects are consistent with each other.

3. **`SetPendingPasswords` and `ConfirmPendingPasswords` are naturally idempotent** — Both operate on a single object. `SetPendingPasswords` checks if pending keys with the matching rotation ID already exist; `ConfirmPendingPasswords` is a no-op when no pending keys remain. This idempotency would be harder to guarantee across two objects.

## Component Details

### CredentialRotationReconciler (new)

For phases where the ClusterManager drives progress (`Rotating`, `Discarding`), the Reconciler requeues every 15 seconds to check for phase advancement.

### ClusterManager

Handles MySQL-level operations by reading the CredentialRotation CR to determine the current phase.

```go
func (p *managerProcess) handlePasswordRotation(ctx context.Context, ss *StatusSet) (bool, error) {
    var cr mocov1beta2.CredentialRotation
    err := p.client.Get(ctx, types.NamespacedName{
        Namespace: p.name.Namespace,
        Name:      p.name.Name,
    }, &cr)
    if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
        return false, nil
    }
    if err != nil {
        return false, err
    }

    switch cr.Status.Phase {
    case mocov1beta2.RotationPhaseRotating:
        return p.handleRotatingPhase(ctx, ss, &cr)
    case mocov1beta2.RotationPhaseDiscarding:
        return p.handleDiscardingPhase(ctx, ss, &cr)
    default:
        return false, nil
    }
}
```

The DB operation logic (`rotateInstanceUsers`, `discardInstanceUsers`, `checkInstanceDualPasswords`) lives in `clustering/password_rotation.go` and reads/writes the CredentialRotation CR status for phase transitions.

### MySQLClusterReconciler

The only change to `MySQLClusterReconciler` is a guard in `reconcileV1Secret` that checks whether an active CredentialRotation exists:

```go
func (r *MySQLClusterReconciler) reconcileV1Secret(ctx context.Context, ...) (ctrl.Result, error) {
    // ... source Secret creation (unchanged) ...

    // During credential rotation, the CredentialRotationReconciler owns
    // secret distribution in phases where pending passwords have been or
    // are being distributed. Skip to prevent overwriting them.
    // In the Rotating phase, pending passwords have NOT been distributed
    // to user Secrets yet, so normal reconciliation continues — this
    // allows self-healing and keeps the clustering loop operational.
    // On any Get error (NotFound, CRD missing, cache not ready), assume
    // no active rotation and proceed with distribution.
    var cr mocov1beta2.CredentialRotation
    if err := r.Get(ctx, client.ObjectKey{
        Namespace: cluster.Namespace,
        Name:      cluster.Name,
    }, &cr); err == nil {
        switch cr.Status.Phase {
        case mocov1beta2.RotationPhaseRetained,
            mocov1beta2.RotationPhaseRotated,
            mocov1beta2.RotationPhaseDiscarding,
            mocov1beta2.RotationPhaseDiscarded:
            return nil
        }
    }

    // ... normal secret distribution ...
}
```

The `r.Get()` reads from the informer cache, which controller-runtime starts on demand for the CredentialRotation GVK.
The overhead is negligible (CredentialRotation objects are few and small).

**Cache lag safety:**
If the cache briefly shows a stale Phase, the guard is still safe:
- Phase appears as `""` or `Completed` (rotation not yet started, pending not yet distributed) → no skip → distributes old passwords → harmless
- Phase appears as `Rotating` (pending not yet distributed) → no skip → distributes old passwords → harmless (same as above; Rotating is intentionally not skipped)
- Any Phase from `Retained` onward → skip → correct

The only theoretical risk is if the cache does not yet reflect Phase=Retained while the CredentialRotationReconciler has already distributed pending passwords (Retained → Rotated).
In practice this cannot happen because RETAIN executes on all MySQL instances (takes seconds to minutes), far exceeding the cache propagation delay (~hundreds of milliseconds).

## Crash Safety

### Phase Boundary Safety

| Crash Point | Recovery |
|---|---|
| rotationGeneration bumped, pending passwords not yet generated | Reconciler re-generates on next reconcile |
| Pending passwords generated, RETAIN not started | ClusterManager picks up Phase=Rotating |
| Pre-check passed, `RETAIN_STARTED` marker set, RETAIN not yet executed | Marker skips pre-check on retry; `HasDualPassword` makes RETAIN idempotent |
| RETAIN partially applied (some instances) | `RETAIN_STARTED` marker skips pre-check; `HasDualPassword` makes re-execution idempotent |
| RETAIN complete, Phase=Retained not yet set | ClusterManager re-runs → all skip → sets Retained |
| Phase=Retained, Secrets not yet distributed | Reconciler distributes on next reconcile |
| Phase=Discarding, DISCARD not yet executed | ClusterManager picks up Phase=Discarding |
| DISCARD complete, Phase=Discarded not yet set | DISCARD is idempotent → re-run → sets Discarded |
| Phase=Discarded, Secret promoted but status not updated | `HasPendingPasswords` returns false; `CurrentPasswordsMatch` verifies promotion succeeded → sets Completed |
| Phase=Discarded, Secret not yet promoted | `ConfirmPendingPasswords` is idempotent |

### HasDualPassword Instead of Per-User Status Tracking

MySQL holds only one secondary password per user.
If RETAIN is re-run with the same pending password after a crash, the pending password (now the primary) moves into the secondary slot, evicting the original password.
The controller can no longer connect.

Instead of tracking per-user progress in Kubernetes status (which can fail independently of MySQL), the ClusterManager queries MySQL directly: if `mysql.user.User_attributes` contains `additional_password`, RETAIN is skipped.
This makes MySQL the source of truth and is safe on re-execution because the query is read-only.

### Idempotency of DISCARD

`DISCARD OLD PASSWORD` is idempotent in MySQL (no-op when there is no secondary password), so per-user tracking is not needed.

### ConfirmPendingPasswords Idempotency

If the controller crashes after updating the Secret but before patching the status, the Secret has already been promoted (pending passwords are now current) but the Phase is still Discarded.
On re-reconcile, `HasPendingPasswords` returns `(false, nil)`.
The Reconciler then verifies crash recovery by comparing the controller Secret's current passwords with the per-namespace user Secret via `CurrentPasswordsMatch`.
If they match, promotion already succeeded — the Reconciler proceeds to set Phase=Completed.
If they differ (indicating pending keys were lost without promotion), the Reconciler emits an `InconsistentState` Warning Event and requeues instead of completing.

### Stale Rotation Detection

The `rotationGeneration` / `observedRotationGeneration` pair replaces any need for `LastRotationID` tracking.
A new rotation is detected when `spec.rotationGeneration > status.observedRotationGeneration`.
The `rotationID` (UUID) in the source Secret is matched against `status.rotationID` to detect stale pending passwords from a previous interrupted cycle.

### Status Update Conflict Handling

The ClusterManager uses `retry.RetryOnConflict` with `Status().Update()` on the CredentialRotation CR for phase transitions, the same pattern as the current `MySQLCluster` status updates.

## Interaction with Other Reconcile Steps

### reconcileV1Secret

During an active rotation, `reconcileV1Secret` checks for the existence of a CredentialRotation CR (see [MySQLClusterReconciler](#mysqlclusterreconciler)).
After rotation completes (Phase=Completed), the source Secret already contains promoted passwords, so normal distribution resumes correctly.

### GatherStatus and ClusterManager Connection

Unchanged. GatherStatus reads passwords from the per-namespace user Secret.
During rotation phases, the user Secret always contains passwords that MySQL accepts:
- Phase=Rotating/Retained: user Secret has old passwords, MySQL accepts old via dual password
- Phase=Rotated onwards: new passwords have been distributed to user Secret

The rotation handler reads passwords directly from the source (controller) Secret and creates its own DB connections, independent of GatherStatus.

### StatefulSet Rolling Restart

The CredentialRotationReconciler patches the StatefulSet Pod template annotation (`moco.cybozu.com/password-rotation-restart`) to trigger rolling restart.
Since `MySQLClusterReconciler` uses server-side apply with its own field manager, the annotation set by a different field manager is preserved.

## Deletion Handling

### CR Deletion During Rotation

In normal operation the CR is never deleted (it is long-lived).
However, if a user deletes it while rotation is in progress (e.g. for emergency recovery), MySQL may be left in an inconsistent state (some instances with dual passwords, pending passwords in the source Secret).

The CredentialRotation CR does **not** use a finalizer for automatic rollback, because:
- Rollback requires connecting to every MySQL instance, which may not be possible during deletion (e.g., if the cluster is being scaled down)
- Partial rollback is worse than no rollback — it is safer to leave the state for manual inspection

See [Recovery Procedures](#recovery-procedures) for the steps to restore a consistent state after deletion.

### MySQLCluster Deletion

OwnerReference ensures the CredentialRotation CR is garbage-collected when the MySQLCluster is deleted. No special handling is needed because the MySQL instances are also being destroyed.

## Assumptions

- No MOCO system user has a dual password when rotation starts.
  The ClusterManager checks this at Phase=Rotating using `HasDualPassword` across all instances and all users.
  If a stale dual password is found, the ClusterManager waits (emitting a `DualPasswordExists` Warning Event) instead of proceeding.
  See [Recovery: Dual Password Exists While No Active Rotation](#dual-password-exists-while-no-active-rotation).
- MySQL version is 8.0.14+ (dual password support).

## Security Considerations

- `RotateUserPassword`, `DiscardOldPassword`, and `MigrateUserAuthPlugin` interpolate user names directly into SQL (MySQL does not support placeholders for `ALTER USER`). User names are always from the fixed constants in `pkg/constants/users.go`.
- `MigrateUserAuthPlugin` interpolates the plugin name into `IDENTIFIED WITH`. The value is validated against `^[a-zA-Z0-9_]+$` and derived from `@@global.authentication_policy`, never from user input.
- All ALTER USER operations use `SET sql_log_bin=0` via a dedicated `db.Conn` to prevent cross-cluster propagation.

## Recovery Procedures

All recovery procedures follow the same principle: **reset MySQL passwords back to the current (old) values known to the controller**.
The key insight is that `ALTER USER ... IDENTIFIED BY` (without RETAIN) sets the primary password and clears any secondary, restoring MySQL to a clean single-password state.

### How to Reset MySQL Passwords

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

**Why this needs MySQL cleanup:**
The interrupted rotation may have partially executed RETAIN on some instances.
Without cleanup, a new rotation would see `HasDualPassword=true` on those instances, skip RETAIN, and leave them with stale passwords — causing connectivity loss after DISCARD.

**Recovery order: delete CR → clean Secret → rollout → MySQL → recreate CR**

This ordering is critical. At this point, some Pods may be running with new (pending) passwords.
MySQL still accepts both via dual password.
If MySQL were reset first, Pods with new passwords would immediately lose connectivity.
By deleting the CR and cleaning the Secret first, reconcileV1Secret is unblocked to re-distribute old passwords before MySQL clears the dual-password state.

```console
# Step 1: Delete the CredentialRotation CR.
# This unblocks reconcileV1Secret to re-distribute old passwords.
$ kubectl delete credentialrotation my-cluster

# Step 2: Clean the source Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all pending password keys (e.g. ADMIN_PASSWORD_PENDING) and ROTATION_ID

# Step 3: Restart Pods to pick up old passwords.
# EnvFrom values are only read at container startup, so a rollout restart is required.
$ kubectl -n <namespace> rollout restart statefulset <cluster-name>
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 4: Reset MySQL passwords on all instances.
# See "How to Reset MySQL Passwords" above.

# Step 5: Recreate the CR to retry rotation.
# For GitOps: ArgoCD will recreate the CR automatically from Git.
# For imperative use:
$ kubectl moco credential rotate <cluster-name>
```

### Missing Pending Passwords During Discard

**Symptom:** Warning Event `MissingRotationPending`

**Cause:** The source Secret lost its pending keys (manual edit, restore from backup, etc.) while the CredentialRotation CR still shows Phase=Rotated.

**Why this is dangerous:**
At Phase=Rotated, all instances hold dual passwords and Pods may be using the pending passwords.
The pending passwords are irrecoverable (MySQL stores only hashes).
Without recovery, dual-password state persists indefinitely and blocks future rotations.

**Recovery:** Same as stale pending passwords — delete CR → clean Secret → restart Pods → reset MySQL → recreate CR.

```console
# Step 1: Delete the CredentialRotation CR.
$ kubectl delete credentialrotation my-cluster

# Step 2: Clean any remaining pending keys from the source Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete any remaining pending password keys (e.g. ADMIN_PASSWORD_PENDING) and ROTATION_ID

# Step 3: Restart Pods to pick up old passwords.
$ kubectl -n <namespace> rollout restart statefulset <cluster-name>
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 4: Reset MySQL passwords on all instances.
# See "How to Reset MySQL Passwords" above.

# Step 5: Recreate the CR to retry rotation.
# For GitOps: ArgoCD will recreate the CR automatically from Git.
# For imperative use:
$ kubectl moco credential rotate <cluster-name>
```

### Dual Password Exists While No Active Rotation

**Symptom:** Warning Event `DualPasswordExists`

**Cause:** A MOCO system user has `additional_password` set while no rotation is in progress (Phase is `""` or `Completed`).
This happens when a previous recovery didn't fully clear MySQL's dual-password state, or someone ran `ALTER USER ... RETAIN CURRENT PASSWORD` manually.

**Why DISCARD OLD PASSWORD must not be used:**
After RETAIN, the primary is the new (potentially unknown) password and the secondary is the old (known) password.
DISCARD removes the secondary, leaving only the unknown primary — breaking connectivity.

**Recovery:** No CR deletion or Secret cleanup needed (already clean).

```console
# Step 1 (recommended): Verify Pods can connect with current credentials.

# Step 2 (recommended): Wait for any in-progress rollout.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 3: Reset MySQL passwords on all instances.
# See "How to Reset MySQL Passwords" above.

# After recovery, retry: kubectl moco credential rotate <name>
```

## Impact Summary

| Category | Files |
|---|---|
| **New** | `api/v1beta2/credentialrotation_types.go`, `controllers/credentialrotation_controller.go`, `clustering/password_rotation.go`, validation webhook |
| **New (CLI)** | `cmd/kubectl-moco/cmd/credential.go` (rotate / discard / show subcommands) |
| **New (DB ops)** | `pkg/password/rotation.go`, `pkg/dbop/password.go` |
| **Modified** | `controllers/mysqlcluster_controller.go` (guard in `reconcileV1Secret`), `cmd/moco-controller/cmd/run.go` (register new reconciler) |
