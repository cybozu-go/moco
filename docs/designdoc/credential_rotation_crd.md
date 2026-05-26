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

Rotation state is exposed as three Conditions. `RotationReady` and `DiscardReady` are action-availability guards — at most one is True at a time, signalling which operator action is currently allowed. `DualPassword` is the orthogonal physical-state observation.

- **`RotationReady`** — `True` iff the CR is in the idle steady state (`Step()==StepIdle`): no cycle is in flight, no dual password is held, and the operator may bump `spec.rotationGeneration`.
- **`DiscardReady`** — `True` iff the CR is in the awaiting-discard steady state (`Step()==StepAwaitingDiscard`): the rotation phase has finished, **the post-distribute StatefulSet rollout has settled**, MySQL holds a dual-password set, and the operator may bump `spec.discardGeneration`. The rollout gate ensures that every Pod is already using the new password before `DiscardReady=True` is exposed — running `discard` while old Pods still hold the old password could otherwise strip the secondary password out from under them.
- **`DualPassword`** — `True` while MySQL holds a dual-password set (between successful RETAIN and successful DISCARD).

```
  User bumps spec.rotationGeneration
               |
               v
  +--- Rotate operation -------------------------------------------+
  |                                                                |
  |  RotationReady=True, DiscardReady=False,                       |
  |  DualPassword=False  /  conditions absent (fresh CR)           |
  |    | CredentialRotationReconciler:                             |
  |    | generate pending passwords, flip RotationReady→False      |
  |    v                                                           |
  |  RotationReady=False (Pending), DiscardReady=False (Pending),  |
  |  DualPassword=False (NotRetained)                              |
  |    | ClusterManager:                                           |
  |    | ALTER USER RETAIN on all instances                        |
  |    v                                                           |
  |  RotationReady=False, DiscardReady=False,                      |
  |  DualPassword=True (Retained)                                  |
  |    | CredentialRotationReconciler:                             |
  |    | distribute Secrets + rolling restart                      |
  |    | promote observedRotationGeneration                        |
  |    v                                                           |
  |  RotationReady=False (Pending), DiscardReady=False (Pending),  |
  |  DualPassword=True (Retained) ← StepAwaitingRollout            |
  |    | CredentialRotationReconciler:                             |
  |    | watch StatefulSet rollout; when settled,                  |
  |    | flip DiscardReady→True (verification window opens)        |
  |    v                                                           |
  |  RotationReady=False (Pending), DiscardReady=True (Reconciled),|
  |  DualPassword=True (Retained)                                  |
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
  |  spec.discardGeneration bumped; DiscardReady still             |
  |  True (stale), DualPassword=True                               |
  |    | CredentialRotationReconciler:                             |
  |    | flip DiscardReady → False (Pending); emit DiscardStarted  |
  |    v                                                           |
  |  RotationReady=False, DiscardReady=False (Pending),            |
  |  DualPassword=True                                             |
  |    | ClusterManager (blocked on DiscardReady=False/Pending):   |
  |    | DISCARD OLD PASSWORD + auth plugin migration              |
  |    | (rollout already settled in AwaitingRollout, no re-wait)  |
  |    v                                                           |
  |  RotationReady=False, DiscardReady=False,                      |
  |  DualPassword=False (NotRetained)                              |
  |    | CredentialRotationReconciler:                             |
  |    | confirm Secret; promote observedDiscardGeneration         |
  |    | flip RotationReady→True (back to idle)                    |
  |    v                                                           |
  |  RotationReady=True (Reconciled), DiscardReady=False (Pending),|
  |  DualPassword=False                                            |
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

The CredentialRotationReconciler handles **K8s resource operations** (condition transitions, Secret management, StatefulSet rolling-restart annotation).
The ClusterManager handles **DB operations** (ALTER USER RETAIN, DISCARD OLD PASSWORD, auth plugin migration, dual password pre-checks). The post-distribute StatefulSet rollout wait sits in the Reconciler (`AwaitingRollout` step), not ClusterManager — `DiscardReady=True` is gated on rollout completion so by the time the operator can bump `discardGeneration` and ClusterManager picks up DISCARD, every Pod is already running with the new password.

This follows the existing separation in MOCO where controllers manage K8s objects and the ClusterManager manages MySQL state.
Each sub-step has a clear *driver* — the component that performs work during that sub-step and transitions out of it by flipping the relevant Conditions:

```
ApplyingRetain → DistributingPassword → AwaitingRollout → AwaitingDiscard → ApplyingDiscard → Finalizing → Idle
   ClusterMgr      Reconciler             Reconciler        (steady state:    Reconciler      Reconciler  (cycle complete)
   (RETAIN)        (Secret.Apply +        (wait for STS     waiting for       (flips           (promote pending
                    promote observedRot)   rollout → flip   operator's        DiscardReady     in source Secret +
                                           DiscardReady)    discard bump)     to Pending,      flips RotationReady
                                                                              hands off to     to True)
                                                                              ClusterMgr →
                                                                              CM runs DISCARD)
```

The "sub-step" is not stored as a single field. It is derived from the combination of three Conditions (`RotationReady`, `DiscardReady`, `DualPassword`) plus the `spec.{rotation,discard}Generation` vs `status.observed{Rotation,Discard}Generation` comparison (see [Step matrix](#step-matrix)). The component that completes a sub-step writes the corresponding Condition transitions:
- ClusterManager flips `DualPassword=True` (after RETAIN) and `DualPassword=False` (after DISCARD).
- Reconciler flips `DiscardReady=True` (after the post-distribute StatefulSet rollout settles, opening the verification window), `DiscardReady=False/Pending` (acknowledging an operator-initiated discard before ClusterManager runs DISCARD), and `RotationReady=True` (promoting `observedDiscardGeneration` after finalising the source Secret).

Inside the `ApplyingDiscard` step both components are eligible to run. To avoid a race that would skip the `DiscardStarted` Event and the `DiscardReady=False/Pending` observation, ClusterManager blocks on `DiscardReady=False/Pending` before touching MySQL — the Reconciler always wins the initial state-set, and ClusterManager only takes over once the handshake condition is observable. ClusterManager does **not** repeat the rollout wait inside DISCARD: the post-distribute rollout is already gated by `DiscardReady=True` (set in `AwaitingRollout`), so by the time the operator can bump `discardGeneration` every Pod is already running with the new password.

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
  observedGeneration: 1          # The metadata.generation the controller has reconciled
  observedRotationGeneration: 0  # Last completed rotationGeneration
  observedDiscardGeneration: 0   # Last completed discardGeneration
  rotationID: ""                 # UUID for the in-flight rotation cycle
  conditions:                    # See "Conditions" below
    - type: RotationReady
      status: "False"
      reason: Pending
      message: "Rotation cycle in flight; idle (rotate) is not currently allowed."
      lastTransitionTime: "2026-05-20T05:29:50Z"
      observedGeneration: 1
    - type: DiscardReady
      status: "False"
      reason: Pending
      message: "Rotation cycle in flight; awaiting-discard (discard) is not currently allowed."
      lastTransitionTime: "2026-05-20T05:29:50Z"
      observedGeneration: 1
    - type: DualPassword
      status: "False"
      reason: NotRetained
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

The Kubernetes API convention discourages `phase`-style enums and recommends Conditions in their place ([rationale](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties)). Each Condition is a positive-sense observation; True means the observed predicate currently holds, with `Reason` carrying the category of cause.

`CredentialRotation` exposes its state as three `metav1.Condition` entries. `RotationReady` and `DiscardReady` are **action-availability guards**: `True` means the corresponding operator action (`kubectl moco credential rotate` / `kubectl moco credential discard`) is currently allowed. They are mutually exclusive — at most one of them is True at a time — because the steady states they describe (Idle vs AwaitingDiscard) are themselves mutually exclusive. `DualPassword` is the orthogonal physical-state observation. The workflow "current step" is **derived** on the fly from these three Conditions plus the spec/observed generation comparison (see [Step matrix](#step-matrix)).

| Type | When `True` | When `False` |
|---|---|---|
| `RotationReady` | The CR is in the **Idle** steady state: no cycle is in flight, no dual password is held, and the operator may bump `spec.rotationGeneration`. Equivalent to `IsIdle()`. | A cycle is in flight (including `AwaitingRollout`), the CR is in `AwaitingDiscard`, or the cycle is stuck (Refused/Blocked/Stale). |
| `DiscardReady` | The CR is in the **AwaitingDiscard** steady state: the rotation phase finished, the post-distribute StatefulSet rollout has settled (every Pod is running with the new password), MySQL holds a dual-password set, and the operator may bump `spec.discardGeneration`. Equivalent to `IsAwaitingDiscard()`. | A cycle is in flight that is not yet awaiting-discard (including `AwaitingRollout`, where the rolling restart is still in progress), the CR is in Idle, the discard phase is in flight, or it is stuck (Refused/Blocked/Stale). |
| `DualPassword` | MySQL is holding a dual-password set for the MOCO system users (between successful RETAIN and successful DISCARD). | No dual-password state in MySQL (either never retained, or already discarded). |

> **kubectl wait ergonomics.** `kubectl wait --for=condition=RotationReady` waits until the CR returns to Idle (= the previous cycle has fully completed and a new rotate is allowed). `kubectl wait --for=condition=DiscardReady` waits until the CR enters AwaitingDiscard (= the rotation phase finished **and** the post-distribute rollout settled, so the verification window is genuinely open and a discard may now be bumped). The two waits are not used together — pick the one that matches what the script does next.

#### Reason values

Each `Reason` has a single meaning across every condition that uses it. The full set:

| Reason | Used on | Meaning |
|---|---|---|
| `Reconciled` | `RotationReady`, `DiscardReady` (True) | The condition's predicate currently holds: the CR is in the matching steady state. |
| `Pending` | `RotationReady`, `DiscardReady` (False) | The CR is not in the matching steady state, but no error has been recorded (the cycle is in flight, or the CR is in the other Ready's steady state). |
| `Refused` | `RotationReady`, `DiscardReady` (False) | The requested operation could not start (e.g. `cluster.Spec.Replicas == 0`). Nothing has been mutated. |
| `Blocked` | `RotationReady`, `DiscardReady` (False) | A cycle that previously started cannot progress (e.g. cluster scaled to 0 after pending passwords were written). Manual scale-up or the documented recovery procedure is required. |
| `Stale` | `RotationReady`, `DiscardReady` (False) | The source Secret (or other persisted state) is inconsistent. Manual recovery is required. |
| `Retained` | `DualPassword` (True) | MySQL is holding a dual-password set on all system users. |
| `NotRetained` | `DualPassword` (False) | MySQL is not currently holding a dual-password set (initial state for a cycle, or already discarded). |

> **Event-only reasons** (not condition `Reason` values): `DiscardRefused`, `DualPasswordExists`, `InconsistentState`, `MissingRotationPending`. These are emitted as Kubernetes Events for visibility in addition to the condition transition (e.g. `RotationReady=False/Refused` carries the same information as the `RotationRefused` Event, but the Event surfaces the transition to `kubectl describe`).

#### Step matrix

The internal workflow step is derived from the combination of the three Conditions plus the generation comparisons:

| Sub-step | `RotationReady` | `DiscardReady` | `DualPassword` | `newRotation` | `newDiscard` | Notes |
|---|---|---|---|---|---|---|
| Initial (no `RotationReady` condition yet) | absent | absent | absent | — | — | Treated as Idle so the first reconcile initialises the cycle. |
| Idle | **True** | False | False | False | False | Terminal / steady state. Operator may bump `spec.rotationGeneration`. |
| Applying RETAIN | False | False | False | True | False | RETAIN has not yet succeeded; ClusterManager runs it. |
| Distributing password | False | False | **True** | True | False | RETAIN succeeded; Reconciler distributes Secrets and promotes `observedRotationGeneration`. |
| Awaiting rollout | False | False | True | False | False | Reconciler waits for the post-distribute StatefulSet rollout to settle; flips `DiscardReady=True` once it does. |
| Awaiting discard | False | **True** | True | False | False | Steady state. Rollout already settled. Operator may bump `spec.discardGeneration`. |
| Applying DISCARD | False | False | True | False | True | Discard requested; ClusterManager runs DISCARD (no rollout re-wait needed). |
| Finalizing | False | False | False | False | True | DISCARD succeeded (`DualPassword` flipped back); Reconciler promotes pending passwords and flips `RotationReady=True`. |

`newRotation` is shorthand for `spec.rotationGeneration > status.observedRotationGeneration`; `newDiscard` likewise. A True Ready condition that lingers across a fresh `spec.{rotation,discard}Generation` bump is treated as Idle / AwaitingDiscard (whichever it is) so the Reconciler dispatch fires the corresponding seed handler — this is what avoids the "stale True from previous cycle" deadlock when the operator runs back-to-back rotations. Status=False with a non-`Pending` Reason takes priority over Idle/AwaitingDiscard: `RotationReady=False/Stale` or `DiscardReady=False/Stale` short-circuits to `StalePending`; `RotationReady=False/Refused|Blocked` short-circuits to `RotationRefused`/`RotationBlocked`; `DiscardReady=False/Refused|Blocked` short-circuits to `DiscardRefused`/`DiscardBlocked`. The post-distribute StatefulSet rollout wait is its own step (`AwaitingRollout`) owned by the Reconciler — `DiscardReady=True` is flipped only after the rollout settles, so by the time `discardGeneration` can be bumped no Pod is still running with the old password.

#### Cycle re-entry

When a new cycle begins (the Reconciler picks up `spec.rotationGeneration > status.observedRotationGeneration` while idle), the Reconciler writes the following Conditions in the same `Status().Update()`:

- `RotationReady`: `True/Reconciled` (or `False/Refused`, or absent) → `False/Pending`
- `DiscardReady`: stays `False/Pending` (it was already False in the previous Idle state)
- `DualPassword`: `False/NotRetained` (`Status` unchanged so `LastTransitionTime` is preserved by `apimeta.SetStatusCondition`)

`observedRotationGeneration` and `observedDiscardGeneration` are **not** reset — they continue to reflect the last completed cycle until the Reconciler promotes them at the end of each phase. The CR's `metadata.generation` (and therefore `metav1.Condition.observedGeneration`) bumps automatically on every spec change; controllers stamp the current `metadata.generation` into each condition they write.

#### Why three conditions?

The Kubernetes API conventions require Conditions to be programmatic observations that clients can monitor, with `Reason` reserved for the *category of cause* and not for state-machine encoding. The original design (`Rotating`, `OldPasswordRetained`, `Ready`) violated this in three ways: `Rotating` was a present-tense verb; its `Reason` encoded a workflow state machine; and identifiers like `NotStarted` / `Completed` carried different meanings across `Rotating` and `Ready`. The current design uses `RotationReady` and `DiscardReady` as **action-availability guards** (True iff the corresponding operator action is currently allowed — analogous to Pod's `Ready=True` meaning "you may route traffic to me now"), plus `DualPassword` as the orthogonal observation of MySQL's physical state. The two Ready conditions are structurally mutually exclusive (the Idle and AwaitingDiscard steady states cannot both hold), which removes the read-time ambiguity the earlier "generation tracking" semantics had — where `RotationReady=True && DiscardReady=True` appeared simultaneously and a human reader could not tell which phase the CR was in without consulting the generation fields.

`DualPassword` is intentionally named as a noun, parallel to Kubernetes conditions such as `MemoryPressure`, where `True` describes the situation rather than health. This avoids the past-tense surface-form mismatch that made the earlier `OldPasswordRetained=True` read as "retain completed" instead of "retain currently in effect."

Three conditions match the typical Kubernetes CRD shape (Deployment has 3, Pod has 4–5) and keep `kubectl get` output compact while remaining convention-compliant. The workflow step is derived from the same three Conditions rather than stored separately. The Reconciler reads additional context (source Secret pending state, StatefulSet rollout status) and ClusterManager reads MySQL `HasDualPassword` directly on the path where each matters, so the lack of dedicated sub-step Conditions does not require extra Kubernetes API I/O beyond what each handler already needs.

### Go Type Definitions

```go
// api/v1beta2/credentialrotation_types.go

// CredentialRotationSpec defines the desired state of CredentialRotation.
// The target MySQLCluster is identified by the CR's own name and namespace
// (CredentialRotation name must equal MySQLCluster name).
// +kubebuilder:validation:XValidation:rule="self.discardGeneration <= self.rotationGeneration",message="discardGeneration must be <= rotationGeneration"
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
    // rotation. The bump is only honored while the CR is in the
    // awaiting-discard steady state (DiscardReady=True, DualPassword=True).
    // +kubebuilder:default=0
    // +kubebuilder:validation:Minimum=0
    // +optional
    DiscardGeneration int64 `json:"discardGeneration"`
}

// CredentialRotationStatus defines the observed state of CredentialRotation.
type CredentialRotationStatus struct {
    // ObservedGeneration reflects the .metadata.generation that the
    // controller has most recently reconciled. Clients (kstatus, ArgoCD,
    // Flux) use this together with the RotationReady/DiscardReady
    // conditions to determine whether the controller has caught up with
    // the latest spec change.
    // +kubebuilder:default=0
    // +kubebuilder:validation:Minimum=0
    // +optional
    ObservedGeneration int64 `json:"observedGeneration"`

    // Conditions represent the latest available observations of the
    // rotation state. See the "Conditions" section of the design doc for
    // canonical Type/Reason definitions.
    // +listType=map
    // +listMapKey=type
    // +patchStrategy=merge
    // +patchMergeKey=type
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

    // RotationID is the UUID for the in-flight rotation cycle.
    // Empty when no cycle is active. The value, if non-empty, is a
    // canonical 36-character UUID.
    // +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$`
    // +kubebuilder:validation:MaxLength=36
    // +optional
    RotationID string `json:"rotationID,omitempty"`

    // ObservedRotationGeneration is the last rotationGeneration whose
    // rotation phase (RETAIN + pending-password distribution) completed
    // successfully. RotationReady becomes True when this equals
    // spec.rotationGeneration.
    // +kubebuilder:default=0
    // +kubebuilder:validation:Minimum=0
    // +optional
    ObservedRotationGeneration int64 `json:"observedRotationGeneration"`

    // ObservedDiscardGeneration is the last discardGeneration that
    // completed successfully. DiscardReady becomes True when this equals
    // spec.discardGeneration.
    // +kubebuilder:default=0
    // +kubebuilder:validation:Minimum=0
    // +optional
    ObservedDiscardGeneration int64 `json:"observedDiscardGeneration"`
}

// Condition type constants. Each Condition is an orthogonal past-tense
// or adjective observation (per Kubernetes API conventions).
const (
    ConditionRotationReady = "RotationReady"
    ConditionDiscardReady  = "DiscardReady"
    ConditionDualPassword  = "DualPassword"
)

// Reason constants. Each Reason has a single meaning across every
// condition that uses it.
const (
    ReasonReconciled  = "Reconciled"
    ReasonPending     = "Pending"
    ReasonRefused     = "Refused"
    ReasonBlocked     = "Blocked"
    ReasonStale       = "Stale"
    ReasonRetained    = "Retained"
    ReasonNotRetained = "NotRetained"
)
```

Controller code derives the current workflow step via a single helper. The
step is **not** stored on the CR — only its constituent Conditions are
serialised.

```go
// Step derives the current internal workflow step from spec, status, and
// conditions. The result is purely a function of the CR's persisted state.
func (cr *CredentialRotation) Step() RotationStep { ... }

// RotationStep enumerates the internal dispatch states. Possible values:
//   StepIdle, StepApplyingRetain, StepDistributingPassword,
//   StepAwaitingRollout, StepAwaitingDiscard, StepApplyingDiscard,
//   StepFinalizing, StepRotationRefused, StepRotationBlocked,
//   StepDiscardRefused, StepDiscardBlocked, StepStalePending
type RotationStep string
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
    // 4. rotationGeneration can only increase when the CR is idle, i.e.
    //    Step() ∈ {StepIdle, StepRotationRefused}:
    //      - StepIdle: RotationReady=True AND DiscardReady=False AND
    //        DualPassword=False (the previous cycle completed).
    //      - StepRotationRefused: RotationReady=False/Refused (nothing
    //        was mutated, so a retry is safe).
    //    A previously-stuck cycle (Blocked / Stale, either phase) must be
    //    cleared via the recovery procedure (delete + recreate) before
    //    a new rotationGeneration can be requested.
    // 5. discardGeneration can only increase while the CR is in the
    //    awaiting-discard steady state, i.e. Step() == StepAwaitingDiscard
    //    (RotationReady=False, DiscardReady=True, DualPassword=True;
    //    the post-distribute rollout has already settled).
}

func (r *CredentialRotation) ValidateDelete(ctx context.Context, ...) {
    // Allow delete in the following cases:
    //   - Step() ∈ {StepIdle, StepRotationRefused,
    //               StepRotationBlocked, StepDiscardBlocked,
    //               StepStalePending}.
    //     The Blocked / Stale cases are the documented recovery escape
    //     hatch (each begins with "kubectl delete credentialrotation").
    //   - The owning MySQLCluster is NotFound (GC after owner deletion)
    //   - The owning MySQLCluster has DeletionTimestamp set
    //     (unblock cluster termination — blockOwnerDeletion=true would
    //      otherwise stall the cluster in Terminating)
    //   - The CR carries a stale MySQLCluster ownerRef whose UID does not
    //     match the live cluster (recreated cluster; see Stale CR Handling)
    //
    // StepAwaitingRollout, StepAwaitingDiscard, and StepDiscardRefused
    // are NOT deletable: MySQL still holds dual passwords, and a naive
    // deletion would leave behind state that no controller can recover
    // from. Operators must scale the cluster down (transitioning into
    // StepRotationBlocked / StepDiscardBlocked) first.
    //
    // Otherwise: forbid (a delete while the cycle is actively progressing
    // would abandon the workflow).
}
```

## User Interface

```console
# Rotate: create CR (first time) or bump rotationGeneration
$ kubectl moco credential rotate <cluster-name>

# Check status. ROTREADY/DISCREADY are action-availability guards (True =
# the corresponding operator action is currently allowed). DUALPASSWORD is
# the physical-state observation (True while MySQL is holding a
# dual-password set between RETAIN and DISCARD).
$ kubectl get credentialrotation <cluster-name>
NAME         ROTREADY   DISCREADY   DUALPASSWORD   ROTATIONGEN   OBSERVEDROTATION   DISCARDGEN   OBSERVEDDISCARD   AGE
my-cluster   False      False       True           1             1                  0            0                 90s
# ↑ AwaitingRollout: rotate distributed, waiting for the rolling restart
#   to settle before DISCREADY flips to True.
my-cluster   False      True        True           1             1                  0            0                 5m
# ↑ AwaitingDiscard steady state: RotReady False (cycle not idle yet),
#   DiscReady True (rollout settled, discard now allowed),
#   DualPassword True (dual pw held).

# After running `kubectl moco credential discard <cluster-name>`:
$ kubectl get credentialrotation <cluster-name>
NAME         ROTREADY   DISCREADY   DUALPASSWORD   ROTATIONGEN   OBSERVEDROTATION   DISCARDGEN   OBSERVEDDISCARD   AGE
my-cluster   True       False       False          1             1                  1            1                 6m
# ↑ Idle steady state (cycle complete): RotReady True (rotate allowed
#   again), DiscReady False (no dual pw to discard), DualPassword False.

# Wait for the cycle to reach a steady state. Use exactly one Ready
# condition — pick the one that matches what the script does next:
$ kubectl wait --for=condition=DiscardReady credentialrotation/<cluster-name>   # wait for AwaitingDiscard (e.g. before `discard`)
$ kubectl wait --for=condition=RotationReady credentialrotation/<cluster-name>  # wait for Idle (e.g. before next rotate)

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
| `credential rotate` | If CR does not exist: create with `rotationGeneration: 1`. If CR exists: refuse if the CR is stale (MySQLCluster ownerRef UID does not match the live cluster), require `cr.IsIdle()` (Step is Idle or RotationRefused), then increment `rotationGeneration`. |
| `credential discard` | Refuse if the CR is stale; require `cr.IsAwaitingDiscard()` (Step is AwaitingDiscard) → patch `spec.discardGeneration` to match `spec.rotationGeneration` |
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

Triggered when `spec.rotationGeneration > status.observedRotationGeneration` and the CR is idle (`Step() ∈ {StepIdle, StepRotationRefused, StepRotationBlocked}`).

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Generate UUID as rotationID (reuse existing rotationID from the source Secret on crash recovery) | - | Reconciler |
| 2 | Generate pending passwords (e.g. `ADMIN_PASSWORD_PENDING`) in the source Secret | Secret.Update | Reconciler |
| 3 | Set conditions: `RotationReady=False/Pending`, `DiscardReady=False/Pending`, `DualPassword=False/NotRetained`. Set the rotationID. | Status.Update | Reconciler |

### ClusterManager: ApplyingRetain → DistributingPassword

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Pre-check: scan all instances for pre-existing dual passwords. Wait if found. Skip if `RETAIN_STARTED` marker is set (crash recovery). | - | ClusterManager |
| 1b | Set `RETAIN_STARTED` marker (rotationID) in source Secret | Secret.Update | ClusterManager |
| 2 | For each instance: execute `ALTER USER ... RETAIN CURRENT PASSWORD` with `sql_log_bin=0` | MySQL | ClusterManager |
| 3 | Set conditions: `DualPassword=True/Retained` (and clear any prior `RotationReady=False/Blocked` back to `False/Pending`) | Status.Update | ClusterManager |

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

### CredentialRotationReconciler: DistributingPassword → AwaitingRollout

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Distribute pending passwords to per-namespace Secrets (user Secret + my.cnf Secret) | Secret.Apply | Reconciler |
| 2 | Add restart annotation (`moco.cybozu.com/password-rotation-restart: <rotationID>`) to StatefulSet Pod template. The value is the rotationID (UUID) to ensure each rotation triggers a new rollout. | StatefulSet.Apply (SSA, dedicated field manager + ForceOwnership) | Reconciler |
| 3 | Promote `observedRotationGeneration = spec.rotationGeneration`. `DiscardReady` stays `False/Pending` — the next step (AwaitingRollout) flips it to `True`. | Status.Update | Reconciler |
| 4 | Rolling restart (Pods pick up new passwords via `EnvFrom`) | - | StatefulSet controller |

### CredentialRotationReconciler: AwaitingRollout → AwaitingDiscard

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Get the StatefulSet for the target MySQLCluster. If `NotFound` (rollout not yet started), requeue. | StatefulSet.Get | Reconciler |
| 2 | Check rollout completion (`ObservedGeneration`, `CurrentRevision == UpdateRevision`, `UpdatedReplicas == Replicas`, `ReadyReplicas == Replicas`). If still in flight, requeue. | - | Reconciler |
| 3 | Once settled: set `DiscardReady=True/Reconciled` and emit `AwaitingDiscard` Event. The verification window is now genuinely open — every Pod is running with the new password. | Status.Update | Reconciler |

**Why wait for the rollout here, not inside DISCARD?**
The verification window only makes sense once every Pod is using the new password. Surfacing `DiscardReady=True` earlier would let `kubectl wait --for=condition=DiscardReady` return while the rollout is still in flight, and would let the operator (or an automation script) initiate `discard` against a cluster whose old Pods still rely on the old password — a `DISCARD OLD PASSWORD` against MySQL at that point strips the secondary password those Pods depend on. Holding the rollout wait at the condition-flip boundary makes the guard match its semantics. The rollout state is also a K8s concern, so it logically sits in the Reconciler rather than ClusterManager.

**Scaled-down clusters (replicas=0):**
Rotation is refused at multiple points:
- The validation webhook rejects CR creation or `rotationGeneration` bump when `cluster.Spec.Replicas <= 0`.
- `handleStartRotation` (idle → `ApplyingRetain`) emits a `RotationRefused` Warning Event and sets `RotationReady=False/Refused`. Nothing has been mutated yet, so the CR remains idle (`Step() == StepRotationRefused`).
- If the cluster is scaled down to 0 **after** rotation has reached `ApplyingRetain` (i.e. pending passwords were already written to the source Secret), the ClusterManager step handler emits a `RotationBlocked` Warning Event and sets `RotationReady=False/Blocked` (`Step() == StepRotationBlocked`). Recovery requires either scaling the cluster back up (the reconciler resumes automatically when it sees a healthy cluster again) or running the recovery procedure to clean up the pending state.

Without running instances, ALTER USER cannot execute, and distributing new passwords would break connectivity when the cluster scales back up.

## Discard

### CredentialRotationReconciler: AwaitingDiscard → ApplyingDiscard (initial transition)

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Validate `spec.discardGeneration > status.observedDiscardGeneration` | - | Reconciler |
| 2 | Refuse with a `DiscardRefused` Warning Event and `DiscardReady=False/Refused` if `cluster.Spec.Replicas <= 0` (advancing to DISCARD would wedge because the webhook forbids reverting `discardGeneration` once bumped) | Status.Update | Reconciler |
| 3 | Whenever `DiscardReady` is not already `False/Pending` (initial bump, or recovery from `False/Refused` or `False/Blocked`): set `DiscardReady=False/Pending`, emit `DiscardStarted` Event, requeue. Once it is `False/Pending`, subsequent reconciles simply requeue while ClusterManager drives DISCARD. | Status.Update | Reconciler |

### ClusterManager: ApplyingDiscard → Finalizing

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Handshake: wait until the Reconciler has flipped `DiscardReady` to `False/Pending`. While the condition is `True`, `False/Refused`, or `False/Blocked`, return early without touching MySQL — the Reconciler owns the next transition. | - | ClusterManager |
| 2 | Determine target auth plugin via `GetAuthPlugin` on the primary | MySQL (read-only) | ClusterManager |
| 3 | For each instance: execute `DISCARD OLD PASSWORD` and auth plugin migration with `sql_log_bin=0` | MySQL | ClusterManager |
| 4 | Set conditions: `DualPassword=False/NotRetained` | Status.Update | ClusterManager |

**Why the DiscardReady handshake (Step 1)?**
Both Reconciler and ClusterManager observe `Step()=StepApplyingDiscard` once the operator bumps `discardGeneration`. Without the handshake, ClusterManager could race ahead and run DISCARD before the Reconciler has flipped `DiscardReady` from `True/Reconciled` to `False/Pending`, skipping the `DiscardStarted` Event and leaving the condition stale during the in-flight phase. Blocking ClusterManager on `DiscardReady=False/Pending` makes the Reconciler-side transition observable.

**No rollout wait here.**
The post-distribute StatefulSet rollout wait is owned by the Reconciler (AwaitingRollout step) and is what gates `DiscardReady=True` in the first place. By the time the operator can bump `discardGeneration` and reach this handler, rollout has settled and every Pod is running with the new password.

**Why connect with the pending password?**
DISCARD removes the old password.
If we connected with the old password, the connection would become invalid immediately after DISCARD succeeds.
Using the pending password also implicitly verifies that distribution was successful.

**Scaled-down clusters (replicas=0):**
Discard is rejected with a Warning Event (`DiscardRefused`) and `RequeueAfter: 15s`.
The operator should scale the cluster up first.

### CredentialRotationReconciler: Finalizing → Idle

| Step | Action | Persistence | Component |
|------|--------|-------------|-----------|
| 1 | Promote pending passwords to current in the source Secret via `password.ConfirmPendingPasswords()` | Secret.Update | Reconciler |
| 2 | Promote `observedDiscardGeneration = spec.discardGeneration`. Set conditions: `RotationReady=True/Reconciled` (back to Idle steady state), `DiscardReady=False/Pending` (no dual-password set to discard). | Status.Update | Reconciler |

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

    switch cr.Step() {
    case mocov1beta2.StepApplyingRetain:
        return p.handleApplyingRetain(ctx, ss, &cr)
    case mocov1beta2.StepApplyingDiscard:
        return p.handleApplyingDiscard(ctx, ss, &cr)
    default:
        return false, nil
    }
}
```

The DB operation logic (`rotateInstanceUsers`, `discardInstanceUsers`, `checkInstanceDualPasswords`) lives in `clustering/password_rotation.go` and reads/writes the CredentialRotation conditions for sub-step transitions.

### MySQLClusterReconciler

The only change to `MySQLClusterReconciler` is in `reconcileV1Secret`: it chooses which password set (current vs pending) to distribute based on the CredentialRotation step, and continues to **self-heal** the per-namespace Secrets in every step except `DistributingPassword`.

```go
func (r *MySQLClusterReconciler) reconcileV1Secret(ctx context.Context, ...) error {
    // ... source Secret creation (unchanged) ...

    // During credential rotation, the CredentialRotationReconciler owns secret
    // distribution from the DistributingPassword step onward. Choose which
    // password to distribute based on cr.Step():
    //   - Idle / ApplyingRetain / RotationRefused / RotationBlocked / StalePending:
    //     current passwords (pending not yet distributed).
    //   - DistributingPassword: skip; the rotation reconciler is the writer.
    //   - AwaitingDiscard / ApplyingDiscard / Finalizing:
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
        switch cr.Step() {
        case mocov1beta2.StepDistributingPassword:
            return nil
        case mocov1beta2.StepAwaitingRollout,
            mocov1beta2.StepAwaitingDiscard,
            mocov1beta2.StepApplyingDiscard,
            mocov1beta2.StepFinalizing:
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
Re-applying the pending password during the `AwaitingRollout`/`AwaitingDiscard`/`ApplyingDiscard`/`Finalizing` steps is intentional. If the per-namespace user/my.cnf Secret is accidentally deleted during rotation (manual operation, GC race, etc.), this reconciler restores it from the pending password kept in the source Secret. Pods restarted in that window can still come up with credentials MySQL accepts.

**Cache lag safety:**
If the cache briefly shows a stale Condition combination, the guard remains safe:
- `cr.Step()` resolves to `Idle` / `ApplyingRetain` / a terminal-non-progressing step → distribute current passwords → harmless (`ApplyingRetain` is intentionally not skipped; pending has not yet been distributed by the rotation reconciler).
- `cr.Step()` resolves to `DistributingPassword` → skip → correct (the rotation reconciler is the writer).
- `cr.Step()` resolves to `AwaitingRollout` / `AwaitingDiscard` / `Finalizing` → distribute pending passwords → idempotent re-apply.

The only theoretical risk is if the cache does not yet reflect `DualPassword=True` (which moves Step from `ApplyingRetain` to `DistributingPassword`) while the CredentialRotationReconciler has already distributed pending passwords.
In practice this cannot happen because RETAIN executes on all MySQL instances (takes seconds to minutes), far exceeding the cache propagation delay (~hundreds of milliseconds).

## Crash Safety

### Sub-step Boundary Safety

| Crash Point | Recovery |
|---|---|
| rotationGeneration bumped, pending passwords not yet generated | Reconciler re-generates on next reconcile |
| Pending passwords generated, RETAIN not started | ClusterManager picks up `Step=ApplyingRetain` |
| Pre-check passed, `RETAIN_STARTED` marker set, RETAIN not yet executed | Marker skips pre-check on retry; `HasDualPassword` makes RETAIN idempotent |
| RETAIN partially applied (some instances) | `RETAIN_STARTED` marker skips pre-check; `HasDualPassword` makes re-execution idempotent |
| RETAIN complete, `DualPassword=True` not yet written | ClusterManager re-runs → all skip → writes the condition transition |
| `Step=DistributingPassword`, Secrets not yet distributed | Reconciler distributes on next reconcile |
| `Step=AwaitingRollout`, rollout still in flight | Reconciler re-checks StatefulSet status on next reconcile; flips `DiscardReady=True` once it settles |
| `Step=ApplyingDiscard`, `DiscardReady` not yet flipped to `False/Pending` | Reconciler flips it on next reconcile; ClusterManager stays blocked on the handshake in the meantime |
| `Step=ApplyingDiscard`, `DiscardReady=False/Pending`, DISCARD not yet executed | ClusterManager picks up the step (rollout already settled in `AwaitingRollout`) |
| DISCARD complete, `DualPassword=False` not yet written | DISCARD is idempotent → re-run → writes the condition transition |
| `Step=Finalizing`, Secret promoted but status not updated | `HasPendingPasswords` returns false; `CurrentPasswordsMatch` verifies promotion succeeded → sets `RotationReady=True/Reconciled` (back to Idle) |
| `Step=Finalizing`, Secret not yet promoted | `ConfirmPendingPasswords` is idempotent |

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

If the controller crashes after updating the Secret but before patching the status, the Secret has already been promoted (pending passwords are now current) but `cr.Step()` still resolves to `Finalizing`.
On re-reconcile, `HasPendingPasswords` returns `(false, nil)`.
The Reconciler then verifies crash recovery by comparing the controller Secret's current passwords with the per-namespace user Secret via `CurrentPasswordsMatch`.
If they match, promotion already succeeded — the Reconciler promotes `observedDiscardGeneration`, sets `DiscardReady=True/Reconciled`, and resets the sub-step conditions (Step transitions to `Idle`).
If they differ (indicating pending keys were lost without promotion), the Reconciler emits an `InconsistentState` Warning Event and sets `DiscardReady=False/Stale` instead of completing.

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
- Step `AwaitingRollout` onwards: new passwords have been distributed to user Secret

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
  if cr.Step() ∈ {StepIdle, StepRotationRefused}        → allow (no mutations)
  if cr.Step() ∈ {StepRotationBlocked,
                  StepDiscardBlocked,
                  StepStalePending}                     → allow (recovery)
  if MySQLCluster is NotFound                           → allow (GC after owner deletion)
  if MySQLCluster.DeletionTimestamp                     → allow (GC unblock; see "MySQLCluster Deletion")
  if CR has a stale MySQLCluster                        → allow (recreated cluster; see "Stale CR Handling")
     ownerRef (UID mismatch)
  otherwise (actively progressing cycle,
             or AwaitingRollout / AwaitingDiscard /
             DiscardRefused where dual passwords are
             still held)                                → forbid
```

The `RotationBlocked` / `DiscardBlocked` / `StalePending` escape hatch is what makes the documented [Recovery Procedures](#recovery-procedures) work — they all begin with `kubectl delete credentialrotation ...`.

`AwaitingRollout`, `AwaitingDiscard`, and `DiscardRefused` are NOT deletable: MySQL still holds dual passwords, and deletion would leave behind state that no controller can recover from. Operators must scale the cluster down first (which transitions Step to `RotationBlocked` or `DiscardBlocked`).

If a cycle is *actively progressing* (any other in-flight step) and the operator must roll back manually, the operator can:
1. Scale the cluster down to 0 (the cycle will transition to `Step=RotationBlocked` / `DiscardBlocked`, after which delete is allowed), or
2. Wait for the cluster termination to release the GC delete.

The CredentialRotation CR does **not** use a finalizer for automatic rollback, because:
- Rollback requires connecting to every MySQL instance, which may not be possible during deletion (e.g., if the cluster is being scaled down)
- Partial rollback is worse than no rollback — it is safer to leave the state for manual inspection

See [Recovery Procedures](#recovery-procedures) for the steps to restore a consistent state after deletion.

### MySQLCluster Deletion

The ownerReference (with `blockOwnerDeletion=true`) means Kubernetes GC must delete the CR before the MySQLCluster can finish terminating.
The webhook explicitly allows delete when the owning cluster has a non-nil `DeletionTimestamp`, even when the CR is in an in-flight step (such as `ApplyingRetain` or `ApplyingDiscard`).
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

**Cause:** The source Secret lost its pending keys (manual edit, restore from backup, etc.) while the CredentialRotation CR is in the `AwaitingDiscard` steady state.

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

**Cause:** A MOCO system user has `additional_password` set while no rotation is in progress (the CR is idle: `DualPassword=False`).
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
| **Modified** | `controllers/mysqlcluster_controller.go` (`reconcileV1Secret` chooses current vs pending password based on `cr.Step()` and self-heals per-namespace Secrets), `cmd/moco-controller/cmd/run.go` (register new reconciler) |
