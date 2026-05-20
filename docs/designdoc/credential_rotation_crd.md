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

Rotation state is exposed via three orthogonal status conditions (see [Conditions](#conditions) for the canonical definitions):

- **`Rotating`** — `True` while a cycle is being driven through its sub-steps. The current sub-step is exposed via `Reason`.
- **`OldPasswordRetained`** — `True` while MySQL holds a dual-password set (between successful RETAIN and successful DISCARD).
- **`Ready`** — `True` when the latest spec generations have been fully reconciled.

```
  User bumps spec.rotationGeneration
               |
               v
  +--- Rotate operation -------------------------------------------+
  |                                                                |
  |  Ready=True  /  no conditions yet                              |
  |    | CredentialRotationReconciler:                             |
  |    | generate pending passwords                                |
  |    v                                                           |
  |  Rotating=True (Reason=ApplyingRetain)                         |
  |    | ClusterManager:                                           |
  |    | ALTER USER RETAIN on all instances                        |
  |    v                                                           |
  |  Rotating=True (Reason=DistributingPassword)                   |
  |  OldPasswordRetained=True                                      |
  |    | CredentialRotationReconciler:                             |
  |    | distribute Secrets + rolling restart                      |
  |    v                                                           |
  |  Rotating=True (Reason=AwaitingDiscard)                        |
  |  OldPasswordRetained=True                                      |
  +----+-----------------------------------------------------------+
       |
       v
  Operator verifies apps work with new passwords
       |
  kubectl moco credential discard
       |
       v
  +--- Discard operation ------------------------------------------+
  |                                                                |
  |  Rotating=True (Reason=WaitingForRollout)                      |
  |  OldPasswordRetained=True                                      |
  |    | CredentialRotationReconciler:                             |
  |    | wait for StatefulSet rollout                              |
  |    v                                                           |
  |  Rotating=True (Reason=ApplyingDiscard)                        |
  |  OldPasswordRetained=True                                      |
  |    | ClusterManager:                                           |
  |    | DISCARD OLD PASSWORD + auth plugin migration              |
  |    v                                                           |
  |  Rotating=True (Reason=Finalizing)                             |
  |  OldPasswordRetained=False (Reason=Discarded)                  |
  |    | CredentialRotationReconciler:                             |
  |    | confirm Secret                                            |
  |    v                                                           |
  |  Ready=True (Reason=Completed)                                 |
  |  Rotating=False (Reason=Completed)                             |
  |                                                                |
  +----------------------------------------------------------------+
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

### Why Auth Plugin Migration Happens After DISCARD?

MySQL Error 3894 prevents changing the authentication plugin in a `RETAIN CURRENT PASSWORD` statement.
Instead, plugin migration happens after DISCARD using `ALTER USER ... IDENTIFIED WITH <plugin> BY ...`.

The target plugin is determined from `@@global.authentication_policy` on the primary instance:
- If the first element is a concrete plugin name (e.g. `caching_sha2_password`): use it
- If the first element is `*` or empty: default to `caching_sha2_password`

This enables transparent migration from legacy plugins like `mysql_native_password` during rotation.

### Responsibility Split: CredentialRotationReconciler vs ClusterManager

The CredentialRotationReconciler handles **K8s resource operations** (condition transitions, Secret management, rollout checks).
The ClusterManager handles **DB operations** (ALTER USER RETAIN, DISCARD OLD PASSWORD, auth plugin migration, dual password pre-checks).

This follows the existing separation in MOCO where controllers manage K8s objects and the ClusterManager manages MySQL state.
Each sub-step has a clear *driver* — the component that performs work during that sub-step and transitions out of it:

```
ApplyingRetain → DistributingPassword → AwaitingDiscard → WaitingForRollout → ApplyingDiscard → Finalizing → Completed
   ClusterMgr        Reconciler            Reconciler         Reconciler         ClusterMgr      Reconciler   Reconciler
   (RETAIN)          (Secret.Apply)        (wait for user)    (wait rollout)     (DISCARD)       (promote)    (idle)
```

The component that **enters** a sub-step (writes its Reason into `Rotating`) is the one finishing the previous sub-step:
ClusterManager writes `DistributingPassword` and `Finalizing`;
Reconciler writes `ApplyingRetain`, `AwaitingDiscard`, `WaitingForRollout`, `ApplyingDiscard`, and `Completed`.

Sub-step names are the `Reason` values of the `Rotating` condition.

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
  discardGeneration: 0        # Bump to match rotationGeneration to trigger discard
status:
  observedRotationGeneration: 0  # Last completed rotationGeneration
  observedDiscardGeneration: 0   # Last completed discardGeneration
  rotationID: ""                 # UUID for the in-flight rotation cycle
  conditions:                    # See "Conditions" below
    - type: Rotating
      status: "True"
      reason: ApplyingRetain
      message: "ClusterManager is applying RETAIN on instance 1/3."
      lastTransitionTime: "2026-05-20T05:30:00Z"
      observedGeneration: 1
    - type: OldPasswordRetained
      status: "False"
      reason: NotRetained
      lastTransitionTime: "2026-05-20T05:29:50Z"
      observedGeneration: 1
    - type: Ready
      status: "False"
      reason: InProgress
      lastTransitionTime: "2026-05-20T05:29:50Z"
      observedGeneration: 1
```

### Naming Convention

The CredentialRotation resource name **must match** the target MySQLCluster name (same name, same namespace).
This naturally enforces at most one active rotation per cluster and simplifies lookups — both the controller and ClusterManager can find the CR by the cluster name without a separate reference field.

The CR is **long-lived** — it is created once and persists across multiple rotation cycles.
To start a new rotation, the user increments `spec.rotationGeneration`.
The controller compares `spec.rotationGeneration` with `status.observedRotationGeneration` to detect new rotation requests, and similarly compares `spec.discardGeneration` with `status.observedDiscardGeneration` to detect discard requests.

Both `rotationGeneration` and `discardGeneration` are monotonically increasing counters. The invariant `0 <= discardGeneration <= rotationGeneration` is enforced by the validating webhook: you cannot discard a rotation that has not been performed, and you cannot revert either counter. This makes every spec change idempotent (applying the same spec twice is a no-op) and GitOps-friendly — there are no edge-triggered booleans to flip back and forth.

### OwnerReference

CredentialRotation sets an ownerReference to the target MySQLCluster.
This ensures garbage collection when the MySQLCluster is deleted.

### Conditions

The Kubernetes API convention discourages `phase`-style state-machine enums on new APIs ([rationale](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties)) because adding new enum values is a breaking change and a single field conflates orthogonal observations.
`CredentialRotation` exposes its state as three orthogonal `metav1.Condition` entries instead.

| Type | When `True` | When `False` |
|---|---|---|
| `Rotating` | A cycle is in flight (progressing normally, blocked by `replicas=0`, or stuck on an inconsistent source Secret). The current sub-step is exposed via `Reason`. | No in-flight cycle — initial state, the previous cycle has completed, or the latest rotation request was refused before any MySQL/Secret side effects. |
| `OldPasswordRetained` | MySQL holds a dual-password set for the MOCO system users (between successful RETAIN and successful DISCARD). | No dual-password state in MySQL. |
| `Ready` | `observedRotationGeneration == spec.rotationGeneration` AND `observedDiscardGeneration == spec.discardGeneration`. The latest user request has been fully reconciled into MySQL and Secrets. | A rotation/discard request is still in flight, or has not been issued. |

> **`Ready=True` requires both rotate AND discard to complete.** `observedRotationGeneration` and `observedDiscardGeneration` are updated together at the end of the cycle, when the reconciler transitions `Rotating` from `Finalizing` to `Completed`. While the CR is stable at `Rotating=True (AwaitingDiscard)` waiting for the operator to bump `discardGeneration`, `Ready` is `False`: the cycle is intentionally half-done (MySQL still holds dual passwords) and the CR is not in a stable terminal state.

The "current step" of the workflow is exposed as the `Reason` field of the `Rotating` condition.
`Reason` is an open-ended string by K8s convention — adding new reasons is **not** a breaking change.

#### Reason values

**`Rotating`** (when `Status=True` — a cycle is in flight, either progressing normally or stuck):

The **Driver** column indicates which component performs work *during* that sub-step (and is therefore responsible for transitioning out of it). The component that *sets the Reason on entry* may differ — see [Responsibility Split](#responsibility-split-credentialrotationreconciler-vs-clustermanager) for the handoffs.

| Reason | Driver | Meaning |
|---|---|---|
| `ApplyingRetain` | ClusterManager | `ALTER USER ... RETAIN CURRENT PASSWORD` is being executed on all instances. |
| `DistributingPassword` | Reconciler | RETAIN finished; pending passwords are being distributed to per-namespace Secrets and a rolling restart is being triggered. |
| `AwaitingDiscard` | Reconciler | Pending passwords distributed and rollout complete. Waiting for the operator to bump `discardGeneration`. |
| `WaitingForRollout` | Reconciler | `discardGeneration` bumped; waiting for any in-flight StatefulSet rollout to finish before DISCARD. |
| `ApplyingDiscard` | ClusterManager | `DISCARD OLD PASSWORD` and auth plugin migration are being executed. |
| `Finalizing` | Reconciler | DISCARD finished; pending passwords are being promoted to current in the source Secret. |
| `RotationBlocked` | ClusterManager / Reconciler | Cycle started but cannot progress (e.g. cluster scaled to 0 mid-cycle). Pending passwords exist in the source Secret; manual recovery may be needed. |
| `StalePending` | Reconciler | Source Secret has inconsistent pending state (rotation ID mismatch, partial keys). Manual cleanup required. |

**`Rotating`** (when `Status=False` — no in-flight cycle):

| Reason | Meaning |
|---|---|
| `NotStarted` | The CR has not yet started any cycle (initial state). |
| `Completed` | The latest cycle finished successfully. |
| `RotationRefused` | A rotation could not start (e.g. `cluster.Spec.Replicas == 0`). Nothing has been mutated in MySQL or the source Secret. |

> **Event-only reasons** (not condition `Reason` values): `DiscardRefused`, `DualPasswordExists`, `InconsistentState`, `MissingRotationPending`. These are emitted as Kubernetes Events for visibility while the condition itself stays at the appropriate in-flight Reason (e.g. `AwaitingDiscard` for `DiscardRefused`).

**`OldPasswordRetained`**:

| Status | Reason | Meaning |
|---|---|---|
| `False` | `NotRetained` | No RETAIN has been issued in the current cycle (initial state). |
| `True` | `Retained` | RETAIN succeeded on all instances. |
| `False` | `Discarded` | DISCARD succeeded on all instances. |

**`Ready`**:

| Status | Reason | Meaning |
|---|---|---|
| `False` | `NotStarted` | No cycle has been started. |
| `False` | `InProgress` | A cycle is in flight. |
| `True` | `Completed` | The latest cycle finished successfully. |

#### Step matrix (cross-reference)

The combination of conditions uniquely identifies the workflow state:

| Sub-step | `Rotating` | `OldPasswordRetained` | `Ready` |
|---|---|---|---|
| Initial | False / NotStarted | False / NotRetained | False / NotStarted |
| Applying RETAIN | True / ApplyingRetain | False / NotRetained | False / InProgress |
| Distributing password | True / DistributingPassword | True / Retained | False / InProgress |
| Awaiting discard trigger | True / AwaitingDiscard | True / Retained | False / InProgress |
| Waiting for rollout | True / WaitingForRollout | True / Retained | False / InProgress |
| Applying DISCARD | True / ApplyingDiscard | True / Retained | False / InProgress |
| Finalizing | True / Finalizing | False / Discarded | False / InProgress |
| Completed | False / Completed | False / Discarded | True / Completed |

Out-of-band states (cycle either never started, was refused, or got stuck):

| State | `Rotating` | `OldPasswordRetained` | `Ready` |
|---|---|---|---|
| Rotation refused at fresh CR (replicas=0, first attempt) | False / RotationRefused | False / NotRetained | False / NotStarted |
| Rotation refused after a previous completed cycle | False / RotationRefused | False / Discarded | False / InProgress |
| Rotation blocked (replicas=0 mid-cycle) | True / RotationBlocked | False/NotRetained or True/Retained (depending on whether RETAIN had completed) | False / InProgress |
| Stale pending state (Secret inconsistent) | True / StalePending | True / Retained (typical — RETAIN has run) | False / InProgress |

#### Cycle re-entry

When a new cycle begins (the Reconciler picks up `spec.rotationGeneration > status.observedRotationGeneration` while idle), all three conditions are updated together in the same `Status().Update()`:

- `Rotating`: `False/Completed` (or `False/RotationRefused`, or absent) → `True/ApplyingRetain`
- `OldPasswordRetained`: `False/Discarded` (or `False/NotRetained`, or absent) → `False/NotRetained` (`Reason` reset; `Status` unchanged so `LastTransitionTime` is preserved by `apimeta.SetStatusCondition`)
- `Ready`: `True/Completed` (or `False/NotStarted`, or absent) → `False/InProgress`

`observedRotationGeneration` and `observedDiscardGeneration` are **not** reset — they continue to reflect the last completed cycle until `Finalizing` → `Completed` updates them to match the new spec.
The CR's `metadata.generation` (and therefore `metav1.Condition.observedGeneration`) bumps automatically on every spec change; controllers stamp the current `metadata.generation` into each condition they write.

#### Why three conditions, not six?

A more fine-grained decomposition (one condition per sub-step) was considered but rejected.
The sub-steps within a cycle are sequential, not independently observable — splitting them into multiple boolean conditions would require explicit resets whenever a new cycle begins, and the resulting status would be harder to read at a glance.
Three conditions with sub-steps surfaced via `Reason` keeps the orthogonal facts orthogonal (cycle active? dual-password retained? request reconciled?) while keeping workflow detail discoverable via `kubectl describe` / `kubectl get -o yaml`.

### Go Type Definitions

```go
// api/v1beta2/credentialrotation_types.go

// CredentialRotationSpec defines the desired state of CredentialRotation.
// The target MySQLCluster is identified by the CR's own name and namespace
// (CredentialRotation name must equal MySQLCluster name).
type CredentialRotationSpec struct {
    // RotationGeneration is a monotonically increasing counter.
    // Incrementing this value triggers a new rotation cycle.
    // +kubebuilder:validation:Minimum=1
    // +required
    RotationGeneration int64 `json:"rotationGeneration"`

    // DiscardGeneration is a monotonically increasing counter that triggers
    // the discard step. Must satisfy 0 <= discardGeneration <= rotationGeneration.
    // Bumping this value (typically to match rotationGeneration) signals the
    // controller to discard the retained old password from the previous
    // rotation. The bump is only honored when the Rotating condition's
    // Reason is AwaitingDiscard.
    // +kubebuilder:default=0
    // +kubebuilder:validation:Minimum=0
    // +optional
    DiscardGeneration int64 `json:"discardGeneration"`
}

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
    // Conditions represent the latest available observations of the
    // rotation state. See the "Conditions" section of the design doc for
    // canonical Type/Reason definitions.
    // +listType=map
    // +listMapKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

    // RotationID is the UUID for the in-flight rotation cycle.
    // Empty when no cycle is active.
    // +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$`
    // +optional
    RotationID string `json:"rotationID,omitempty"`

    // ObservedRotationGeneration is the last rotationGeneration
    // that completed successfully.
    // +kubebuilder:default=0
    // +kubebuilder:validation:Minimum=0
    // +optional
    ObservedRotationGeneration int64 `json:"observedRotationGeneration"`

    // ObservedDiscardGeneration is the last discardGeneration
    // that completed successfully.
    // +kubebuilder:default=0
    // +kubebuilder:validation:Minimum=0
    // +optional
    ObservedDiscardGeneration int64 `json:"observedDiscardGeneration"`
}

// Condition type constants.
const (
    ConditionRotating            = "Rotating"
    ConditionOldPasswordRetained = "OldPasswordRetained"
    ConditionReady               = "Ready"
)

// Reason constants for the Rotating condition.
// In-flight sub-step reasons (used with Status=True):
const (
    ReasonApplyingRetain       = "ApplyingRetain"
    ReasonDistributingPassword = "DistributingPassword"
    ReasonAwaitingDiscard      = "AwaitingDiscard"
    ReasonWaitingForRollout    = "WaitingForRollout"
    ReasonApplyingDiscard      = "ApplyingDiscard"
    ReasonFinalizing           = "Finalizing"
    ReasonRotationBlocked      = "RotationBlocked"
    ReasonStalePending         = "StalePending"
)

// Idle reasons (used with Status=False):
const (
    ReasonNotStarted      = "NotStarted"
    ReasonCompleted       = "Completed"
    ReasonRotationRefused = "RotationRefused"
)

// Reason constants for the OldPasswordRetained condition.
const (
    ReasonNotRetained = "NotRetained"
    ReasonRetained    = "Retained"
    ReasonDiscarded   = "Discarded"
)

// Reason constants for the Ready condition reuse ReasonNotStarted /
// ReasonCompleted, plus ReasonInProgress for the in-flight state.
const (
    ReasonInProgress = "InProgress"
)
```

Controller code reads the current sub-step via a small helper:

```go
// currentStep returns the Reason of the Rotating condition if it is True,
// otherwise the empty string.
func currentStep(cr *mocov1beta2.CredentialRotation) string {
    cond := apimeta.FindStatusCondition(cr.Status.Conditions, mocov1beta2.ConditionRotating)
    if cond == nil || cond.Status != metav1.ConditionTrue {
        return ""
    }
    return cond.Reason
}
```

### Validation Webhook

```go
func (r *CredentialRotation) ValidateCreate(ctx context.Context, ...) {
    // 1. MySQLCluster with the same name must exist in the same namespace
    // 2. MySQLCluster replicas must be > 0
    // 3. rotationGeneration must be > 0
    // 4. 0 <= discardGeneration <= rotationGeneration
}

func (r *CredentialRotation) ValidateUpdate(ctx context.Context, ...) {
    // 1. rotationGeneration must be >= old value (monotonically increasing)
    // 2. discardGeneration must be >= old value (monotonically increasing)
    // 3. discardGeneration must be <= rotationGeneration
    // 4. rotationGeneration can only increase when the CR is idle:
    //      isIdle(cr) := (no Rotating condition) ||
    //                    (Rotating.Status=False AND
    //                     Rotating.Reason ∈ {NotStarted, Completed, RotationRefused})
    //    A previously-stuck cycle (Status=True with Reason=RotationBlocked /
    //    StalePending) must be cleared via the recovery procedure (delete +
    //    recreate) before a new rotationGeneration can be requested.
    // 5. discardGeneration can only increase when Rotating.Status=True and
    //    Rotating.Reason=AwaitingDiscard (and rotationGeneration is unchanged
    //    in the same update).
}

func (r *CredentialRotation) ValidateDelete(ctx context.Context, ...) {
    // Allow delete in the following cases:
    //   - The CR is idle (see isIdle() above) — no cycle in flight
    //   - The CR is stuck in a state that requires the recovery procedure:
    //     Rotating.Status=True AND Rotating.Reason ∈ {RotationBlocked, StalePending}.
    //     These reasons indicate the cycle cannot progress on its own; the
    //     documented Recovery Procedures begin with "delete CR", so the
    //     webhook must allow it.
    //   - The owning MySQLCluster is NotFound (GC after owner deletion)
    //   - The owning MySQLCluster has DeletionTimestamp set
    //     (unblock cluster termination — blockOwnerDeletion=true would
    //      otherwise stall the cluster in Terminating)
    //   - The CR carries a stale MySQLCluster ownerRef whose UID does not
    //     match the live cluster (recreated cluster; see Stale CR Handling)
    // Otherwise: forbid (a delete while the cycle is actively progressing
    // would abandon the workflow).
}
```

## User Interface

```console
# Rotate: create CR (first time) or bump rotationGeneration
$ kubectl moco credential rotate <cluster-name>

# Check status. STEP is the Rotating condition's Reason (e.g. ApplyingRetain,
# AwaitingDiscard, Completed, RotationRefused, RotationBlocked, StalePending);
# RETAINED is the OldPasswordRetained condition status; READY is the Ready
# condition status.
$ kubectl get credentialrotation <cluster-name>
NAME         STEP              RETAINED   READY   ROTATIONGEN   OBSERVEDROTATION   DISCARDGEN   OBSERVEDDISCARD   AGE
my-cluster   AwaitingDiscard   True       False   1             0                  0            0                 5m

# Wait for a rotation cycle to finish (status conditions integrate with
# kubectl's standard --for=condition syntax).
$ kubectl wait --for=condition=Ready credentialrotation/<cluster-name>

# Inspect detailed sub-step messages.
$ kubectl describe credentialrotation <cluster-name>

# Discard: bump discardGeneration to match rotationGeneration
$ kubectl moco credential discard <cluster-name>

# Show current credentials
$ kubectl moco credential show <cluster-name>
```

### kubectl moco behavior

| Command | Action |
|---------|--------|
| `credential rotate` | If CR does not exist: create with `rotationGeneration: 1`. If CR exists: refuse if the CR is stale (MySQLCluster ownerRef UID does not match the live cluster), require the CR to be idle (no `Rotating` condition, or `Rotating.Status=False`), then increment `rotationGeneration`. |
| `credential discard` | Refuse if the CR is stale; require `Rotating.Status=True` with `Reason=AwaitingDiscard` → patch `spec.discardGeneration` to match `spec.rotationGeneration` |
| `credential show` | Read per-namespace user Secret |

The CLI validates preconditions (MySQLCluster with the same name exists, replicas > 0, no in-progress rotation, CR is not a leftover from a deleted-and-recreated cluster).

Users can also interact with the CR directly via `kubectl`:

```console
# First rotation
$ kubectl apply -f credential-rotation.yaml
# credential-rotation.yaml:
#   spec:
#     rotationGeneration: 1
#     discardGeneration: 0

# Trigger discard (matches the current rotationGeneration)
$ kubectl patch credentialrotation my-cluster --type=merge \
    -p '{"spec":{"discardGeneration":1}}'

# Start next rotation (after previous one completed)
$ kubectl patch credentialrotation my-cluster --type=merge \
    -p '{"spec":{"rotationGeneration":2}}'
```

### GitOps / ArgoCD

The CR is long-lived and purely declarative, so it works naturally with GitOps:

```yaml
# 1. First rotation: commit this manifest
spec:
  rotationGeneration: 1
  discardGeneration: 0

# 2. After verifying apps work: update and commit
spec:
  rotationGeneration: 1
  discardGeneration: 1

# 3. Next rotation: update and commit
spec:
  rotationGeneration: 2
  discardGeneration: 1
```

Each Git commit triggers an ArgoCD sync that advances the rotation lifecycle.
No imperative `kubectl` commands or CR deletion is required.

**Do not mix GitOps with `kubectl moco credential rotate/discard`.**
The CLI patches the same spec fields that GitOps manages. If the CLI bumps a counter, GitOps reconcile will try to roll it back, but the validating webhook rejects any decrease — leaving the resource permanently `OutOfSync`. Worse, the CLI-triggered rotation/discard already mutates MySQL passwords irreversibly. Pick one source of truth per environment.

## Rotate

### CredentialRotationReconciler: idle → ApplyingRetain

Triggered when `spec.rotationGeneration > status.observedRotationGeneration` and the CR is idle (no `Rotating` condition, or `Rotating.Status=False`).

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Generate UUID as rotationID | - | Reconciler |
| 2 | Generate pending passwords (e.g. `ADMIN_PASSWORD_PENDING`) in the source Secret | Secret.Update | Reconciler |
| 3 | Set conditions: `Rotating=True (ApplyingRetain)`, `OldPasswordRetained=False (NotRetained)`, `Ready=False (InProgress)`, and the rotationID | Status.Update | Reconciler |

### ClusterManager: ApplyingRetain → DistributingPassword

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Pre-check: scan all instances for pre-existing dual passwords. Wait if found. Skip if `RETAIN_STARTED` marker is set (crash recovery). | - | ClusterManager |
| 1b | Set `RETAIN_STARTED` marker (rotationID) in source Secret | Secret.Update | ClusterManager |
| 2 | For each instance: execute `ALTER USER ... RETAIN CURRENT PASSWORD` with `sql_log_bin=0` | MySQL | ClusterManager |
| 3 | Set conditions: `Rotating=True (DistributingPassword)`, `OldPasswordRetained=True (Retained)` | Status.Update | ClusterManager |

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

### CredentialRotationReconciler: DistributingPassword → AwaitingDiscard

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Distribute pending passwords to per-namespace Secrets (user Secret + my.cnf Secret) | Secret.Apply | Reconciler |
| 2 | Add restart annotation (`moco.cybozu.com/password-rotation-restart: <rotationID>`) to StatefulSet Pod template. The value is the rotationID (UUID) to ensure each rotation triggers a new rollout. | StatefulSet.Apply (SSA, dedicated field manager + ForceOwnership) | Reconciler |
| 3 | Set conditions: `Rotating=True (AwaitingDiscard)` | Status.Update | Reconciler |
| 4 | Rolling restart (Pods pick up new passwords via `EnvFrom`) | - | StatefulSet controller |

**Scaled-down clusters (replicas=0):**
Rotation is refused at multiple points:
- The validation webhook rejects CR creation or `rotationGeneration` bump when `cluster.Spec.Replicas <= 0`.
- `handleStartRotation` (idle → `ApplyingRetain`) emits a `RotationRefused` Warning Event and sets `Rotating=False (RotationRefused)`. Nothing has been mutated yet, so the CR remains idle.
- If the cluster is scaled down to 0 **after** rotation has reached `ApplyingRetain` (i.e. pending passwords were already written to the source Secret), the ClusterManager step handler emits a `RotationBlocked` Warning Event and transitions `Rotating` to `True (RotationBlocked)`. The `True` status reflects that the cycle is in flight and has visible side effects; recovery requires either scaling the cluster back up (the reconciler resumes automatically when it sees a healthy cluster again) or running the recovery procedure to clean up the pending state.

Without running instances, ALTER USER cannot execute, and distributing new passwords would break connectivity when the cluster scales back up.

## Discard

### CredentialRotationReconciler: AwaitingDiscard → WaitingForRollout → ApplyingDiscard

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Validate `spec.discardGeneration > status.observedDiscardGeneration` | - | Reconciler |
| 2 | Refuse with a `DiscardRefused` Warning Event if `cluster.Spec.Replicas <= 0` (keeps `Rotating.Reason=AwaitingDiscard`; advancing to `ApplyingDiscard` would wedge because the webhook forbids reverting `discardGeneration` once bumped) | - | Reconciler |
| 3 | Set conditions: `Rotating=True (WaitingForRollout)` | Status.Update | Reconciler |
| 4 | Wait for StatefulSet rollout to complete | - | Reconciler |
| 5 | Set conditions: `Rotating=True (ApplyingDiscard)` | Status.Update | Reconciler |

**Why wait for the rollout?**
The agent sidecar reads passwords from `EnvFrom`, evaluated only at Pod startup.
If old Pods are still running when DISCARD executes, they lose connectivity.
The gate checks: ObservedGeneration, CurrentRevision == UpdateRevision, UpdatedReplicas == Replicas, ReadyReplicas == Replicas.
While waiting, the handler returns `RequeueAfter: 15s` to avoid log spam.

### ClusterManager: ApplyingDiscard → Finalizing

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Determine target auth plugin via `GetAuthPlugin` on the primary | MySQL (read-only) | ClusterManager |
| 2 | For each instance: execute `DISCARD OLD PASSWORD` and auth plugin migration with `sql_log_bin=0` | MySQL | ClusterManager |
| 3 | Set conditions: `Rotating=True (Finalizing)`, `OldPasswordRetained=False (Discarded)` | Status.Update | ClusterManager |

**Why connect with the pending password?**
DISCARD removes the old password.
If we connected with the old password, the connection would become invalid immediately after DISCARD succeeds.
Using the pending password also implicitly verifies that distribution was successful.

**Scaled-down clusters (replicas=0):**
Discard is rejected with a Warning Event (`DiscardRefused`) and `RequeueAfter: 15s`.
The operator should scale the cluster up first.

### CredentialRotationReconciler: Finalizing → Completed

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Promote pending passwords to current in the source Secret via `password.ConfirmPendingPasswords()` | Secret.Update | Reconciler |
| 2 | Set conditions: `Rotating=False (Completed)`, `Ready=True (Completed)`. Set `observedRotationGeneration = spec.rotationGeneration` and `observedDiscardGeneration = spec.discardGeneration` | Status.Update | Reconciler |

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
RETAIN_STARTED:         <uuid>      # only during the ApplyingRetain step (crash-safety marker)
```

`HasPendingPasswords` validates that all 8 pending keys and `ROTATION_ID` are present and that the rotation ID matches the expected value.

### Why Embed Pending Passwords in the Source Secret?

An alternative is to store pending passwords in a separate Secret (owned by the CredentialRotation CR).
Pending passwords are embedded in the source Secret instead, for the following reasons:

1. **Crash safety of the confirm step** — `ConfirmPendingPasswords` (`Finalizing` → `Completed`) promotes pending passwords to current by renaming keys within a single Secret. This is a single-object update. With a separate Secret, the confirm step would need to copy data between two Secrets. If the controller crashes between reading the pending Secret and writing the source Secret, and the pending Secret is subsequently lost (accidental deletion, failed GC), the new passwords become irrecoverable.

2. **Simpler failure modes** — With a single Secret, the only question on crash recovery is "did the update succeed?" With two Secrets, every sub-step must consider whether the two objects are consistent with each other.

3. **`SetPendingPasswords` and `ConfirmPendingPasswords` are naturally idempotent** — Both operate on a single object. `SetPendingPasswords` checks if pending keys with the matching rotation ID already exist; `ConfirmPendingPasswords` is a no-op when no pending keys remain. This idempotency would be harder to guarantee across two objects.

## Component Details

### CredentialRotationReconciler (new)

For sub-steps where the ClusterManager drives progress (`ApplyingRetain`, `ApplyingDiscard`), the Reconciler requeues every 15 seconds to check for condition advancement.

### ClusterManager

Handles MySQL-level operations by reading the CredentialRotation CR to determine the current sub-step.

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

    switch currentStep(&cr) {
    case mocov1beta2.ReasonApplyingRetain:
        return p.handleApplyingRetain(ctx, ss, &cr)
    case mocov1beta2.ReasonApplyingDiscard:
        return p.handleApplyingDiscard(ctx, ss, &cr)
    default:
        return false, nil
    }
}
```

The DB operation logic (`rotateInstanceUsers`, `discardInstanceUsers`, `checkInstanceDualPasswords`) lives in `clustering/password_rotation.go` and reads/writes the CredentialRotation conditions for sub-step transitions.

### MySQLClusterReconciler

The only change to `MySQLClusterReconciler` is in `reconcileV1Secret`: it chooses which password set (current vs pending) to distribute based on the CredentialRotation sub-step, and continues to **self-heal** the per-namespace Secrets in every sub-step except `DistributingPassword`.

```go
func (r *MySQLClusterReconciler) reconcileV1Secret(ctx context.Context, ...) error {
    // ... source Secret creation (unchanged) ...

    // During credential rotation, the CredentialRotationReconciler owns secret
    // distribution from the DistributingPassword sub-step onward. Choose which
    // password to distribute based on the Rotating condition's Reason:
    //   - no Rotating condition / ApplyingRetain / Completed-or-similar terminal:
    //     current passwords (pending not yet distributed).
    //   - DistributingPassword: skip; the rotation reconciler is the writer.
    //   - AwaitingDiscard / WaitingForRollout / ApplyingDiscard / Finalizing:
    //     pending passwords (already distributed by the rotation reconciler).
    //     Re-applying here self-heals if the per-namespace user/my.cnf Secret
    //     was deleted during rotation; apply() is a no-op when content matches.
    // Transient lookup errors must NOT silently fall back to current passwords:
    // doing so would overwrite already-distributed pending credentials and break
    // the rolling restart / discard flow. Only NotFound and NoMatch (CRD not
    // installed) are treated as "no active rotation".
    usePending := false
    var cr mocov1beta2.CredentialRotation
    switch err := r.Get(ctx, client.ObjectKey{
        Namespace: cluster.Namespace,
        Name:      cluster.Name,
    }, &cr); {
    case err == nil:
        // Ignore a stale CR whose ownerReference UID does not match the live
        // cluster (cluster was deleted and recreated under the same name).
        if !crBelongsToCluster(&cr, cluster) {
            break
        }
        switch currentStep(&cr) {
        case mocov1beta2.ReasonDistributingPassword:
            return nil
        case mocov1beta2.ReasonAwaitingDiscard,
            mocov1beta2.ReasonWaitingForRollout,
            mocov1beta2.ReasonApplyingDiscard,
            mocov1beta2.ReasonFinalizing:
            usePending = true
        }
    case apierrors.IsNotFound(err), meta.IsNoMatchError(err):
        // No active rotation — proceed with current passwords.
    default:
        return fmt.Errorf("failed to get CredentialRotation: %w", err)
    }

    passwd, err := passwordForDistribution(secret, usePending)
    // ... apply user Secret and my.cnf Secret using passwd ...
}
```

`passwordForDistribution` returns the pending `MySQLPassword` when `preferPending` is true **and** the source Secret's pending state is fully present (validated via `HasPendingPasswords`). When pending keys and `ROTATION_ID` are all absent, it falls back to the current password — this handles the brief window during the `Finalizing` step where pending keys are promoted to current before the conditions are updated to `Ready=True`. If the pending state is *partially* present (some `*_PENDING` keys missing, or `ROTATION_ID` without keys, etc.), it returns an error instead of silently falling back, so the inconsistency surfaces for manual cleanup rather than letting reconciliation overwrite the per-namespace Secrets that the `DistributingPassword` step already populated with the new passwords.

The `r.Get()` reads from the informer cache, which controller-runtime starts on demand for the CredentialRotation GVK.
The overhead is negligible (CredentialRotation objects are few and small).

**Self-healing per-namespace Secrets during rotation:**
Re-applying the pending password during the `AwaitingDiscard`/`WaitingForRollout`/`ApplyingDiscard`/`Finalizing` sub-steps is intentional. If the per-namespace user/my.cnf Secret is accidentally deleted during rotation (manual operation, GC race, etc.), this reconciler restores it from the pending password kept in the source Secret. Pods restarted in that window can still come up with credentials MySQL accepts.

**Cache lag safety:**
If the cache briefly shows a stale `Rotating.Reason`, the guard remains safe:
- `Reason` appears as the empty step / `ApplyingRetain` / a terminal Reason (`Completed`/`NotStarted`/…) → distribute current passwords → harmless (`ApplyingRetain` is intentionally not skipped; pending has not yet been distributed by the rotation reconciler).
- `Reason` appears as `DistributingPassword` → skip → correct (the rotation reconciler is the writer).
- `Reason` appears as `AwaitingDiscard`/`WaitingForRollout`/`ApplyingDiscard`/`Finalizing` → distribute pending passwords → idempotent re-apply.

The only theoretical risk is if the cache does not yet reflect `Reason=DistributingPassword` while the CredentialRotationReconciler has already distributed pending passwords (i.e. the cache is still showing `ApplyingRetain`).
In practice this cannot happen because RETAIN executes on all MySQL instances (takes seconds to minutes), far exceeding the cache propagation delay (~hundreds of milliseconds).

## Crash Safety

### Sub-step Boundary Safety

| Crash Point | Recovery |
|---|---|
| rotationGeneration bumped, pending passwords not yet generated | Reconciler re-generates on next reconcile |
| Pending passwords generated, RETAIN not started | ClusterManager picks up `Rotating.Reason=ApplyingRetain` |
| Pre-check passed, `RETAIN_STARTED` marker set, RETAIN not yet executed | Marker skips pre-check on retry; `HasDualPassword` makes RETAIN idempotent |
| RETAIN partially applied (some instances) | `RETAIN_STARTED` marker skips pre-check; `HasDualPassword` makes re-execution idempotent |
| RETAIN complete, `Reason=DistributingPassword` not yet set | ClusterManager re-runs → all skip → transitions condition |
| `Reason=DistributingPassword`, Secrets not yet distributed | Reconciler distributes on next reconcile |
| `Reason=ApplyingDiscard`, DISCARD not yet executed | ClusterManager picks up the step |
| DISCARD complete, `Reason=Finalizing` not yet set | DISCARD is idempotent → re-run → transitions condition |
| `Reason=Finalizing`, Secret promoted but status not updated | `HasPendingPasswords` returns false; `CurrentPasswordsMatch` verifies promotion succeeded → sets `Ready=True (Completed)` |
| `Reason=Finalizing`, Secret not yet promoted | `ConfirmPendingPasswords` is idempotent |

### HasDualPassword Instead of Per-User Status Tracking

MySQL holds only one secondary password per user.
If RETAIN is re-run with the same pending password after a crash, the pending password (now the primary) moves into the secondary slot, evicting the original password.
The controller can no longer connect.

Instead of tracking per-user progress in Kubernetes status (which can fail independently of MySQL), the ClusterManager queries MySQL directly: if `mysql.user.User_attributes` contains `additional_password`, RETAIN is skipped.
This makes MySQL the source of truth and is safe on re-execution because the query is read-only.

### Idempotency of DISCARD

MySQL rejects `ALTER USER ... DISCARD OLD PASSWORD` when the user has no retained secondary password, so the statement is **not** self-idempotent.
If a partial DISCARD retry re-ran the statement against an already-discarded user, the rotation would wedge in the `ApplyingDiscard` sub-step.

To make retries safe, `discardInstanceUsers` queries `HasDualPassword(user)` before issuing DISCARD and skips users whose retained password is already gone. This mirrors the `HasDualPassword` gating used in `rotateInstanceUsers` for RETAIN, and keeps MySQL itself as the source of truth for per-user state.

### ConfirmPendingPasswords Idempotency

If the controller crashes after updating the Secret but before patching the status, the Secret has already been promoted (pending passwords are now current) but the conditions still show `Rotating.Reason=Finalizing`.
On re-reconcile, `HasPendingPasswords` returns `(false, nil)`.
The Reconciler then verifies crash recovery by comparing the controller Secret's current passwords with the per-namespace user Secret via `CurrentPasswordsMatch`.
If they match, promotion already succeeded — the Reconciler proceeds to set `Rotating=False (Completed)` and `Ready=True (Completed)`.
If they differ (indicating pending keys were lost without promotion), the Reconciler emits an `InconsistentState` Warning Event and requeues instead of completing.

### Stale Rotation Detection

The `rotationGeneration` / `observedRotationGeneration` pair replaces any need for `LastRotationID` tracking.
A new rotation is detected when `spec.rotationGeneration > status.observedRotationGeneration`.
The `rotationID` (UUID) in the source Secret is matched against `status.rotationID` to detect stale pending passwords from a previous interrupted cycle.

### Status Update Conflict Handling

The ClusterManager uses `retry.RetryOnConflict` with `Status().Update()` on the CredentialRotation CR for condition transitions, the same pattern as the current `MySQLCluster` status updates.

## Interaction with Other Reconcile Steps

### reconcileV1Secret

During an active rotation, `reconcileV1Secret` checks for the existence of a CredentialRotation CR (see [MySQLClusterReconciler](#mysqlclusterreconciler)).
After rotation completes (`Ready=True`), the source Secret already contains promoted passwords, so normal distribution resumes correctly.

### GatherStatus and ClusterManager Connection

Unchanged. GatherStatus reads passwords from the per-namespace user Secret.
During rotation, the user Secret always contains passwords that MySQL accepts:
- Step `ApplyingRetain` / `DistributingPassword`: user Secret has old passwords, MySQL accepts old via dual password
- Step `AwaitingDiscard` onwards: new passwords have been distributed to user Secret

The rotation handler reads passwords directly from the source (controller) Secret and creates its own DB connections, independent of GatherStatus.

### StatefulSet Rolling Restart

The CredentialRotationReconciler applies (Server-Side Apply) the StatefulSet Pod template annotation (`moco.cybozu.com/password-rotation-restart`) under a dedicated field manager (`moco-credential-rotation`) with `ForceOwnership` to trigger rolling restart.
The dedicated field manager keeps ownership of the rotation annotation key isolated from `MySQLClusterReconciler`'s `moco-controller`, so the restart trigger is not silently removed by the next MySQLCluster reconcile (which does not declare the rotation annotation in its apply config).
`ForceOwnership` additionally covers the edge case where a user pre-sets the same annotation key in `cluster.Spec.PodTemplate.Annotations` — the field would otherwise be owned by `moco-controller` and the apply would conflict.

## Deletion Handling

### CR Deletion During Rotation

In normal operation the CR is never deleted (it is long-lived).
The validating webhook **forbids** deletion while a rotation is in progress to prevent the workflow from being abandoned mid-flight (which would leave pending/dual passwords behind with no automatic recovery path):

```text
ValidateDelete:
  if CR is idle (no Rotating condition, OR
                 Rotating.Status=False AND
                 Reason ∈ {NotStarted, Completed,
                           RotationRefused})           → allow
  if Rotating.Status=True AND
     Reason ∈ {RotationBlocked, StalePending}          → allow (recovery)
  if MySQLCluster is NotFound                          → allow (GC after owner deletion)
  if MySQLCluster.DeletionTimestamp                    → allow (GC unblock; see "MySQLCluster Deletion")
  if CR has a stale MySQLCluster                       → allow (recreated cluster; see "Stale CR Handling")
     ownerRef (UID mismatch)
  otherwise (actively progressing cycle)               → forbid
```

The `RotationBlocked` / `StalePending` escape hatch is what makes the documented [Recovery Procedures](#recovery-procedures) work — they all begin with `kubectl delete credentialrotation ...`.

If a cycle is *actively progressing* (any other in-flight `Reason`) and the operator must roll back manually, the operator can:
1. Scale the cluster down to 0 (the cycle will transition to `Rotating.Reason=RotationBlocked`, after which delete is allowed), or
2. Wait for the cluster termination to release the GC delete.

The CredentialRotation CR does **not** use a finalizer for automatic rollback, because:
- Rollback requires connecting to every MySQL instance, which may not be possible during deletion (e.g., if the cluster is being scaled down)
- Partial rollback is worse than no rollback — it is safer to leave the state for manual inspection

See [Recovery Procedures](#recovery-procedures) for the steps to restore a consistent state after deletion.

### MySQLCluster Deletion

The ownerReference (with `blockOwnerDeletion=true`) means Kubernetes GC must delete the CR before the MySQLCluster can finish terminating.
The webhook explicitly allows delete when the owning cluster has a non-nil `DeletionTimestamp`, even if the CR's `Rotating` condition is still `True`.
Without that branch the cluster would be stuck in `Terminating` until the rotation finished, which contradicts the user's intent to tear the cluster down.

No special teardown is needed because the MySQL instances are also being destroyed.

### Stale CR Handling (Cluster Recreated Under the Same Name)

If a `MySQLCluster` is deleted and another is recreated under the same name **before** GC reclaims the original CR (or while it is paused for some reason), the leftover CR matches the new cluster by `namespace/name` but its ownerReference points at the old cluster's UID.
Adopting that CR onto the new cluster would let stale rotation state (conditions, pending passwords, restart annotations) poison a fresh cluster. The design treats stale CRs as **invisible** to every component:

| Component | Behavior on stale CR |
|---|---|
| Validating webhook (`ValidateDelete`) | Allow delete (operator/GC can remove it) |
| `CredentialRotationReconciler` | Emit `StaleCredentialRotation` Warning Event and return without adopting (no ownerRef rewrite) |
| `ClusterManager.handlePasswordRotation` | Return early, do not run RETAIN/DISCARD |
| `MySQLClusterReconciler.reconcileV1Secret` | Ignore the CR's conditions; distribute current passwords normally |
| `kubectl moco credential rotate`/`discard` | Refuse with an error instructing the user to delete the stale CR |

"Stale" means: the CR has a `MySQLCluster` ownerReference whose UID does not match the live cluster, and no matching reference. A CR with no `MySQLCluster` ownerReference yet (e.g. just created by `kubectl moco credential rotate` and not yet adopted) is treated as **fresh**, not stale.

## Assumptions

- No MOCO system user has a dual password when rotation starts.
  The ClusterManager checks this during the `ApplyingRetain` step using `HasDualPassword` across all instances and all users.
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

**Cause:** The source Secret lost its pending keys (manual edit, restore from backup, etc.) while the CredentialRotation CR still shows `Rotating.Reason=AwaitingDiscard`.

**Why this is dangerous:**
At `AwaitingDiscard`, all instances hold dual passwords and Pods may be using the pending passwords.
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

**Cause:** A MOCO system user has `additional_password` set while no rotation is in progress (the CR is idle: no `Rotating` condition, or `Rotating.Status=False`).
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
| **New** | `api/v1beta2/credentialrotation_types.go`, `api/v1beta2/credentialrotation_webhook.go` (Create/Update/Delete validation, stale-CR and terminating-cluster GC handling), `controllers/credentialrotation_controller.go`, `clustering/password_rotation.go` |
| **New (CLI)** | `cmd/kubectl-moco/cmd/credential.go` (`rotate`/`discard`/`show` subcommands, stale-CR detection) |
| **New (DB ops)** | `pkg/password/rotation.go`, `pkg/dbop/password.go` (RETAIN/DISCARD/auth plugin migration with per-user `HasDualPassword` idempotency) |
| **Modified** | `controllers/mysqlcluster_controller.go` (`reconcileV1Secret` chooses current vs pending password based on the CR's `Rotating.Reason` and self-heals per-namespace Secrets), `cmd/moco-controller/cmd/run.go` (register new reconciler) |
