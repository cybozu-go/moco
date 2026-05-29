# System User Password Rotation with CredentialRotation CRD

## Background

MOCO manages 8 system MySQL users (`moco-admin`, `moco-agent`, `moco-repl`, `moco-clone-donor`, `moco-exporter`, `moco-backup`, `moco-readonly`, `moco-writable`). Their passwords are generated at cluster creation, stored in a controller-managed Secret in the system namespace, and distributed to per-namespace Secrets. Once generated, these passwords never change.

If a credential leak occurs, the only recovery option today is recreating the cluster. This design introduces an in-place rotation mechanism that avoids downtime, using a dedicated **CredentialRotation** CRD with its own controller.

## Why a Dedicated CRD?

Password rotation could be folded into `MySQLClusterReconciler`, but a dedicated CRD is preferable:

1. **Blast radius** — A dedicated controller isolates rotation failures from StatefulSet, Service, and backup CronJob reconciliation.
2. **Status bloat** — `MySQLCluster.Status` already carries conditions, backup status, replica counts, and more.
3. **Testability** — `MySQLClusterReconciler` is already large; rotation logic is easier to test in isolation.
4. **Separation of concerns** — Rotation is an operator-initiated, infrequent operation with its own lifecycle.

KubeDB takes a similar approach with `MySQLOpsRequest` (`type: RotateAuth`) as a separate CRD.

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

Rotation is a **two-step process** — **rotate** then **discard** — using MySQL's dual-password feature (8.0.14+). The operator explicitly triggers each step, with a verification window in between where both old and new passwords are accepted.

State is exposed as three Conditions:

- **`RotationReady`** — `True` iff the CR is in the **idle steady state** (`Step()==StepIdle`): no cycle in flight, no dual password held, and the operator may bump `spec.rotationGeneration`.
- **`DiscardReady`** — `True` iff the CR is in the **awaiting-discard steady state** (`Step()==StepAwaitingDiscard`): the rotation phase has finished, the post-distribute StatefulSet rollout has settled, MySQL holds a dual-password set, and the operator may bump `spec.discardGeneration`. The rollout gate ensures every Pod is already using the new password before the verification window opens.
- **`DualPassword`** — `True` while MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD).

`RotationReady` and `DiscardReady` are **action-availability guards** and are structurally mutually exclusive. `DualPassword` is the orthogonal physical-state observation.

```
  User bumps spec.rotationGeneration
       │
       ▼
  ┌── Rotate ───────────────────────────────────────────────────────┐
  │  Idle (RotReady=True, DiscReady=False, DualPw=False)            │
  │    │ Reconciler: seed pending passwords, RotReady→False/Pending │
  │    ▼                                                            │
  │  ApplyingRetain (RotReady=False, DiscReady=False, DualPw=False) │
  │    │ ClusterManager: ALTER USER ... RETAIN on every instance    │
  │    ▼                                                            │
  │  DistributingPassword (DualPw=True)                             │
  │    │ Reconciler: per-namespace Secret apply, restart annotation │
  │    │            promote observedRotationGeneration              │
  │    ▼                                                            │
  │  AwaitingRollout (DiscReady=False, DualPw=True)                 │
  │    │ Reconciler: watch StatefulSet rollout; once settled,       │
  │    │            DiscReady→True (verification window opens)      │
  │    ▼                                                            │
  │  AwaitingDiscard (DiscReady=True, DualPw=True)                  │
  └────────────────────────────────────────────────────────────────-┘
       │
       │  Operator verifies apps work with new passwords
       │  kubectl moco credential discard
       ▼
  ┌── Discard ──────────────────────────────────────────────────────┐
  │  spec.discardGeneration bumped (DiscReady=True stale, DualPw=T) │
  │    │ Reconciler: DiscReady→False/Pending; emit DiscardStarted   │
  │    ▼                                                            │
  │  ApplyingDiscard (DiscReady=False/Pending, DualPw=True)         │
  │    │ ClusterManager (blocked until DiscReady=False/Pending):    │
  │    │            DISCARD OLD PASSWORD + auth plugin migration    │
  │    │            (rollout already settled — no re-wait)          │
  │    ▼                                                            │
  │  Finalizing (DualPw=False)                                      │
  │    │ Reconciler: confirm Secret; promote observedDiscardGen     │
  │    │            RotReady→True (back to Idle)                    │
  │    ▼                                                            │
  │  Idle                                                           │
  └────────────────────────────────────────────────────────────────-┘
```

## Key Design Decisions

### Why MySQL Dual Password?

MySQL 8.0.14+ allows a user to have two valid passwords at once. `ALTER USER ... IDENTIFIED BY <new> RETAIN CURRENT PASSWORD` adds the new password as primary and keeps the old one as secondary; `ALTER USER ... DISCARD OLD PASSWORD` removes the secondary. MySQL only holds **one** secondary slot per user, so a second RETAIN would overwrite (and lose) the original old password — this is why double-execution must be prevented (see [Crash Safety](#crash-safety)).

### Why `sql_log_bin=0`?

MOCO supports cross-cluster replication. `ALTER USER` is a DDL written to the binary log; if propagated, a downstream cluster would receive the upstream's passwords and break its own credentials.

All rotation `ALTER USER` calls run in a dedicated `db.Conn` with `SET SESSION sql_log_bin=0`. As a consequence, within-cluster replicas also do not receive the change via replication, so `ALTER USER` must be executed on **every instance individually**.

### Why Auth Plugin Migration Happens After DISCARD?

MySQL Error 3894 prevents changing the authentication plugin in a `RETAIN CURRENT PASSWORD` statement. Plugin migration is therefore deferred to a separate `ALTER USER ... IDENTIFIED WITH <plugin> BY ...` issued after DISCARD.

The target plugin is read from `@@global.authentication_policy` on the primary; if the first element is `*` or empty, `caching_sha2_password` is used. This enables transparent migration from legacy plugins (e.g. `mysql_native_password`) as a side effect of each rotation.

### Responsibility Split: Reconciler vs ClusterManager

The **CredentialRotationReconciler** handles K8s resource operations: condition transitions, Secret management, StatefulSet rolling-restart annotation, StatefulSet rollout wait.

The **ClusterManager** handles DB operations: dual-password pre-checks, `ALTER USER RETAIN`, `DISCARD OLD PASSWORD`, auth plugin migration.

The post-distribute rollout wait sits in the Reconciler (the `AwaitingRollout` step). `DiscardReady=True` is gated on rollout completion, so by the time ClusterManager picks up DISCARD, every Pod is already running with the new password.

Each sub-step has one *driver* that performs the work and flips the condition that transitions out of the step:

| Sub-step | Driver | Condition transition on completion |
|---|---|---|
| `ApplyingRetain` | ClusterManager | `DualPassword=True` |
| `DistributingPassword` | Reconciler | promote `observedRotationGeneration` |
| `AwaitingRollout` | Reconciler | `DiscardReady=True` |
| `AwaitingDiscard` | (steady state — operator action) | (operator bumps `discardGeneration`) |
| `ApplyingDiscard` (initial) | Reconciler | `DiscardReady=False/Pending` (handshake) |
| `ApplyingDiscard` (DB work) | ClusterManager | `DualPassword=False` |
| `Finalizing` | Reconciler | promote `observedDiscardGeneration`, `RotationReady=True` |

Inside `ApplyingDiscard`, both components are eligible to run. To keep the `DiscardStarted` Event and the `DiscardReady=False/Pending` observation visible, ClusterManager blocks until `DiscardReady=False/Pending` is observed before touching MySQL.

## CRD Definition

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata:
  name: my-cluster            # must match the target MySQLCluster name
  namespace: my-namespace     # same namespace as the MySQLCluster
spec:
  rotationGeneration: 1       # bump to trigger a new rotation
  discardGeneration: 0        # bump to match rotationGeneration to discard
status:
  observedGeneration: 1
  observedRotationGeneration: 0
  observedDiscardGeneration: 0
  rotationID: ""              # UUID for the in-flight cycle
  conditions:
    - type: RotationReady
    - type: DiscardReady
    - type: DualPassword
```

### Naming Convention

The CR name **must match** the target MySQLCluster name (same name, same namespace). This naturally enforces at most one active rotation per cluster and lets both controllers look up the CR by the cluster name without an extra reference field.

The CR is **long-lived** — created once and reused across rotation cycles. A new cycle is started by incrementing `spec.rotationGeneration`; the controller compares each `spec.*Generation` with the corresponding `status.observed*Generation` to detect new requests.

Both counters are monotonically increasing; the invariant `0 <= discardGeneration <= rotationGeneration` is enforced by the validating webhook. This makes every spec change idempotent (applying the same spec twice is a no-op) and GitOps-friendly.

### OwnerReference

CredentialRotation sets an ownerReference to the target MySQLCluster so that Kubernetes garbage-collects it on cluster deletion.

### Spec / Status Fields

| Field | Type | Notes |
|---|---|---|
| `spec.rotationGeneration` | int64 | Required, `>= 1`, monotonically increasing. Each bump triggers a new rotation cycle. |
| `spec.discardGeneration` | int64 | Optional, default `0`, `<= rotationGeneration`, monotonically increasing. Bump (typically to match `rotationGeneration`) to start discard. |
| `status.observedGeneration` | int64 | Standard `metadata.generation` echo for kstatus / ArgoCD / Flux. |
| `status.observedRotationGeneration` | int64 | Last rotationGeneration whose rotation phase (RETAIN + distribute) completed. |
| `status.observedDiscardGeneration` | int64 | Last discardGeneration whose discard phase completed. |
| `status.rotationID` | string | UUID for the in-flight cycle (empty when no cycle is active). |
| `status.conditions` | `[]metav1.Condition` | See [Conditions](#conditions). |

### Conditions

The Kubernetes API conventions discourage `phase`-style enums and prescribe Conditions. Each Condition is a positive-sense observation; `True` means the predicate currently holds, with `Reason` describing the category of cause.

| Type | When `True` | When `False` |
|---|---|---|
| `RotationReady` | Idle steady state (`Step()==StepIdle`): no cycle in flight, no dual password held. Operator may bump `rotationGeneration`. | A cycle is in flight, the CR is in `AwaitingDiscard`, or the cycle is stuck (`Refused`/`Blocked`/`Stale`). |
| `DiscardReady` | Awaiting-discard steady state (`Step()==StepAwaitingDiscard`): rotation phase done, rollout settled, dual password held. Operator may bump `discardGeneration`. | A cycle is in flight that is not yet awaiting-discard, the CR is idle, the discard phase is in flight, or it is stuck. |
| `DualPassword` | MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD). | No dual-password state in MySQL. |

> `RotationReady` and `DiscardReady` are **not** equivalent to `IsIdle()` / `IsAwaitingDiscard()` in the strict sense — `IsIdle()` also returns true for `StepRotationRefused` (where `RotationReady=False/Refused`), since nothing has been mutated and a retry is safe. The webhook uses the `IsIdle()` / `IsAwaitingDiscard()` predicates to decide whether a `rotationGeneration` / `discardGeneration` bump is allowed.

**kubectl wait ergonomics.** `kubectl wait --for=condition=RotationReady` waits for Idle (previous cycle fully done, next rotate allowed). `kubectl wait --for=condition=DiscardReady` waits for AwaitingDiscard (rollout settled, discard allowed). The two are never used together.

#### Reason values

Each `Reason` has a single meaning across every condition that uses it.

| Reason | Used on | Meaning |
|---|---|---|
| `Reconciled` | `RotationReady`, `DiscardReady` (True) | The matching steady state. |
| `Pending` | `RotationReady`, `DiscardReady` (False) | Not in the matching steady state, no error recorded (cycle in flight, or the other Ready is True). |
| `Refused` | `RotationReady`, `DiscardReady` (False) | The requested operation could not start (e.g. `replicas == 0`). Nothing has been mutated. |
| `Blocked` | `RotationReady`, `DiscardReady` (False) | A started cycle cannot progress (e.g. cluster scaled to 0 after pending passwords were written). Manual recovery required. |
| `Stale` | `RotationReady`, `DiscardReady` (False) | The source Secret (or other persisted state) is inconsistent. Manual recovery required. |
| `Retained` | `DualPassword` (True) | MySQL holds a dual-password set on all system users. |
| `NotRetained` | `DualPassword` (False) | MySQL is not currently holding a dual-password set. |

> **Event-only reasons** (not condition `Reason` values): `DiscardRefused`, `DualPasswordExists`, `InconsistentState`, `MissingRotationPending`. These are emitted as Kubernetes Events for `kubectl describe` visibility in addition to the condition transition.

#### Step matrix

The internal workflow step is derived from the three Conditions plus the generation comparisons; it is **not** stored on the CR.

| Step | `RotationReady` | `DiscardReady` | `DualPassword` | `newRotation` | `newDiscard` |
|---|---|---|---|---|---|
| Initial (conditions absent) | — | — | — | — | — |
| `Idle` | **True** | False | False | False | False |
| `ApplyingRetain` | False | False | False | True | False |
| `DistributingPassword` | False | False | **True** | True | False |
| `AwaitingRollout` | False | False | True | False | False |
| `AwaitingDiscard` | False | **True** | True | False | False |
| `ApplyingDiscard` | False | False | True | False | True |
| `Finalizing` | False | False | False | False | True |

`newRotation` ≡ `spec.rotationGeneration > status.observedRotationGeneration`; `newDiscard` likewise.

A `Status=False` Reason of `Refused`/`Blocked`/`Stale` takes priority: it short-circuits to `RotationRefused` / `RotationBlocked` / `DiscardRefused` / `DiscardBlocked` / `StalePending` regardless of the table above. A stale `RotationReady=True` lingering across a fresh `rotationGeneration` bump is treated as Idle (and similarly for `DiscardReady=True` and AwaitingDiscard) so the seed handler fires — this avoids the "stale True from previous cycle" deadlock for back-to-back rotations.

#### Why three conditions?

`RotationReady` and `DiscardReady` are action-availability guards — analogous to Pod `Ready=True` meaning "you may route traffic to me now". The two Ready conditions are structurally mutually exclusive (the Idle and AwaitingDiscard steady states cannot both hold), which removes the read-time ambiguity earlier "generation tracking" semantics had. `DualPassword` is the orthogonal physical-state observation, named as a noun in parallel with `MemoryPressure`-style conditions where `True` describes the situation rather than health.

The current step is derived from these three conditions plus the generation comparisons, so the lack of a stored Phase keeps the CR convention-compliant without requiring extra API I/O at runtime.

### Validation Webhook

**ValidateCreate:**
- The target MySQLCluster (same name, same namespace) must exist.
- `cluster.Spec.Replicas` must be `> 0`.
- `rotationGeneration` must be `> 0`.
- `0 <= discardGeneration <= rotationGeneration`.

**ValidateUpdate:**
- Both `rotationGeneration` and `discardGeneration` must be monotonically non-decreasing.
- `discardGeneration <= rotationGeneration`.
- `rotationGeneration` may only increase while `oldCR.IsIdle()` (Step is `Idle` or `RotationRefused` — i.e. nothing was mutated, so a retry is safe). A previously-stuck cycle (`Blocked` / `Stale`) must be cleared via the recovery procedure (delete + recreate) before a new request.
- `rotationGeneration` increase additionally requires `cluster.Spec.Replicas > 0` (rechecked at apply time; the controller still re-checks at reconcile time to handle scale-downs after admission).
- `discardGeneration` may only increase while `oldCR.IsAwaitingDiscard()` (Step is `AwaitingDiscard`; the post-distribute rollout has settled).

**ValidateDelete** — allow:
- `cr.IsDeletable()` is true (Step is one of `Idle`, `RotationRefused`, `RotationBlocked`, `DiscardBlocked`, `StalePending`). The `Blocked` / `Stale` cases are the documented recovery escape hatch.
- The owning MySQLCluster is `NotFound` (GC after owner deletion).
- The owning MySQLCluster has `DeletionTimestamp` set (`blockOwnerDeletion=true` would otherwise stall cluster termination).
- The CR carries a stale MySQLCluster ownerRef whose UID does not match the live cluster (recreated cluster; see [Stale CR Handling](#stale-cr-handling-cluster-recreated-under-the-same-name)).

`AwaitingRollout`, `AwaitingDiscard`, and `DiscardRefused` are **not** deletable: MySQL still holds dual passwords. Operators must scale the cluster down first (which transitions to `RotationBlocked` or `DiscardBlocked`).

## User Interface

| Command | Behaviour |
|---|---|
| `kubectl moco credential rotate <cluster>` | If CR does not exist: create with `rotationGeneration: 1`. If CR exists: refuse if stale; require `cr.IsIdle()`; increment `rotationGeneration`. |
| `kubectl moco credential discard <cluster>` | Refuse if stale; require `cr.IsAwaitingDiscard()`; patch `spec.discardGeneration` to match `spec.rotationGeneration`. |
| `kubectl moco credential show <cluster>` | Read the per-namespace user Secret. |

`kubectl get credentialrotation` prints `ROTREADY` / `DISCREADY` / `DUALPASSWORD` (the three condition statuses) plus the four generation columns and `AGE`.

### GitOps / ArgoCD

The CR is long-lived and purely declarative, so it works naturally with GitOps. The lifecycle is driven by committing `rotationGeneration` / `discardGeneration` bumps; each commit triggers an ArgoCD sync that advances the cycle. No imperative CLI calls or CR deletions are required for normal operation.

**Do not mix GitOps with `kubectl moco credential rotate/discard`.** The CLI patches the same spec fields GitOps manages. If the CLI bumps a counter, GitOps reconcile will try to roll it back, but the webhook rejects any decrease — leaving the resource permanently `OutOfSync`. Worse, the CLI-triggered phase already mutates MySQL passwords irreversibly. Pick one source of truth per environment.

## Rotate

### Reconciler: Idle → ApplyingRetain

Triggered when `newRotation` and the CR is in `Step ∈ {Idle, RotationRefused, RotationBlocked}` with `cluster.Spec.Replicas > 0`.

| # | Action | Persistence |
|---|---|---|
| 1 | Generate (or reuse on crash recovery) the rotation UUID. | — |
| 2 | Write 8 `*_PENDING` keys and `ROTATION_ID` into the source Secret. | Secret.Update |
| 3 | Set `RotationReady=False/Pending`, `DiscardReady=False/Pending`, `DualPassword=False/NotRetained`. | Status.Update |

### ClusterManager: ApplyingRetain → DistributingPassword

| # | Action | Persistence |
|---|---|---|
| 1 | Pre-check: every instance is scanned for pre-existing dual passwords. Skipped if the `RETAIN_STARTED` marker is set (crash recovery). | — |
| 2 | Set `RETAIN_STARTED` marker (rotationID) in the source Secret. | Secret.Update |
| 3 | For each instance: connect with the current password, disable `super_read_only` on replicas, execute `ALTER USER ... RETAIN CURRENT PASSWORD` per user (skipping users where `HasDualPassword` is already true), restore `super_read_only`. | MySQL |
| 4 | Set `DualPassword=True/Retained` (and clear any prior `RotationReady=False/Blocked` back to `False/Pending`). | Status.Update |

**Pre-check + `RETAIN_STARTED` marker.** If any instance already has a dual-password set from outside this cycle, a `DualPasswordExists` Warning Event is emitted and the step waits. Once the pre-check passes, the marker is persisted so a crashed-and-restarted reconcile skips the pre-check and resumes RETAIN — idempotency from there is provided by per-user `HasDualPassword`.

### Reconciler: DistributingPassword → AwaitingRollout

| # | Action | Persistence |
|---|---|---|
| 1 | Apply pending passwords to per-namespace user Secret and my.cnf Secret. | Secret.Apply |
| 2 | Add `moco.cybozu.com/password-rotation-restart: <rotationID>` to the StatefulSet pod template via SSA under field manager `moco-credential-rotation` with `ForceOwnership`. | StatefulSet.Apply |
| 3 | Promote `observedRotationGeneration = spec.rotationGeneration`. `DiscardReady` stays `False/Pending`. | Status.Update |
| 4 | StatefulSet controller rolls the Pods. | — |

### Reconciler: AwaitingRollout → AwaitingDiscard

| # | Action | Persistence |
|---|---|---|
| 1 | Get the StatefulSet; if `NotFound`, requeue. | — |
| 2 | Check rollout completion (`ObservedGeneration` caught up, `CurrentRevision == UpdateRevision`, `UpdatedReplicas == Replicas`, `ReadyReplicas == Replicas`). If in flight, requeue. | — |
| 3 | Once settled: `DiscardReady=True/Reconciled` and emit `AwaitingDiscard` Event. | Status.Update |

**Why wait for the rollout here, not inside DISCARD?** The verification window only makes sense once every Pod is using the new password. Surfacing `DiscardReady=True` earlier would let `kubectl wait --for=condition=DiscardReady` return while the rollout is still in flight, and would let an automation script kick off `discard` against a cluster whose old Pods still depend on the old password — DISCARD at that point would strip the secondary password out from under them. The rollout is also a K8s concern, so it belongs in the Reconciler.

### Scaled-down clusters (replicas=0)

Rotation is refused at three points:
- The webhook rejects CR creation or `rotationGeneration` bump when `cluster.Spec.Replicas <= 0`.
- `handleStartRotation` emits a `RotationRefused` Warning Event and sets `RotationReady=False/Refused`. Nothing has been mutated; the CR stays in `Step=RotationRefused` and remains eligible for retry.
- If the cluster is scaled down to 0 *after* pending passwords were written, the ClusterManager handler emits a `RotationBlocked` Warning Event and sets `RotationReady=False/Blocked`. Recovery requires either scaling the cluster back up (the reconciler resumes automatically when it sees a healthy cluster again) or following the recovery procedure.

## Discard

### Reconciler: AwaitingDiscard → ApplyingDiscard (initial transition)

| # | Action | Persistence |
|---|---|---|
| 1 | Validate `spec.discardGeneration > status.observedDiscardGeneration`. | — |
| 2 | If `cluster.Spec.Replicas <= 0`: emit `DiscardRefused` Warning Event and set `DiscardReady=False/Refused`. The webhook forbids reverting `discardGeneration` once bumped, so a Refused state at this point is a stable wait-for-scale-up. | Status.Update |
| 3 | Whenever `DiscardReady` is not already `False/Pending` (initial bump, or recovery from `False/Refused` or `False/Blocked`): set `DiscardReady=False/Pending`, emit `DiscardStarted` Event, requeue. Once it is `False/Pending`, subsequent reconciles just requeue while ClusterManager drives DISCARD. | Status.Update |

### ClusterManager: ApplyingDiscard → Finalizing

| # | Action | Persistence |
|---|---|---|
| 1 | Handshake: wait until `DiscardReady=False/Pending`. While the condition is `True`, `False/Refused`, or `False/Blocked`, return early. | — |
| 2 | Determine the target auth plugin via `GetAuthPlugin` on the primary. | MySQL (read-only) |
| 3 | For each instance: connect with the **pending** password, disable `super_read_only` on replicas, execute `DISCARD OLD PASSWORD` per user (skipped where `HasDualPassword` is already false), then auth plugin migration. Restore `super_read_only`. | MySQL |
| 4 | Set `DualPassword=False/NotRetained`. | Status.Update |

**Why the handshake?** Both Reconciler and ClusterManager observe `Step=ApplyingDiscard` once the operator bumps `discardGeneration`. Without the handshake, ClusterManager could race ahead and run DISCARD before the Reconciler flips `DiscardReady` from `True/Reconciled` to `False/Pending`, skipping the `DiscardStarted` Event.

**Why connect with the pending password?** DISCARD removes the old password. Connecting with the old password would fail immediately after DISCARD succeeds. Using the pending password also implicitly verifies that distribution was successful.

**No rollout re-wait.** The post-distribute rollout is already gated by `DiscardReady=True` (set in `AwaitingRollout`), so by the time the operator can bump `discardGeneration` every Pod is already running with the new password.

### Reconciler: Finalizing → Idle

| # | Action | Persistence |
|---|---|---|
| 1 | Promote pending passwords to current in the source Secret (`ConfirmPendingPasswords`). | Secret.Update |
| 2 | Promote `observedDiscardGeneration = spec.discardGeneration`. Set `RotationReady=True/Reconciled`, `DiscardReady=False/Pending`. | Status.Update |

## Source Secret Layout

During rotation, the source Secret (in the controller namespace) holds both current and pending passwords:

```
ADMIN_PASSWORD:         <current>
AGENT_PASSWORD:         <current>
…
ADMIN_PASSWORD_PENDING: <new>      # only during rotation
AGENT_PASSWORD_PENDING: <new>      # only during rotation
…
ROTATION_ID:            <uuid>     # only during rotation
RETAIN_STARTED:         <uuid>     # only during the ApplyingRetain step (crash-safety marker)
```

`HasPendingPasswords` validates that all 8 `*_PENDING` keys and `ROTATION_ID` are present together and that the rotation ID matches the expected value. Partial states (some pending keys missing, or `ROTATION_ID` without pending keys) are surfaced as inconsistent state.

### Why Embed Pending Passwords in the Source Secret?

An alternative is a separate Secret owned by the CR. Pending passwords are embedded in the source Secret instead because:

1. **Crash safety of the confirm step.** `ConfirmPendingPasswords` promotes pending passwords to current by renaming keys within a single object — atomic at the Secret level. A separate Secret would require copying data between two objects; a crash between the read and the write could lose the new passwords irrecoverably.
2. **Simpler failure modes.** With a single Secret, the only question on crash recovery is "did the update succeed?". With two Secrets, every sub-step has to reason about cross-object consistency.
3. **Idempotency.** `SetPendingPasswords` checks if matching pending keys already exist; `ConfirmPendingPasswords` is a no-op when no pending keys remain. Both work on a single object.

## Component Details

### CredentialRotationReconciler (new)

The reconciler watches:
- `CredentialRotation` (primary).
- `MySQLCluster` (filtered to `Spec.Replicas` change or `DeletionTimestamp` flip), mapped to the same namespace/name — so a `Refused` / `Blocked` cycle resumes immediately on scale-up.
- `Secret` in the system namespace (filtered by the `mysql-<ns>.<name>` naming pattern), so a Stale source Secret can be cleaned up without waiting for the 15-second requeue.

For sub-steps owned by ClusterManager (`ApplyingRetain`, `ApplyingDiscard` DB work), the reconciler requeues every 15s while observing the condition for progress.

Server-Side Apply writes for the rolling-restart annotation use the dedicated field manager `moco-credential-rotation` with `ForceOwnership`, so the annotation is not stripped by `MySQLClusterReconciler`'s `moco-controller` field manager on the next cluster reconcile.

### ClusterManager

ClusterManager reads the CredentialRotation CR inside each tick and dispatches on `cr.Step()`:
- `ApplyingRetain` → run the RETAIN flow on this cluster.
- `ApplyingDiscard` → run the DISCARD flow (blocked on the `DiscardReady=False/Pending` handshake first).
- Any other step → no-op for rotation; normal clustering continues.

A CR whose ownerReference UID does not match the live cluster (stale CR) is ignored. Status writes use `retry.RetryOnConflict` with a fresh `Get` inside the retry to play nicely with concurrent status updates from the reconciler.

### MySQLClusterReconciler

The only change to `MySQLClusterReconciler` is in `reconcileV1Secret`: it consults `cr.Step()` to decide which password set to distribute, and continues to **self-heal** the per-namespace Secrets in every step except `DistributingPassword`.

| `cr.Step()` | Behaviour of `reconcileV1Secret` |
|---|---|
| (no CR / CRD not installed) | Distribute current passwords (normal behaviour). |
| `Idle`, `ApplyingRetain`, `RotationRefused`, `RotationBlocked`, `StalePending` | Distribute current passwords (pending not yet distributed). |
| `DistributingPassword` | Skip: the rotation reconciler is the writer. |
| `AwaitingRollout`, `AwaitingDiscard`, `ApplyingDiscard`, `Finalizing` | Distribute pending passwords (self-heal — `apply()` is a no-op when the content matches). |
| `DiscardRefused`, `DiscardBlocked` | Distribute pending passwords (the rotation phase is past; pending is already the canonical credential). |

Transient lookup errors must **not** silently fall back to current passwords — doing so would overwrite already-distributed pending credentials and break the rollout / discard flow. Only `NotFound` and `NoMatch` (CRD not installed) are treated as "no active rotation".

## Crash Safety

| Crash point | Recovery |
|---|---|
| `rotationGeneration` bumped, pending passwords not yet generated | Reconciler re-generates on next reconcile |
| Pending passwords generated, RETAIN not started | ClusterManager picks up `Step=ApplyingRetain` |
| Pre-check passed, `RETAIN_STARTED` marker set, RETAIN not yet executed | Marker skips pre-check; `HasDualPassword` makes RETAIN idempotent |
| RETAIN partially applied | `RETAIN_STARTED` marker + per-user `HasDualPassword` makes re-execution safe |
| RETAIN complete, `DualPassword=True` not yet written | Re-run sees all users already retained → writes the condition transition |
| `DistributingPassword`, Secrets not yet distributed | Reconciler distributes on next reconcile |
| `AwaitingRollout`, rollout still in flight | Reconciler re-checks StatefulSet status; flips `DiscardReady=True` once it settles |
| `ApplyingDiscard`, `DiscardReady` not yet flipped to `False/Pending` | Reconciler flips it on next reconcile; ClusterManager blocked on handshake meanwhile |
| `ApplyingDiscard`, `DiscardReady=False/Pending`, DISCARD not yet executed | ClusterManager picks up the step |
| DISCARD complete, `DualPassword=False` not yet written | `HasDualPassword` gates DISCARD → re-run skips all users → writes the condition transition |
| `Finalizing`, Secret promoted but status not updated | `HasPendingPasswords` returns false; `CurrentPasswordsMatch` verifies promotion succeeded → sets `RotationReady=True` |
| `Finalizing`, Secret not yet promoted | `ConfirmPendingPasswords` is idempotent |

### Why `HasDualPassword` instead of per-user status tracking?

MySQL holds only one secondary password slot per user. A second RETAIN with the same pending password would overwrite the secondary slot — evicting the original old password and breaking the controller's ability to connect. Tracking per-user progress in Kubernetes status would be racy with MySQL state; ClusterManager queries MySQL directly (`mysql.user.User_attributes` for `additional_password`) so MySQL is the source of truth, and the check is read-only and safe to re-run.

### Idempotency of DISCARD

`ALTER USER ... DISCARD OLD PASSWORD` fails when there is no secondary password to discard. The DISCARD handler queries `HasDualPassword` per user and skips users whose secondary password is already gone — mirroring the RETAIN gate.

### `ConfirmPendingPasswords` crash recovery

If the controller crashes after the Secret update but before the status update, the Secret has already been promoted but `cr.Step()` still resolves to `Finalizing`. On re-reconcile, `HasPendingPasswords` returns `(false, nil)`. The reconciler then verifies via `CurrentPasswordsMatch` that the controller Secret's current passwords match the per-namespace user Secret. If they match, promotion already succeeded; if they differ, the reconciler emits an `InconsistentState` Warning Event and sets `DiscardReady=False/Stale`.

## Deletion Handling

### CR deletion during rotation

The CR is long-lived in normal operation. The validating webhook forbids deletion mid-cycle to prevent leaving pending / dual-password state behind:

```
ValidateDelete:
  cr.IsDeletable()                                  → allow (no mutations, or
                                                      stuck in a state that the
                                                      recovery procedure resolves
                                                      by deleting the CR)
  MySQLCluster is NotFound                          → allow (GC after owner deletion)
  MySQLCluster.DeletionTimestamp set                → allow (unblock cluster GC)
  CR has stale MySQLCluster ownerRef (UID mismatch) → allow (recreated cluster)
  otherwise                                         → forbid
```

`AwaitingRollout`, `AwaitingDiscard`, and `DiscardRefused` are **not** deletable: MySQL still holds dual passwords. Operators must scale the cluster down first (which transitions to `RotationBlocked` / `DiscardBlocked`).

The CR does **not** use a finalizer for automatic rollback: rollback requires connecting to every MySQL instance (which may not be possible during deletion), and a partial rollback is worse than no rollback.

### MySQLCluster deletion

The ownerReference (`blockOwnerDeletion=true`) means GC must delete the CR before the MySQLCluster finishes terminating. The webhook explicitly allows delete when the owning cluster has a non-nil `DeletionTimestamp`, even for in-flight steps; otherwise the cluster would be stuck in `Terminating` indefinitely. No special teardown is needed — the MySQL instances are being destroyed too.

### Stale CR handling (cluster recreated under the same name)

If a `MySQLCluster` is deleted and another is recreated under the same name before GC reclaims the original CR, the leftover CR matches the new cluster by `namespace/name` but its ownerReference points at the old cluster's UID. Adopting that CR onto the new cluster would let stale rotation state poison a fresh cluster, so stale CRs are **invisible** to every component:

| Component | Behaviour on a stale CR |
|---|---|
| ValidateDelete | Allow delete (operator / GC can remove it) |
| `CredentialRotationReconciler` | Emit `StaleCredentialRotation` Warning Event and return without adopting |
| `ClusterManager.handlePasswordRotation` | Return early; do not run RETAIN / DISCARD |
| `MySQLClusterReconciler.reconcileV1Secret` | Ignore the CR; distribute current passwords normally |
| `kubectl moco credential rotate` / `discard` | Refuse with an error instructing the user to delete the stale CR |

"Stale" means the CR has a MySQLCluster ownerReference whose UID does **not** match the live cluster, with no matching reference. A CR with no MySQLCluster ownerReference yet (just-created, not yet adopted) is treated as **fresh**.

## Assumptions

- No MOCO system user has a dual password when rotation starts. The pre-check on `ApplyingRetain` validates this; on violation it emits a `DualPasswordExists` Warning Event and waits. See [Recovery: Dual Password Exists While No Active Rotation](#dual-password-exists-while-no-active-rotation).
- MySQL version is 8.0.14+ (dual password support).

## Security Considerations

- `RotateUserPassword`, `DiscardOldPassword`, and `MigrateUserAuthPlugin` interpolate user names directly into SQL (MySQL does not support placeholders for `ALTER USER`). User names are always from the fixed constants in `pkg/constants/users.go`.
- `MigrateUserAuthPlugin` interpolates the plugin name into `IDENTIFIED WITH`. The value is validated against `^[a-zA-Z0-9_]+$` and derived from `@@global.authentication_policy` on the primary, never from user input.
- All `ALTER USER` rotation calls run under `SET sql_log_bin=0` on a dedicated `db.Conn` to prevent cross-cluster propagation.

## Recovery Procedures

All recovery procedures share one principle: **reset MySQL passwords back to the current (old) values known to the controller.** `ALTER USER ... IDENTIFIED BY` (without RETAIN) sets the primary password and clears any secondary, returning MySQL to a clean single-password state.

### How to Reset MySQL Passwords

Retrieve the current passwords from the source Secret:

```console
$ kubectl -n <system-namespace> get secret <controller-secret-name> \
    -o jsonpath='{.data.ADMIN_PASSWORD}' | base64 -d
# Repeat for AGENT_PASSWORD, REPLICATION_PASSWORD, CLONE_DONOR_PASSWORD,
# EXPORTER_PASSWORD, BACKUP_PASSWORD, READONLY_PASSWORD, WRITABLE_PASSWORD
```

Identify the primary:

```console
$ kubectl -n <namespace> exec <pod> -c mysqld -- \
    mysql -u moco-admin -p<admin-password> \
    -e "SELECT @@read_only, @@super_read_only;"
# primary: read_only=0, super_read_only=0
# replica: read_only=1, super_read_only=1
```

Execute on the primary:

```console
$ kubectl -n <namespace> exec <primary-pod> -c mysqld -- \
    mysql -u moco-admin -p<admin-password> -e "
  SET SESSION sql_log_bin=0;
  ALTER USER 'moco-admin'@'%'        IDENTIFIED BY '<admin-password>';
  ALTER USER 'moco-agent'@'%'        IDENTIFIED BY '<agent-password>';
  ALTER USER 'moco-repl'@'%'         IDENTIFIED BY '<repl-password>';
  ALTER USER 'moco-clone-donor'@'%'  IDENTIFIED BY '<clone-donor-password>';
  ALTER USER 'moco-exporter'@'%'     IDENTIFIED BY '<exporter-password>';
  ALTER USER 'moco-backup'@'%'       IDENTIFIED BY '<backup-password>';
  ALTER USER 'moco-readonly'@'%'     IDENTIFIED BY '<readonly-password>';
  ALTER USER 'moco-writable'@'%'     IDENTIFIED BY '<writable-password>';
"
```

Execute on **each replica** (with `super_read_only` handling):

```console
$ kubectl -n <namespace> exec <replica-pod> -c mysqld -- \
    mysql -u moco-admin -p<admin-password> -e "
  SET SESSION sql_log_bin=0;
  SET GLOBAL super_read_only=OFF;
  ALTER USER 'moco-admin'@'%'        IDENTIFIED BY '<admin-password>';
  ALTER USER 'moco-agent'@'%'        IDENTIFIED BY '<agent-password>';
  ALTER USER 'moco-repl'@'%'         IDENTIFIED BY '<repl-password>';
  ALTER USER 'moco-clone-donor'@'%'  IDENTIFIED BY '<clone-donor-password>';
  ALTER USER 'moco-exporter'@'%'     IDENTIFIED BY '<exporter-password>';
  ALTER USER 'moco-backup'@'%'       IDENTIFIED BY '<backup-password>';
  ALTER USER 'moco-readonly'@'%'     IDENTIFIED BY '<readonly-password>';
  ALTER USER 'moco-writable'@'%'     IDENTIFIED BY '<writable-password>';
  SET GLOBAL super_read_only=ON;
"
```

> `sql_log_bin=0` must be set before disabling `super_read_only` to prevent intermediate writes from being logged. MOCO's clustering loop will re-enable `super_read_only` automatically if the manual re-enable fails.

### Stale Pending Passwords (RotationID Mismatch)

**Symptom:** Warning Event `RotationPendingError`.

**Cause:** A previous rotation was interrupted, leaving `*_PENDING` keys and a `ROTATION_ID` from a different cycle in the source Secret.

**Why this needs MySQL cleanup:** The interrupted rotation may have partially executed RETAIN. Without cleanup, a new rotation would see `HasDualPassword=true` on those instances, skip RETAIN, and leave stale passwords — breaking connectivity after DISCARD.

**Recovery order: delete CR → clean Secret → restart Pods → reset MySQL → recreate CR.** Order matters: some Pods may be running with pending passwords. MySQL still accepts both. Resetting MySQL first would break those Pods immediately.

```console
# 1. Delete the CR; reconcileV1Secret is now free to re-distribute old passwords.
$ kubectl delete credentialrotation my-cluster

# 2. Clean the source Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_PENDING keys and ROTATION_ID.

# 3. Restart Pods so they pick up the old passwords.
$ kubectl -n <namespace> rollout restart statefulset <cluster-name>
$ kubectl -n <namespace> rollout status  statefulset <cluster-name>

# 4. Reset MySQL passwords on all instances (see "How to Reset MySQL Passwords").

# 5. Recreate the CR (GitOps will do this from Git; or:)
$ kubectl moco credential rotate <cluster-name>
```

### Missing Pending Passwords During Discard

**Symptom:** Warning Event `MissingRotationPending`.

**Cause:** The source Secret lost its pending keys (manual edit, restore from backup, etc.) while the CR is in `AwaitingDiscard`.

**Why this is dangerous:** All instances hold dual passwords and Pods may be using the (now-lost) pending passwords. MySQL stores only password hashes — the pending values are irrecoverable.

**Recovery:** Same procedure as Stale Pending Passwords above.

### Dual Password Exists While No Active Rotation

**Symptom:** Warning Event `DualPasswordExists`.

**Cause:** A system user has `additional_password` set while no rotation is in progress (CR is idle, `DualPassword=False`). Either a previous recovery didn't fully clear MySQL state, or someone ran `ALTER USER ... RETAIN CURRENT PASSWORD` manually.

**Why DISCARD is unsafe here:** After a manual RETAIN, the primary password is the new (unknown) value and the secondary is the old (known) value. DISCARD would remove the secondary, leaving only the unknown primary — breaking connectivity.

**Recovery:** No CR deletion or Secret cleanup needed.

```console
# 1. (recommended) Verify Pods can connect with current credentials.
# 2. (recommended) Wait for any in-progress rollout.
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# 3. Reset MySQL passwords on all instances (see "How to Reset MySQL Passwords").

# After recovery, retry rotation:
$ kubectl moco credential rotate <cluster-name>
```

## Impact Summary

| Category | Files |
|---|---|
| **New** | `api/v1beta2/credentialrotation_types.go`, `api/v1beta2/credentialrotation_helpers.go`, `api/v1beta2/credentialrotation_webhook.go`, `controllers/credentialrotation_controller.go`, `clustering/password_rotation.go` |
| **New (CLI)** | `cmd/kubectl-moco/cmd/credential.go` (`rotate` / `discard` / `show` subcommands, stale-CR detection) |
| **New (DB ops)** | `pkg/password/rotation.go`, `pkg/dbop/password.go` |
| **Modified** | `controllers/mysqlcluster_controller.go` (`reconcileV1Secret` chooses current vs pending password by `cr.Step()` and self-heals per-namespace Secrets), `cmd/moco-controller/cmd/run.go` (register the new reconciler) |
