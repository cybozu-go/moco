# System User Password Rotation with CredentialRotation CRD

## Background

MOCO manages 8 MySQL users (`moco-admin`, `moco-agent`, `moco-repl`, `moco-clone-donor`, `moco-exporter`, `moco-backup`, `moco-readonly`, `moco-writable`). This document calls all 8 "system users" for short — the code labels the first six "system users" and the last two "end-user accounts" — and rotation covers all 8. Their passwords are generated at cluster creation, stored in a controller-managed credential Secret (the **controller Secret**, named by `ControllerSecretName()`) in the system namespace, and distributed to per-namespace Secrets. Once generated, these passwords never change.

> The controller Secret is distinct from the *replication source Secret* (`spec.replicationSourceSecretName`), which holds donor connection info for an intermediate-primary cluster. This document only uses "controller Secret" for the credential Secret.

If a credential leak occurs, the only recovery option today is recreating the cluster. This design introduces an in-place rotation mechanism that avoids downtime, using a dedicated **CredentialRotation** CRD with its own controller.

## Why a Dedicated CRD?

Password rotation could be implemented inside `MySQLClusterReconciler`, but a dedicated CRD is a better fit, for four reasons:

1. **Blast radius** — A dedicated controller isolates rotation failures from StatefulSet, Service, and backup CronJob reconciliation.
2. **Status bloat** — `MySQLCluster.Status` already carries conditions, backup status, replica counts, and more; adding rotation state would make it harder to read.
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
- Rollback of a started rotation (the design is roll-forward only; see [Roll-forward Only](#roll-forward-only))

## Assumptions

- **RETAIN is all-or-nothing.** `DualPassword=True` (the promotion precondition) is only set after `ALTER USER ... RETAIN` succeeded on **every** instance. The RETAIN loop must never skip an unreachable instance — the core invariant depends on this. If the loop skipped an instance, that instance would keep rejecting the canonical current password. Any future change to the RETAIN loop must preserve this property.
- No MOCO system user has a dual password when rotation starts. The pre-check on `ApplyingRetain` validates this; on violation it emits a `DualPasswordExists` Warning Event and waits. (The pre-check is skipped on crash recovery, when this cycle's `RETAIN_STARTED` marker is already set — per-user `HasDualPassword` checks take over.) See [Recovery: Dual Password Exists Outside the Current Cycle](#dual-password-exists-outside-the-current-cycle).
- MySQL version is 8.0.14+ (dual password support).

## Overview

Rotation is a **two-phase process** — **rotate** then **discard** — using MySQL's dual-password feature (8.0.14+). The operator triggers each phase explicitly. Between the two phases there is a verification window where MySQL accepts both the old and the new password.

The whole design rests on a single invariant:

> **The controller Secret's current passwords always authenticate on every MySQL instance.**

New passwords are staged as `*_PENDING` keys, applied to every instance with `ALTER USER ... RETAIN CURRENT PASSWORD`, and **promoted to current immediately after RETAIN succeeds on all instances** — at that moment every instance accepts both old and new, so making the new password canonical is safe. Distribution to per-namespace Secrets then happens through the normal `MySQLCluster` reconcile path, which always distributes current passwords. See [The Core Invariant](#the-core-invariant) for why this ordering removes whole classes of failure modes.

State is exposed as three Conditions:

- **`RotationReady`** — `True` while the CR is in the **idle steady state**: no cycle in flight, no dual password held, and the operator may bump `spec.rotationGeneration`.
- **`DiscardReady`** — `True` while the CR is in the **awaiting-discard steady state**: the rotation phase has finished, the post-promotion StatefulSet rollout has settled, and MySQL holds a dual-password set. In this state the operator may bump `spec.discardGeneration`. The rollout gate makes sure every Pod already uses the new password before the verification window opens.
- **`DualPassword`** — `True` while MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD).

`RotationReady` and `DiscardReady` show whether the operator may start the next action, and are never `True` at the same time. `DualPassword` shows whether MySQL currently holds a secondary password.

```
  User bumps spec.rotationGeneration
       │
       ▼
  ┌── Rotate ────────────────────────────────────────────────────────────────┐
  │  Idle (RotationReady=True, DiscardReady=False, DualPw=False)             │
  │    │ Reconciler: seed pending passwords + status.rotationID,             │
  │    │            all three Conds→False (RotationReady→False/Pending);     │
  │    │            emit RotationStarted                                     │
  │    ▼                                                                     │
  │  ApplyingRetain (RotationReady=False, DiscardReady=False, DualPw=False)  │
  │    │ ClusterManager: ALTER USER ... RETAIN on every instance             │
  │    ▼                                                                     │
  │  Promoting (DualPw=True)                                                 │
  │    │ Reconciler: promote pending → current in the controller Secret      │
  │    │            (one atomic Secret update; old values move to *_OLD)     │
  │    │            promote observedRotationGeneration                       │
  │    ▼                                                                     │
  │  AwaitingRollout (DiscardReady=False, DualPw=True)                       │
  │    │ MySQLClusterReconciler: distribute current passwords (normal path)  │
  │    │ Reconciler: wait for distribution to catch up, add restart          │
  │    │            annotation, watch StatefulSet rollout; once settled,     │
  │    │            DiscardReady→True (verification window opens)            │
  │    ▼                                                                     │
  │  AwaitingDiscard (DiscardReady=True, DualPw=True)                        │
  └──────────────────────────────────────────────────────────────────────────┘
       │
       │  Operator verifies apps work with new passwords
       │  kubectl moco discard-old-credential
       ▼
  ┌── Discard ───────────────────────────────────────────────────────────────┐
  │  spec.discardGeneration bumped (DiscardReady still True, DualPw=True)    │
  │    │ Reconciler: DiscardReady→False/Pending; emit DiscardStarted         │
  │    ▼                                                                     │
  │  ApplyingDiscard (DiscardReady=False/Pending, DualPw=True)               │
  │    │ ClusterManager (runs SQL only after DiscardReady=False/Pending):    │
  │    │            DISCARD OLD PASSWORD + auth plugin migration             │
  │    │            (connects with current; rollout already settled)         │
  │    ▼                                                                     │
  │  Finalizing (DualPw=False)                                               │
  │    │ Reconciler: delete *_OLD, ROTATION_ID, RETAIN_STARTED keys          │
  │    │            (cleanup only — no password movement); promote           │
  │    │            observedDiscardGen; RotationReady→True,                  │
  │    │            DiscardReady→False/Pending (back to Idle)                │
  │    ▼                                                                     │
  │  Idle                                                                    │
  └──────────────────────────────────────────────────────────────────────────┘
```

> In this diagram, `Cond→False/Pending` is compact notation for setting the condition to `status: False` with `reason: Pending`, and `DualPw` is short for `DualPassword`. The text and tables in this document use `Cond=False` (`reason=Pending`) instead.

## Key Design Decisions

### The Core Invariant

**The controller Secret's current passwords always authenticate on every MySQL instance**, at every point in the cycle and at every crash point:

- Before RETAIN completes: current = old password, which is the primary password on every instance.
- Promotion happens only after RETAIN succeeded on **all** instances, so at that moment every instance accepts both old and new.
- After promotion: current = new password, valid on every instance (as primary, with old as secondary).
- After a partial or complete DISCARD: current = new password, still valid on every instance.

The invariant gives every downstream consumer one simple rule: **read current, it works.** Consequences:

1. **One distribution path.** `MySQLClusterReconciler.reconcileV1Secret` always distributes current passwords. It needs no knowledge of rotation, no phase-dependent branching, and no "which password set do I distribute" logic. The CredentialRotationReconciler never writes per-namespace Secrets.
2. **Trivial crash recovery.** There is no "which password world am I in?" question. Any component that restarts reads current and reconnects.
3. **No unrecoverable state.** The only value that can be lost mid-cycle is the **old** password. The new password becomes canonical current at promotion; losing the old value only leaves a harmless secondary password in MySQL, which the next cycle's pre-check detects and reports.
4. **Non-destructive CR deletion.** Deleting the CR at any step never breaks connectivity; it can only leave residue (dual passwords in MySQL, stale keys in the controller Secret). The next cycle adopts `*_PENDING` residue and stops on `*_OLD` residue — see [CR deletion during rotation](#cr-deletion-during-rotation).

**The invariant depends on RETAIN being all-or-nothing.** The RETAIN loop must abort the promotion path if `ALTER USER ... RETAIN` cannot be verified on even one instance. Skipping an unreachable instance and promoting anyway would create an instance where the canonical current password does not authenticate — with the old password no longer canonical. This is recorded as a hard assumption (see [Assumptions](#assumptions)) and must be preserved by any future change to the RETAIN loop.

### Why MySQL Dual Password?

MySQL 8.0.14+ allows a user to have two valid passwords at once. `ALTER USER ... IDENTIFIED BY <new> RETAIN CURRENT PASSWORD` adds the new password as primary and keeps the old one as secondary; `ALTER USER ... DISCARD OLD PASSWORD` removes the secondary. MySQL only holds **one** secondary slot per user, so a second RETAIN would overwrite (and lose) the original old password — this is why double-execution must be prevented (see [Crash Safety](#crash-safety)).

### Why `sql_log_bin=0`?

MOCO supports cross-cluster replication. `ALTER USER` is a DDL written to the binary log; if propagated, a downstream cluster would receive the upstream's passwords and break its own credentials.

All rotation `ALTER USER` calls run in a dedicated `db.Conn` with `SET SESSION sql_log_bin=0`. As a consequence, within-cluster replicas also do not receive the change via replication, so `ALTER USER` must be executed on **every instance individually**.

### Why Auth Plugin Migration Happens After DISCARD

MySQL Error 3894 prevents changing the authentication plugin in a `RETAIN CURRENT PASSWORD` statement. Plugin migration is therefore deferred to a separate `ALTER USER ... IDENTIFIED WITH <plugin> BY ...` issued after DISCARD.

The target plugin is read from `@@global.authentication_policy` on the primary; if the first element is `*` or empty, `caching_sha2_password` is used. This lets each rotation also migrate users away from legacy plugins (e.g. `mysql_native_password`) without extra operator steps.

### Why Promote Immediately After RETAIN?

An earlier revision of this design kept the old password as canonical current until the very end of the cycle (promotion happened after DISCARD, in `Finalizing`). That ordering forced every component to answer "current or pending?" per step: `reconcileV1Secret` needed a phase-dependent `usePending` branch, the CredentialRotationReconciler had to write per-namespace Secrets itself during distribution, DISCARD had to connect with the pending password, and crash recovery needed a `CurrentPasswordsMatch` check to detect a half-finished promotion. It also created a genuinely unrecoverable failure mode: if the `*_PENDING` keys were lost while Pods were already using them, the new passwords were gone for good (MySQL stores only hashes).

Promoting right after RETAIN removes all of that. The trade-offs accepted in exchange:

- **Roll-forward only** (see below).
- The old password must be archived in `*_OLD` keys until the cycle completes, for recovery purposes.
- The all-or-nothing RETAIN assumption becomes load-bearing (see [The Core Invariant](#the-core-invariant)).

### Roll-forward Only

Once promotion happens, the new password is canonical and the designed path only moves forward (distribute → rollout → verify → discard). There is no designed rollback. If the operator finds a problem during the verification window, the options are:

- Complete the cycle (discard) and immediately start another rotation, or
- Follow the manual reset procedure (see [Recovery Procedures](#recovery-procedures)) — possible at any time before DISCARD because the old password still authenticates as the secondary, and its value is preserved in the `*_OLD` keys.

This matches the nature of credential rotation: the old credential must be treated as leaked, so returning to it is rarely the right response.

### Responsibility Split: Reconciler vs ClusterManager

The **CredentialRotationReconciler** handles K8s resource operations: condition transitions, controller Secret management (seed / promote / cleanup), StatefulSet rolling-restart annotation, distribution catch-up wait, StatefulSet rollout wait.

The **ClusterManager** handles DB operations: dual-password pre-checks, `ALTER USER RETAIN`, `DISCARD OLD PASSWORD`, auth plugin migration (with a temporary `super_read_only` toggle on the instances that run with it). It also writes the state that belongs to these DB operations: the `RETAIN_STARTED` marker in the controller Secret, the `DualPassword` condition, and the `Blocked` reason on the two Ready conditions.

The **MySQLClusterReconciler** distributes the controller Secret's current passwords to per-namespace Secrets — its normal job, unchanged by rotation.

Each step has one *driver* that does the work and then writes the change that moves the CR to the next step:

| Step | Driver | What the driver writes on completion |
|---|---|---|
| `ApplyingRetain` | ClusterManager | `DualPassword=True` |
| `Promoting` | Reconciler | promote `observedRotationGeneration` |
| `AwaitingRollout` | Reconciler (distribution itself: MySQLClusterReconciler) | `DiscardReady=True` |
| `AwaitingDiscard` | (steady state — operator action) | (operator bumps `discardGeneration`) |
| `ApplyingDiscard` (initial) | Reconciler | `DiscardReady=False` (`reason=Pending`) |
| `ApplyingDiscard` (DB work) | ClusterManager | `DualPassword=False` |
| `Finalizing` | Reconciler | promote `observedDiscardGeneration`, `RotationReady=True`, `DiscardReady=False` (`reason=Pending`) |

Inside `ApplyingDiscard`, both components can run. ClusterManager waits until it observes `DiscardReady=False` (`reason=Pending`) before it runs any SQL, so the `DiscardStarted` Event and that condition stay visible.

## CRD Definition

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata:
  name: my-cluster            # must match the target MySQLCluster name
  namespace: my-namespace     # same namespace as the MySQLCluster
spec:
  rotationGeneration: 1       # bump to trigger a new rotation
  discardGeneration: 1        # bump to match rotationGeneration to discard
status:                       # shown after the first cycle completed (Idle)
  observedGeneration: 2
  observedRotationGeneration: 1
  observedDiscardGeneration: 1
  rotationID: ""              # empty when no cycle is active
  conditions:                 # illustrative; real entries also carry
    - type: RotationReady     #   reason, message, lastTransitionTime
      status: "True"
    - type: DiscardReady
      status: "False"
    - type: DualPassword
      status: "False"
```

### Naming Convention

The CR name **must match** the target MySQLCluster name (same name, same namespace). This naturally enforces at most one active rotation per cluster and lets both controllers look up the CR by the cluster name without an extra reference field.

The CR is **long-lived** — created once and reused across rotation cycles. A new cycle is started by incrementing `spec.rotationGeneration`; the controller compares each `spec.*Generation` with the corresponding `status.observed*Generation` to detect new requests.

Both counters only increase. The validating webhook enforces `0 <= discardGeneration <= rotationGeneration`, and schema-level validation on the CRD (`minimum` markers and a CEL rule) enforces it again, so the rule also holds when the webhook is unavailable. Because the counters only increase, applying the same spec twice is a no-op, which works well with GitOps.

### OwnerReference

CredentialRotation sets an ownerReference to the target MySQLCluster so that Kubernetes garbage-collects it on cluster deletion.

### Spec / Status Fields

| Field | Type | Notes |
|---|---|---|
| `spec.rotationGeneration` | int64 | Required, `>= 1`, monotonically increasing. The webhook rejects any value other than `1` at create time, so the counter matches the number of rotation cycles one-to-one. Bump via update to start the next cycle. |
| `spec.discardGeneration` | int64 | Required, `>= 0`, `<= rotationGeneration`, monotonically increasing. The webhook rejects any value other than `0` at create time. Bump via update (typically to match `rotationGeneration`) to start discard. |
| `status.observedGeneration` | int64 | Standard `metadata.generation` echo for kstatus / ArgoCD / Flux. |
| `status.observedRotationGeneration` | int64 | Last rotationGeneration whose rotation phase (RETAIN + promote) completed. |
| `status.observedDiscardGeneration` | int64 | Last discardGeneration whose discard phase completed. |
| `status.rotationID` | string | UUID for the in-flight cycle (empty when no cycle is active). |
| `status.conditions` | `[]metav1.Condition` | See [Conditions](#conditions). |

### Conditions

The Kubernetes API conventions discourage `phase`-style enums and recommend Conditions instead. Each Condition describes an observed state in the positive sense: `True` means the state currently holds, and `Reason` describes why.

| Type | When `True` | When `False` |
|---|---|---|
| `RotationReady` | Idle steady state (`Step()==StepIdle`): no cycle in flight, no dual password held. Operator may bump `rotationGeneration`. | A cycle is in flight, the CR is in `AwaitingDiscard`, or the cycle is stuck (`Refused`/`Blocked`/`Stale`). |
| `DiscardReady` | Awaiting-discard steady state (`Step()==StepAwaitingDiscard`): rotation phase done, rollout settled, dual password held. Operator may bump `discardGeneration`. | A cycle is in flight that is not yet awaiting-discard, the CR is idle, the discard phase is in flight, or it is stuck. |
| `DualPassword` | MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD). | No dual-password state in MySQL. |

`DualPassword` can also be `Unknown` (`reason=Unverified`) right after a Stale recovery, when the reconciler could not verify the MySQL state — see the [Reason values](#reason-values) below.

> `RotationReady` and `DiscardReady` are **not** equivalent to `IsIdle()` / `IsAwaitingDiscard()` in the strict sense — `IsIdle()` also returns true for `StepRotationRefused` (where `RotationReady=False` with `reason=Refused`), since nothing has been mutated and a retry is safe. The webhook uses the `IsIdle()` / `IsAwaitingDiscard()` predicates to decide whether a `rotationGeneration` / `discardGeneration` bump is allowed.

**Using `kubectl wait`.** `kubectl wait --for=condition=RotationReady` waits for Idle (previous cycle fully done, next rotate allowed). `kubectl wait --for=condition=DiscardReady` waits for AwaitingDiscard (rollout settled, discard allowed). The two are never used together.

#### Reason values

Each `Reason` has a single meaning across every condition that uses it. A
Reason is always paired with a fixed `Status` value — for example, `Reconciled`
only appears with `Status=True`, `Pending` / `Refused` / `Blocked` / `Stale`
only appear with `Status=False`, and `Unverified` only appears with
`Status=Unknown`.

| Reason | Appears on condition | With status | Meaning |
|---|---|---|---|
| `Reconciled` | `RotationReady` or `DiscardReady` | `True` | The matching steady state (Idle / AwaitingDiscard). |
| `Pending` | `RotationReady` or `DiscardReady` | `False` | Not in the matching steady state, no error recorded — the cycle is in flight, or the other Ready is True. |
| `Refused` | `RotationReady` or `DiscardReady` | `False` | The requested operation could not start (e.g. `replicas == 0`). Nothing has been mutated. |
| `Blocked` | `RotationReady` or `DiscardReady` | `False` | A started cycle cannot progress (e.g. cluster scaled to 0 after pending passwords were written). Manual recovery required. |
| `Stale` | `RotationReady` or `DiscardReady` | `False` | The controller Secret (or other persisted state) is inconsistent. Manual recovery required; once the controller Secret is cleaned, the controller aborts the stuck cycle and returns the CR to Idle by itself (`RotationRecovered` Event). |
| `Retained` | `DualPassword` | `True` | MySQL holds a dual-password set on all system users. |
| `NotRetained` | `DualPassword` | `False` | MySQL is not currently holding a dual-password set. |
| `Unverified` | `DualPassword` | `Unknown` | Set only during Stale recovery: the reconciler never connects to MySQL, so it cannot claim `False`. The next cycle's pre-check verifies the real state. `Step()` treats `Unknown` like `False`, so the CR still derives `Idle`. |

> **Events.** In addition to the condition transitions, the controllers emit Kubernetes Events for `kubectl describe` visibility. Their reasons are distinct from the condition `Reason` values above. Events on the CredentialRotation (emitted by the Reconciler): `RotationStarted`, `PasswordsPromoted`, `AwaitingDiscard`, `DiscardStarted`, `RotationCompleted`, `RotationRecovered`, and the Warnings `RotationRefused`, `DiscardRefused`, `RotationPaused`, `RotationPendingError`, `InconsistentState`, `StaleCredentialRotation`. Events on the MySQLCluster (emitted by the ClusterManager): `RetainApplied`, `DiscardApplied`, and the Warnings `RotationBlocked`, `DiscardBlocked`, `DualPasswordExists`, `PasswordRotationError`.

#### Step matrix

The internal workflow step is derived from the three Conditions plus the generation comparisons; it is **not** stored on the CR.

| Step | `RotationReady` | `DiscardReady` | `DualPassword` | Outstanding phase |
|---|---|---|---|---|
| Initial (`RotationReady` absent — treated as `Idle`) | — | — | — | — |
| `Idle` | **True** | False | False | — |
| `ApplyingRetain` | False | False | False | `rotation` |
| `Promoting` | False | False | **True** | `rotation` |
| `AwaitingRollout` | False | False | True | — |
| `AwaitingDiscard` | False | **True** | True | — |
| `ApplyingDiscard` | False | False | True | `discard` |
| `Finalizing` | False | False | False | `discard` |

Besides the steps above, a condition with reason `Refused`, `Blocked`, or `Stale` selects an error step: `RotationRefused`, `RotationBlocked`, `DiscardRefused`, `DiscardBlocked`, or `StalePending`. The paragraphs below describe how these interact with the matrix.

"Outstanding phase" shows which phase the operator has requested but the controller has not yet recorded in `observed*Generation`. It is computed from spec and status each time:

- `rotation` when `spec.rotationGeneration > status.observedRotationGeneration`
- `discard` when `spec.discardGeneration > status.observedDiscardGeneration`
- `—` when both are in sync

The two values are mutually exclusive: the rotation phase always completes — promoting `observedRotationGeneration` — before the operator can request discard. This is why the matrix can distinguish the three `(False, False, True)` rows: `Promoting` has `rotation` outstanding, `AwaitingRollout` has neither outstanding, and `ApplyingDiscard` has `discard` outstanding.

A `Status=False` condition with the `Stale` reason takes precedence over
everything else: the CR is `StalePending` until recovery. The `Refused` and
`Blocked` reasons are checked only while the matching phase is outstanding:
`RotationReady=False` (`reason=Refused`/`Blocked`) selects
`RotationRefused`/`RotationBlocked` while a rotation is outstanding, and
`DiscardReady` works the same way for the discard steps.

When a new `rotationGeneration` is requested, an old
`RotationReady=True` condition may remain until the next status update.
This old condition is treated as the previous cycle's state, so the CR is
still handled as Idle and the rotation seed handler can start the new cycle.
An old `DiscardReady=True` condition needs no special handling: with a discard
outstanding and `DualPassword=True`, the CR goes straight to
`ApplyingDiscard` (the `DiscardReady` value is not consulted there), and its
handler flips the condition to `False` (`reason=Pending`). Either way, a
stale condition from the previous state cannot stop the new request.

When both generations are in sync, `DualPassword` is the authoritative
signal: `True` means `AwaitingRollout` (or `AwaitingDiscard` once
`DiscardReady=True`), `False` means `Idle` — whatever the Ready conditions
say. The matrix rows above show the canonical combinations that the
controller itself writes.

#### Why three conditions?

`RotationReady` and `DiscardReady` show whether the operator may start the next action. They are similar to a Pod's `Ready=True` condition, which means that the Pod can receive traffic. These two Ready conditions cannot be `True` at the same time: the Idle and AwaitingDiscard steady states are different states. This makes the current state clear when the controller reads the CR. `DualPassword` has a different role: it shows whether MySQL currently has a secondary password. It is named after the state it describes, like the `MemoryPressure` condition, rather than describing whether the system is healthy.

The controller calculates the current step from these three conditions and the generation values. It therefore does not need to store a separate `Phase` field in the CR or make an additional API request at runtime. This follows the Kubernetes convention of using Conditions to describe resource state.

### Validation Webhook

**ValidateCreate:**

All of the following must be true.

- The target MySQLCluster (same name, same namespace) must exist.
- `cluster.Spec.Replicas` must be `> 0`.
- `rotationGeneration` must equal `1` (counter starts at 1 by convention).
- `discardGeneration` must equal `0`. Discard must be requested via update after the CR reaches the awaiting-discard steady state. A non-zero value at create time would skip `AwaitingRollout` and the verification window.

**ValidateUpdate:**

The following checks apply to every update, whatever fields changed:

- `rotationGeneration` must be greater than or equal to its old value.
- `discardGeneration` must be greater than or equal to its old value.
- `discardGeneration <= rotationGeneration`.

In addition, the following condition applies when the corresponding generation is increased:

- If `rotationGeneration` increases, both of the following must be true:
  - `oldCR.IsIdle()` is true. This means the old Step is `Idle` or `RotationRefused`; nothing was mutated, so a retry is safe. A previously stuck cycle (`Blocked` / `Stale`) must be cleared through the recovery procedure before a new request.
  - The live target MySQLCluster exists and has `cluster.Spec.Replicas > 0`. This is rechecked when the request is applied; the controller also re-checks it at reconcile time to handle scale-downs after admission.
- If `discardGeneration` increases, `oldCR.IsAwaitingDiscard()` must be true. The old Step must be `AwaitingDiscard`, which means the post-promotion rollout has settled and the verification window is open. As a consequence, `rotationGeneration` and `discardGeneration` cannot be increased in the same update — that would skip the `AwaitingRollout` gate and the verification window.

**ValidateDelete:**

Deletion is **always allowed** — there is no delete webhook.

The core invariant makes CR deletion non-destructive: the per-namespace Secrets always hold the canonical current passwords, which authenticate on every instance regardless of when the CR disappears. Deleting the CR mid-cycle can only leave **residue**:

- dual passwords (a harmless secondary slot) on some or all instances, and
- stale `*_PENDING` / `*_OLD` / `ROTATION_ID` / `RETAIN_STARTED` keys in the controller Secret.

Neither affects a running cluster. `*_PENDING` residue does not even block the next cycle: the seed handler reuses the leftover `ROTATION_ID` and pending passwords — they are random values that were never promoted, so adopting them is equivalent to generating fresh ones — and RETAIN resumes where the abandoned cycle stopped. `*_OLD` residue **does** block the next cycle: the seed handler refuses to overwrite the promoted-state bookkeeping until it is cleaned up with the [Recovery Procedures](#recovery-procedures). This also removes the previous design's deadlocks around cluster termination and stale CRs: garbage collection is never blocked by a webhook.

## User Interface

| Command | Behavior |
|---|---|
| `kubectl moco rotate-credential <cluster>` | If CR does not exist: create with `(rotationGeneration: 1, discardGeneration: 0)`. If CR exists: refuse if stale; require `cr.IsIdle()`; increment `rotationGeneration` with an `Update` so a concurrent modification fails with a Conflict instead of being lost. |
| `kubectl moco discard-old-credential <cluster>` | Refuse if stale; require `cr.IsAwaitingDiscard()`; set `spec.discardGeneration` to `spec.rotationGeneration` with an `Update` (same Conflict semantics). |
| `kubectl moco credential <cluster>` | Read the per-namespace user Secret (unchanged from previous releases). |

Both mutating commands also check, at the time they run, whether the cluster can make progress. They refuse when `spec.offline` is `true`, when the `moco.cybozu.com/clustering-stopped` annotation is set to `true`, or when the MySQLCluster is not `Healthy`. In addition:

- `rotate-credential` refuses when `moco.cybozu.com/reconciliation-stopped=true`, because the rotation phase depends on MySQLClusterReconciler distributing the promoted passwords. It also refuses when `cluster.Spec.Replicas` is 0 — the webhook would reject the bump anyway, but the CLI fails faster with a clearer message.
- `discard-old-credential` needs neither extra check, because distribution finished before the CR reached `AwaitingDiscard`.

The webhook and the controller remain the authority; these checks only fail fast with a clear message.

`kubectl get credentialrotation` prints `ROTATIONREADY` / `DISCARDREADY` / `DUALPASSWORD` (the three condition statuses) plus the four generation columns and `AGE`.

### GitOps / ArgoCD

The CR is long-lived and purely declarative, so it works naturally with GitOps. The lifecycle is driven by committing `rotationGeneration` / `discardGeneration` bumps; each commit triggers an ArgoCD sync that advances the cycle. No imperative CLI calls or CR deletions are required for normal operation.

**Do not mix GitOps with `kubectl moco rotate-credential` / `discard-old-credential`.** The CLI writes the same spec fields GitOps manages. If the CLI bumps a counter, GitOps reconciliation will try to roll it back, but the webhook rejects any decrease — leaving the resource permanently `OutOfSync`. Worse, the CLI-triggered phase already mutates MySQL passwords irreversibly. Pick one source of truth per environment.

## Rotation Phase

### Reconciler: Idle → ApplyingRetain

Triggered when the outstanding phase is `rotation` (i.e. `spec.rotationGeneration > status.observedRotationGeneration`) and the CR is in `Step ∈ {Idle, RotationRefused, RotationBlocked}` (from the latter two, only while `cluster.Spec.Replicas > 0`). The handler itself refuses first when `cluster.Spec.Replicas` is 0 — see [Scaled-down Clusters](#scaled-down-clusters-replicas0).

| # | Action | Persistence |
|---|---|---|
| 1 | Take the `ROTATION_ID` stored in the controller Secret, or generate a new UUID when there is none. Reusing the stored ID makes this step idempotent across crashes, and also adopts the residue of an abandoned cycle (see ValidateDelete). | — |
| 2 | Write 8 `*_PENDING` keys and `ROTATION_ID` into the controller Secret. A complete pending set that already matches the `ROTATION_ID` is kept as is. If the Secret is inconsistent or still holds `*_OLD` keys, emit a `RotationPendingError` Warning Event and set `RotationReady=False` (`reason=Stale`) instead — that branch writes only the status, not the Secret. | Secret.Update |
| 3 | Set `status.rotationID`, `RotationReady=False` (`reason=Pending`), `DiscardReady=False` (`reason=Pending`), `DualPassword=False` (`reason=NotRetained`). Emit `RotationStarted` Event. | Status.Update |

On the Refused and Stale branches, conditions that are still missing on a fresh CR are seeded with their default `False` values first, so `RotationReady` is the only condition that carries a non-default reason.

### ClusterManager: ApplyingRetain → Promoting

Triggered on a ClusterManager tick when `cr.Step() == StepApplyingRetain` (that is, the `rotation` phase is outstanding, `DualPassword=False`, and no `Refused`/`Blocked`/`Stale` condition takes precedence). The handler first checks the controller Secret: it waits until the Reconciler has written this cycle's `*_PENDING` keys, and it refuses a Secret that is already promoted for this rotationID — that combination contradicts `DualPassword=False` and needs manual recovery. Next, if the cluster was scaled down to 0 replicas, it emits a `RotationBlocked` Warning Event and sets `RotationReady=False` (`reason=Blocked`) — see [Scaled-down Clusters](#scaled-down-clusters-replicas0).

| # | Action | Persistence |
|---|---|---|
| 1 | Pre-check: every instance is scanned for pre-existing dual passwords. Steps 1 and 2 are both skipped when the `RETAIN_STARTED` marker already holds this cycle's rotationID (crash recovery). | — |
| 2 | Set `RETAIN_STARTED` marker (rotationID) in the controller Secret. | Secret.Update |
| 3 | For each instance, connect with the current password. If the instance is a replica, or the intermediate primary when `spec.replicationSourceSecretName` is set, temporarily disable `super_read_only` (these instances run with it enabled) and re-enable it after the updates. Run `ALTER USER ... RETAIN CURRENT PASSWORD` for each user, skipping users that already have a dual password. | MySQL |
| 4 | Set `DualPassword=True` (`reason=Retained`) and `RotationReady=False` (`reason=Pending`) in one status update — the latter clears a previous `Blocked` reason, if any. Emit `RetainApplied` Event on the MySQLCluster. | Status.Update |

#### Pre-check and crash recovery

If any instance already has a dual password from outside this rotation cycle, emit a `DualPasswordExists` Warning Event and wait. After the pre-check succeeds, store the marker before running RETAIN. If the controller crashes and restarts, the marker — when it holds this cycle's rotationID — tells the handler to skip the pre-check and resume RETAIN. The `HasDualPassword` check for each user makes retries safe and idempotent.

#### All-or-nothing

Step 3 aborts on the first instance that cannot be reached or fails `ALTER USER`. `DualPassword=True` is only written after **every** instance holds the dual-password set. This is the precondition that makes the next step (promotion) safe — see [The Core Invariant](#the-core-invariant).

### Reconciler: Promoting → AwaitingRollout

Triggered when `cr.Step() == StepPromoting` (that is, the `rotation` phase is outstanding and `DualPassword=True`). At this point every instance accepts both the old and the new password, so the new password can safely become canonical.

| # | Action | Persistence |
|---|---|---|
| 1 | Validate the controller Secret with `RotationState`: all 8 `*_PENDING` keys present with matching `ROTATION_ID` → proceed. If instead all 8 `*_OLD` keys are present with matching `ROTATION_ID` and no `*_PENDING` keys remain, promotion already happened (crash recovery) — skip to step 3. A clean Secret means the pending passwords were lost without promotion: emit an `InconsistentState` Warning Event, set `RotationReady=False` (`reason=Stale`). Any other state (partial key groups, `ROTATION_ID` mismatch) is inconsistent: emit a `RotationPendingError` Warning Event, set `RotationReady=False` (`reason=Stale`). | — |
| 2 | **One atomic Secret update**: copy current values to `*_OLD` keys, copy `*_PENDING` values to current keys, delete the `*_PENDING` keys and `RETAIN_STARTED`. `ROTATION_ID` is kept until the cycle completes. | Secret.Update |
| 3 | Record the rotation as complete by setting `status.observedRotationGeneration` to `spec.rotationGeneration`; emit `PasswordsPromoted` Event. Keep `DiscardReady=False` (`reason=Pending`) until the StatefulSet rollout is complete. | Status.Update |

The presence of the `*_OLD` key group also serves as the "promotion done" marker: no extra revision annotation is needed to distinguish "promoted, status not yet updated" from "not yet promoted" after a crash.

From this moment the invariant flips from "current = old" to "current = new". Both remain true statements on every instance because of the dual-password window.

### AwaitingRollout: distribution and rollout

Triggered when `cr.Step() == StepAwaitingRollout` (that is, `DualPassword=True` and neither phase is outstanding, with `DiscardReady=False`).

Distribution itself is **not** performed by the CredentialRotationReconciler. `MySQLClusterReconciler.reconcileV1Secret` distributes the controller Secret's current passwords — its normal behavior — and a watch on the controller Secret triggers it promptly after promotion (see [MySQLClusterReconciler](#mysqlclusterreconciler)).

The CredentialRotationReconciler gates progress:

| # | Action | Persistence |
|---|---|---|
| 1 | Wait until the per-namespace user Secret and `my.cnf` Secret are derived from the controller Secret's **current** passwords (content comparison; `CurrentPasswordsMatch` for the user Secret). If not yet, requeue — MySQLClusterReconciler will catch up. | — |
| 2 | Add `moco.cybozu.com/password-rotation-restart: <rotationID>` to the StatefulSet pod template with server-side apply (SSA), under field manager `moco-credential-rotation` with `ForceOwnership`. Skip the apply when the pod template already carries **this rotation's** annotation value. | StatefulSet.Apply |
| 3 | Check whether the StatefulSet rollout is complete — only against a pod template that carries this rotation's annotation, never against a stale pre-annotation object. Confirm that `status.observedGeneration` has caught up with `metadata.generation`, `status.currentRevision` matches `status.updateRevision`, and `status.replicas`, `status.updatedReplicas`, and `status.readyReplicas` all equal the desired `spec.replicas`. If any check is not satisfied, do not mark the rollout complete; requeue and check again later. | — |
| 4 | After all rollout checks pass, set the `DiscardReady` condition to `True` with reason `Reconciled`. This records that the new password has been distributed and all Pods are ready, so the verification window is open. Emit an `AwaitingDiscard` Event to report that the operator may request the discard step. | Status.Update |

The skip condition in step 2 compares the annotation **value**, not just its presence: a template that still carries the *previous* cycle's rotationID is re-applied, and that re-apply is what triggers this cycle's rolling restart. The skip itself is required because every StatefulSet update that is not a pure partition change — including a content-no-op re-apply — passes the `StatefulSetDefaulter` mutating webhook, which resets `spec.updateStrategy.rollingUpdate.partition` to `replicas` to guard rollouts. Re-applying on each reconcile would keep resetting the partition that the partition controller walks down, and the rollout would never complete.

#### Why the annotation waits for distribution (step 1 before step 2)

Connectivity is never at risk. A Pod that restarts early reads the old password from the namespace Secret, which has not been updated yet, and the old password still works during the dual-password window. The ordering protects the **discard gate**: if the annotation triggered a rollout while the namespace Secret still held old values, the rollout could settle with Pods running on the old password, `DiscardReady` would flip to `True`, and a subsequent DISCARD would remove the very password those Pods use.

#### Why the reconciler waits for the rollout

The verification window is safe only after every Pod has restarted and its MySQL clients have picked up the new password from the distributed Secrets. For example, `moco-agent` reads credentials from the per-namespace user Secret, and `mysqld-exporter` reads them from the `my.cnf` Secret. If the controller set `DiscardReady=True` before the rollout finished, `kubectl wait --for=condition=DiscardReady` could return while some Pods' clients still used the old password. An automation script could then start the discard step. `DISCARD OLD PASSWORD` would remove the secondary password that those clients still need, and they could lose access to MySQL.

The rollout is a Kubernetes operation, so the CredentialRotation Reconciler checks the StatefulSet status and opens the verification window. The ClusterManager performs the MySQL `DISCARD` operation only after this check has succeeded.

## Discard Phase

### Reconciler: AwaitingDiscard → ApplyingDiscard

Triggered when the outstanding phase is `discard` (i.e. `spec.discardGeneration > status.observedDiscardGeneration`) and the CR is in `Step ∈ {ApplyingDiscard, DiscardRefused, DiscardBlocked}`. (Once the discard is outstanding, `Step()` never returns `AwaitingDiscard` — the CR moves straight to `ApplyingDiscard`. From `DiscardRefused`/`DiscardBlocked`, the handler is retried only while `cluster.Spec.Replicas > 0`.)

| # | Action | Persistence |
|---|---|---|
| 1 | If `cluster.Spec.Replicas <= 0`: emit `DiscardRefused` Warning Event and set `DiscardReady=False` (`reason=Refused`). The webhook forbids reverting `discardGeneration` once bumped, so a Refused state at this point simply waits for the cluster to be scaled up. | Status.Update |
| 2 | Whenever `DiscardReady` is not already `False` (`reason=Pending`) — the initial bump, or recovery from `Refused` or `Blocked` — set `DiscardReady=False` (`reason=Pending`), emit `DiscardStarted` Event, requeue. Once it is `False` (`reason=Pending`), subsequent reconciles just requeue while ClusterManager drives DISCARD. | Status.Update |

### ClusterManager: ApplyingDiscard → Finalizing

Triggered on a ClusterManager tick when `cr.Step() == StepApplyingDiscard` (that is, the `discard` phase is outstanding, `DualPassword=True`, and no `Refused`/`Blocked`/`Stale` condition takes precedence). The handler runs three pre-checks before any SQL, in this order:

1. The controller Secret must be in the **promoted** state for this rotationID (the `*_OLD` group with a matching `ROTATION_ID`, which stays in the Secret until `Finalizing`). Any other state — pending keys still present, or a Secret without bookkeeping keys (e.g. restored from a pre-rotation backup) — cannot prove that the current keys hold the promoted passwords, so the handler refuses to run DISCARD and reports the error (`PasswordRotationError` Warning Event) instead of risking the core invariant.
2. If the cluster was scaled down to 0 replicas, emit a `DiscardBlocked` Warning Event and set `DiscardReady=False` (`reason=Blocked`); the discard resumes when the cluster is scaled back up.
3. `DiscardReady` must already be `False` (`reason=Pending`), written by the CredentialRotationReconciler after the discard request is observed. If not yet, ClusterManager skips rotation work for that tick and retries on a later tick. This ordering records the in-flight state and emits the `DiscardStarted` Event before the DISCARD SQL; it is not a blocking wait or a rollout gate — the post-promotion rollout already settled before `DiscardReady` could become `True` in `AwaitingRollout`.

| # | Action | Persistence |
|---|---|---|
| 1 | Determine the target auth plugin via `GetAuthPlugin` on the primary. | MySQL (read-only) |
| 2 | For each instance, connect with the **current** password. If the instance is a replica, or the intermediate primary when `spec.replicationSourceSecretName` is set, temporarily disable `super_read_only` (these instances run with it enabled) and re-enable it after the updates. Run `DISCARD OLD PASSWORD` for each user, skipping users that no longer have a dual password; then, in a second pass, migrate each user's auth plugin, skipping users already on the target plugin. | MySQL |
| 3 | Set `DualPassword=False` (`reason=NotRetained`). Emit `DiscardApplied` Event on the MySQLCluster. | Status.Update |

**Connecting with the current password is always correct.** DISCARD removes the *secondary* (old) password; the current (new) password is the primary and is unaffected before, during, and after DISCARD. There is no ordering hazard here — this is the invariant at work.

### Reconciler: Finalizing → Idle

Triggered when `cr.Step() == StepFinalizing` (that is, the `discard` phase is outstanding and `DualPassword=False`). This step is **bookkeeping only** — no password values move. The canonical current passwords were already promoted before distribution.

| # | Action | Persistence |
|---|---|---|
| 1 | Delete the `*_OLD` keys, `ROTATION_ID`, and `RETAIN_STARTED` from the controller Secret. Deleting absent keys is a no-op, so a crash-retry re-runs safely. | Secret.Update |
| 2 | Promote `observedDiscardGeneration = spec.discardGeneration`. Clear `status.rotationID`. Set `RotationReady=True` (`reason=Reconciled`), `DiscardReady=False` (`reason=Pending`). Emit `RotationCompleted` Event. | Status.Update |

If the Secret holds any other inconsistent state at this point (unpromoted pending keys, a partial `*_OLD` group, or a `ROTATION_ID` from a different cycle — only possible through manual tampering), the handler emits a `RotationPendingError` Warning Event and sets `DiscardReady=False` (`reason=Stale`), instead of hiding the inconsistency by deleting the bookkeeping around it.

## Scaled-down Clusters (replicas=0)

A cluster with 0 replicas stops rotation at three points:
- At admission: the webhook rejects CR creation or a `rotationGeneration` bump when `cluster.Spec.Replicas <= 0`.
- At reconcile time, before any mutation: if the cluster was scaled down to 0 after admission, `handleStartRotation` emits a `RotationRefused` Warning Event and sets `RotationReady=False` (`reason=Refused`). Nothing has been mutated; the CR stays in `Step=RotationRefused` and the request is retried automatically.
- If the cluster is scaled down to 0 *after* pending passwords were written, the ClusterManager handler emits a `RotationBlocked` Warning Event and sets `RotationReady=False` (`reason=Blocked`). Recovery requires either scaling the cluster back up (the reconciler retries automatically once `cluster.Spec.Replicas > 0` again) or following the recovery procedure.

A scale-down after promotion does not park the cycle in `AwaitingRollout`: a 0-replica StatefulSet counts as settled, so the CR still reaches `AwaitingDiscard`. The discard handlers then stop at 0 replicas: the Reconciler sets `DiscardReady=False` (`reason=Refused`) and ClusterManager sets `reason=Blocked`, and whichever observes the scale-down first wins (see [Discard Phase](#discard-phase)). Both are safe pauses, and both resume on scale-up. In all of these paused states the cluster keeps working with the canonical current passwords.

## Stopped Clustering or Reconciliation

The [stop clustering / stop reconciliation feature](../usage.md#stop-clustering-and-reconciliation) also pauses a rotation in flight:

- With **reconciliation stopped**, `MySQLClusterReconciler` does not distribute the promoted passwords, so the cycle pauses in `AwaitingRollout`. The pause only happens while distribution has not caught up yet: if the annotation is added after the promoted passwords were already distributed, the remaining rollout continues (the partition controller does not depend on `MySQLClusterReconciler`) and the cycle completes normally.
- With **clustering stopped**, the ClusterManager loop is paused, so `ApplyingRetain` and `ApplyingDiscard` cannot run their SQL.

This is intentional: the stop annotations are explicit operator requests, and the rotation controllers must not bypass them. The paused states are safe — the current passwords keep authenticating everywhere — and the cycle resumes automatically when the operator runs `kubectl moco start clustering` / `start reconciliation`. While paused, the Reconciler emits a `RotationPaused` Warning Event on the CredentialRotation that names the blocking annotation. The CLI refuses to *start* a step that a stop annotation would pause (see [User Interface](#user-interface)); the Events cover the case where the annotation is added mid-cycle.

A discard in flight is not affected by stopped reconciliation: the promoted passwords were distributed before `AwaitingDiscard`, and the discard phase runs on the ClusterManager and the CredentialRotationReconciler only.

## Controller Secret Layout

The controller Secret (in the controller namespace) always holds the canonical current passwords, plus rotation bookkeeping keys during a cycle:

```
ADMIN_PASSWORD:         <current>  # always present; always authenticates
AGENT_PASSWORD:         <current>  #   on every instance (the invariant)
…
ADMIN_PASSWORD_PENDING: <new>      # seed → promotion
AGENT_PASSWORD_PENDING: <new>      # seed → promotion
…
ADMIN_PASSWORD_OLD:     <previous> # promotion → cycle completion (recovery only)
AGENT_PASSWORD_OLD:     <previous> # promotion → cycle completion (recovery only)
…
ROTATION_ID:            <uuid>     # seed → cycle completion
RETAIN_STARTED:         <uuid>     # pre-check pass → promotion (crash-safety marker)
```

Key-group validation is all-or-nothing, implemented by a single entry point, `RotationState(secret, expectedRotationID)`:

- `Pending` — all 8 `*_PENDING` keys and a matching `ROTATION_ID` are present together.
- `Promoted` — all 8 `*_OLD` keys and a matching `ROTATION_ID` are present together.
- `Clean` — no bookkeeping key is present. (A leftover `RETAIN_STARTED` marker alone still counts as `Clean`; it is harmless and is removed at the next cycle's promotion or `Finalizing`.)

Partial states (some keys of a group missing, `ROTATION_ID` without either group, or a rotation-ID mismatch) are reported as an error. The two groups never coexist: promotion replaces the pending group with the old group in a single Secret update.

The `*_OLD` keys exist **only for recovery**: no controller logic reads them on the happy path. They preserve the previous password's plaintext (MySQL stores only hashes) so an operator can identify and reset the secondary password if a cycle is abandoned, and they serve as the promotion marker for crash recovery.

### Why a Single Controller Secret (No Candidate Secret)?

An alternative is to stage the new password in a separate Secret owned by the CredentialRotation CR (an immutable "candidate" Secret). Staging inside the controller Secret is preferred because:

1. **Promotion is the invariant flip and must be atomic.** Renaming keys within a single object is one atomic `Update`. With two objects, promotion becomes a cross-object copy that needs its own revision marker to be crash-safe.
2. **Fewer objects holding raw credentials.** A candidate Secret adds one more object with plaintext passwords to reason about for RBAC, garbage collection, and stale-CR handling.
3. **Key groups double as state markers.** The presence of the `*_PENDING` group vs. the `*_OLD` group encodes "not yet promoted" vs. "promoted" for free; a separate Secret would need explicit cross-references.

## Component Details

### CredentialRotationReconciler (new)

The reconciler watches:
- `CredentialRotation` (primary watched resource).
- `MySQLCluster` (update events filtered to `Spec.Replicas` changes and `DeletionTimestamp` flips; create and delete events pass through), mapped to the same namespace/name — so a `Refused` / `Blocked` cycle resumes immediately on scale-up.
- `Secret` in the system namespace (the predicate filters on the namespace; the mapping function then parses the `mysql-<ns>.<name>` naming pattern), so promotion-related Secret changes are picked up — and manual cleanup of a Stale controller Secret triggers the automatic recovery — without waiting for the 15-second requeue.
- `Secret` in cluster namespaces (the per-namespace user Secret `moco-<name>` and `my.cnf` Secret `moco-my-cnf-<name>`), so the `AwaitingRollout` distribution catch-up check runs as soon as MySQLClusterReconciler redistributes. A Secret named `moco-my-cnf-<x>` is ambiguous (it is also the user Secret of a cluster literally named `my-cnf-<x>`), so both interpretations are enqueued; the extra request is a cheap no-op.
- `StatefulSet` (`moco-<name>`), so `DiscardReady=True` flips as soon as the rollout settles instead of on the next periodic requeue.

For steps owned by ClusterManager (`ApplyingRetain`, `ApplyingDiscard` DB work), the reconciler requeues every 15s while observing the condition for progress.

The rolling-restart annotation is applied with the dedicated field manager `moco-credential-rotation` and `ForceOwnership`. This keeps the annotation owned by the credential rotation reconciler, so the regular `MySQLClusterReconciler` does not remove it during its next reconcile.

The reconciler never writes per-namespace Secrets.

### ClusterManager

ClusterManager reads the CredentialRotation CR inside each tick and dispatches on `cr.Step()`:
- `ApplyingRetain` → run the RETAIN flow on this cluster.
- `ApplyingDiscard` → run the DISCARD flow (only after observing `DiscardReady=False` (`reason=Pending`), written by the Reconciler).
- Any other step → no-op for rotation; normal clustering continues.

A CR whose ownerReference UID does not match the live cluster (stale CR) is ignored. ClusterManager applies a stricter rule than the stale-CR definition used elsewhere: it also ignores a CR that has no MySQLCluster ownerReference yet, and waits until the Reconciler has adopted it.

### MySQLClusterReconciler

`reconcileV1Secret` is **unchanged by rotation**: it always distributes the controller Secret's current passwords to the per-namespace user Secret and `my.cnf` Secret. It does not read the CredentialRotation CR and has no rotation-specific branches. Thanks to the core invariant, whatever it distributes — pre-promotion old values or post-promotion new values — authenticates on every instance.

One addition is needed for **promptness** (not correctness): a watch on the controller Secret in the system namespace (filtered by the `mysql-<ns>.<name>` naming pattern, mapped to the owning cluster — the same pattern as the existing moco-agent certificate watch). Without it, redistribution after promotion would wait for the next unrelated reconcile. The CredentialRotationReconciler's `AwaitingRollout` step waits on the result, so distribution latency directly delays the verification window.

## Crash Safety

Every row below preserves the core invariant: at each crash point, the controller Secret's current passwords authenticate on every instance, so no component ever loses access.

| Crash point | Recovery |
|---|---|
| `rotationGeneration` bumped, pending passwords not yet generated | Reconciler re-generates on next reconcile |
| Pending passwords generated, RETAIN not started | ClusterManager picks up `Step=ApplyingRetain` |
| Pre-check passed, `RETAIN_STARTED` marker set, RETAIN not yet executed | Marker skips pre-check; `HasDualPassword` makes RETAIN idempotent |
| RETAIN partially applied | `RETAIN_STARTED` marker + per-user `HasDualPassword` makes re-execution safe |
| RETAIN complete, `DualPassword=True` not yet written | Re-run sees all users already retained → writes the condition transition |
| `Promoting`, Secret not yet promoted | Promotion is a single atomic update; re-run performs it |
| `Promoting`, Secret promoted but status not updated | `*_OLD` group present with matching `ROTATION_ID` and no `*_PENDING` group → promotion done → writes `observedRotationGeneration` |
| `AwaitingRollout`, distribution not yet caught up | MySQLClusterReconciler self-heals; rotation reconciler requeues |
| `AwaitingRollout`, restart annotation applied, rollout not settled | The pod template already carries this rotation's annotation → re-apply is skipped; the rollout check proceeds |
| `AwaitingRollout`, rollout still in flight | Reconciler re-checks StatefulSet status; flips `DiscardReady=True` once it settles |
| `ApplyingDiscard`, `DiscardReady` not yet flipped to `False` (`reason=Pending`) | Reconciler flips it on next reconcile; ClusterManager skips DISCARD until it observes the condition |
| `ApplyingDiscard`, `DiscardReady=False` (`reason=Pending`), DISCARD not yet executed | ClusterManager picks up the step |
| DISCARD partially applied | `HasDualPassword` gates DISCARD → re-run skips finished users |
| DISCARD done on an instance, auth plugin not yet migrated | The plugin comparison gates migration → re-run migrates only users still on the old plugin |
| DISCARD complete, `DualPassword=False` not yet written | Re-run skips all users → writes the condition transition |
| `Finalizing`, keys deleted but status not updated | The Secret is already clean → cleanup is a no-op → status update re-runs (key deletion itself is one atomic Secret update, so a partial deletion cannot occur) |

### Why `HasDualPassword` instead of per-user status tracking?

MySQL holds only one secondary password slot per user. A second RETAIN with the same pending password would overwrite the secondary slot — evicting the original old password and breaking the controller's ability to connect. Per-user progress stored in Kubernetes status could drift from the real MySQL state. Instead, ClusterManager queries MySQL directly (`mysql.user.User_attributes` for `additional_password`), so MySQL stays the source of truth. The query is read-only and safe to re-run.

### Idempotency of DISCARD

`ALTER USER ... DISCARD OLD PASSWORD` is a no-op when there is no secondary password to discard. The DISCARD handler still queries `HasDualPassword` per user and skips users whose secondary password is already gone — mirroring the RETAIN gate and making a retry explicit in the logs.

## Deletion Handling

### CR deletion during rotation

The CR is long-lived in normal operation, but deletion is allowed at any step (see [ValidateDelete](#validation-webhook)). Deleting the CR mid-cycle never breaks connectivity — the canonical current passwords keep working — but leaves residue. `*_PENDING` residue is harmless: the next cycle adopts it and rolls forward. `*_OLD` residue stops the next cycle in `StalePending` until it is cleaned up:

| Deleted during | Residue | Effect on the next rotation |
|---|---|---|
| `ApplyingRetain` (before any RETAIN ran) | `*_PENDING` keys, `ROTATION_ID` | None — the seed handler adopts the staged pending passwords (never-promoted random values, equivalent to fresh ones) |
| `ApplyingRetain` (partial RETAIN) | Above + `RETAIN_STARTED` + dual passwords on some instances | None — RETAIN resumes where the abandoned cycle stopped (the marker skips the pre-check; per-user `HasDualPassword` keeps it idempotent) |
| `Promoting` (before the atomic Secret update) | Same as the partial-RETAIN row, with dual passwords on all instances | None — same as above |
| `Promoting` (Secret already promoted) .. `ApplyingDiscard` | `*_OLD` keys, `ROTATION_ID`, dual passwords on all instances | Stops in `StalePending` — reset MySQL, then remove the keys ([Leftover Old Passwords](#leftover-old-passwords-abandoned-cycle-after-promotion)) |
| `Finalizing` | `*_OLD` keys, `ROTATION_ID` (no dual passwords) | Stops in `StalePending` — remove the keys |

The CR does **not** use a finalizer for automatic rollback: rollback requires connecting to every MySQL instance (which may not be possible during deletion), and a partial rollback is worse than no rollback. With the core invariant, skipping rollback costs nothing but residue.

### MySQLCluster deletion

The CR carries an ownerReference to its MySQLCluster, so garbage collection deletes the CR when the cluster is deleted. The ownerReference does not set `blockOwnerDeletion`: nothing about rotation needs to delay cluster termination, and CR deletion is always allowed. No special teardown is needed — the MySQL instances are being destroyed too.

Garbage collection is asynchronous. With the default background cascading deletion, the MySQLCluster object disappears first and the CR is collected shortly after. A new cluster with the same name can be created inside that window, which is why the stale-CR handling below exists.

### Stale CR handling (cluster recreated under the same name)

If a `MySQLCluster` is deleted and another is recreated under the same name before garbage collection reclaims the original CR, the leftover CR matches the new cluster by `namespace/name` but its ownerReference points at the old cluster's UID. Adopting that CR onto the new cluster would let leftover rotation state break the new cluster's credentials, so stale CRs are **invisible** to every component:

| Component | Behavior on a stale CR |
|---|---|
| `CredentialRotationReconciler` | Emit `StaleCredentialRotation` Warning Event and return without adopting |
| `ClusterManager.handlePasswordRotation` | Return early; do not run RETAIN / DISCARD |
| `MySQLClusterReconciler` | (does not read the CR at all) |
| `kubectl moco rotate-credential` / `discard-old-credential` | Refuse with an error instructing the user to delete the stale CR |

"Stale" means the CR has a MySQLCluster ownerReference, but its UID does **not** match the UID of the live cluster. A CR with no MySQLCluster ownerReference yet (just-created, not yet adopted) is treated as **fresh** by the Reconciler and the CLI; ClusterManager simply waits for adoption (see [ClusterManager](#clustermanager)).

## Security Considerations

- `RotateUserPassword`, `DiscardOldPassword`, and `MigrateUserAuthPlugin` interpolate user names directly into SQL (MySQL does not support placeholders in the user position of `ALTER USER`; password values do use bind placeholders). Every rotation operation validates the user name against the fixed constants in `pkg/constants/users.go` at runtime before building the statement.
- `MigrateUserAuthPlugin` interpolates the plugin name into `IDENTIFIED WITH`. The value is validated against `^[a-zA-Z0-9_]+$` and derived from `@@global.authentication_policy` on the primary, never from user input.
- All `ALTER USER` rotation calls run under `SET sql_log_bin=0` on a dedicated `db.Conn` to prevent cross-cluster propagation.
- During a cycle the controller Secret temporarily holds one extra password set (`*_PENDING` before promotion, `*_OLD` after). Both live in the same Secret as the current passwords, so no new RBAC permissions are needed.

## Recovery Procedures

All recovery procedures share one principle: **reset MySQL passwords to the current values in the controller Secret.** Thanks to the core invariant, the current values always authenticate, so recovery never needs to guess which password set is live. Note that `ALTER USER ... IDENTIFIED BY` (without RETAIN) only replaces the primary password — it does **not** remove a retained secondary password. For this reason, the reset scripts also run `ALTER USER ... DISCARD OLD PASSWORD` for every user. DISCARD removes the secondary password if one exists and does nothing otherwise, so MySQL returns to a clean single-password state. Without the DISCARD statements, the secondary passwords would stay, and the RETAIN pre-check (`DualPasswordExists`) would block the next rotation.

Recovery never requires restarting Pods: per-namespace Secrets only ever hold the canonical current values, so no Pod depends on a password that recovery would take away.

### How to Reset MySQL Passwords

Retrieve the current passwords from the controller Secret:

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

> On an intermediate-primary cluster (`spec.replicationSourceSecretName` set), the primary also runs with `super_read_only=1`, so this check finds no writable instance. Look up the primary with `kubectl get mysqlcluster <name> -o jsonpath='{.status.currentPrimaryIndex}'` instead, and use the replica snippet below (with the `super_read_only` toggle) on **every** instance, including the primary.

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
  ALTER USER 'moco-admin'@'%'        DISCARD OLD PASSWORD;
  ALTER USER 'moco-agent'@'%'        DISCARD OLD PASSWORD;
  ALTER USER 'moco-repl'@'%'         DISCARD OLD PASSWORD;
  ALTER USER 'moco-clone-donor'@'%'  DISCARD OLD PASSWORD;
  ALTER USER 'moco-exporter'@'%'     DISCARD OLD PASSWORD;
  ALTER USER 'moco-backup'@'%'       DISCARD OLD PASSWORD;
  ALTER USER 'moco-readonly'@'%'     DISCARD OLD PASSWORD;
  ALTER USER 'moco-writable'@'%'     DISCARD OLD PASSWORD;
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
  ALTER USER 'moco-admin'@'%'        DISCARD OLD PASSWORD;
  ALTER USER 'moco-agent'@'%'        DISCARD OLD PASSWORD;
  ALTER USER 'moco-repl'@'%'         DISCARD OLD PASSWORD;
  ALTER USER 'moco-clone-donor'@'%'  DISCARD OLD PASSWORD;
  ALTER USER 'moco-exporter'@'%'     DISCARD OLD PASSWORD;
  ALTER USER 'moco-backup'@'%'       DISCARD OLD PASSWORD;
  ALTER USER 'moco-readonly'@'%'     DISCARD OLD PASSWORD;
  ALTER USER 'moco-writable'@'%'     DISCARD OLD PASSWORD;
  SET GLOBAL super_read_only=ON;
"
```

> `sql_log_bin=0` must be set before disabling `super_read_only` to prevent intermediate writes from being logged. MOCO's clustering loop will re-enable `super_read_only` automatically if the manual re-enable fails.

### Stale Pending Passwords (Inconsistent Controller Secret)

**Symptom:** Warning Event `RotationPendingError`; the CR stops in `StalePending` (`RotationReady=False`, `reason=Stale`).

**Cause:** The rotation bookkeeping in the controller Secret is inconsistent: a partial `*_PENDING` group, a `ROTATION_ID` that does not match the CR's in-flight cycle, or a `ROTATION_ID` without a key group — typically because the Secret was edited by hand or restored from a backup. (A **complete** pending set left behind by a deleted CR is *not* this state: the next cycle adopts it and continues — see [CR deletion during rotation](#cr-deletion-during-rotation).)

```console
# 1. If RETAIN may have run (RETAIN_STARTED is present, or in doubt):
#    reset MySQL passwords on all instances (see "How to Reset MySQL
#    Passwords"). This clears any partial dual-password state.

# 2. Clean the controller Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_PENDING keys, *_OLD keys, ROTATION_ID, and RETAIN_STARTED.

# 3. Wait for the controller to observe the clean Secret. It aborts the
#    stuck cycle, emits a RotationRecovered Event, and returns the CR
#    to Idle.
$ kubectl -n <namespace> wait credentialrotation <cluster-name> \
    --for=condition=RotationReady

# 4. Retry rotation.
$ kubectl moco rotate-credential <cluster-name>
```

If `RETAIN_STARTED` is absent **and no `*_OLD` keys are present**, no `ALTER USER` was ever executed and step 1 can be skipped — the stale keys are the only residue. (Promotion removes the marker, so `*_OLD` keys mean RETAIN ran on every instance despite the missing marker.)

One variant recovers without any operator action: when the pending passwords were lost while the Secret is otherwise clean (`InconsistentState` Warning Event), `handleStalePending` finds a clean Secret on the next reconcile and returns the CR to Idle immediately. MySQL may still hold dual passwords in that case; the next cycle's pre-check reports them (`DualPasswordExists`), and [Dual Password Exists Outside the Current Cycle](#dual-password-exists-outside-the-current-cycle) covers the reset.

### Leftover Old Passwords (Abandoned Cycle After Promotion)

**Symptom:** `*_OLD` keys and `ROTATION_ID` remain in the controller Secret with no active cycle (the CR was deleted between promotion and completion). When the next rotation is requested, the seed handler emits a `RotationPendingError` Warning Event and the CR stops in `StalePending`.

**Impact:** None on the running cluster — the current passwords are canonical and authenticate everywhere. Instances may still hold the old password as a harmless secondary.

```console
# 1. Reset MySQL passwords on all instances (see "How to Reset MySQL
#    Passwords"). This clears the leftover secondary passwords.

# 2. Clean the controller Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_OLD keys and ROTATION_ID.

# 3. If the CR is already stuck in StalePending, wait for the controller
#    to observe the clean Secret and return the CR to Idle
#    (RotationRecovered Event).
$ kubectl -n <namespace> wait credentialrotation <cluster-name> \
    --for=condition=RotationReady

# 4. Retry rotation.
$ kubectl moco rotate-credential <cluster-name>
```

### Dual Password Exists Outside the Current Cycle

**Symptom:** Warning Event `DualPasswordExists` on the MySQLCluster, repeated on every ClusterManager tick while a rotation cycle waits in `ApplyingRetain`.

**Cause:** A system user already had `additional_password` set when the cycle's pre-check ran. Either a previous recovery didn't fully clear MySQL state, or someone ran `ALTER USER ... RETAIN CURRENT PASSWORD` manually. The cycle waits; MySQL has not been changed (the pending passwords are already staged in the controller Secret).

**Why DISCARD is unsafe here:** After a manual RETAIN, the primary password is the new (unknown) value and the secondary is the old (known) value. DISCARD would remove the secondary, leaving only the unknown primary — breaking connectivity.

**Recovery:** No CR deletion, Secret cleanup, or retry command needed.

```console
# 1. (recommended) Verify Pods can connect with current credentials.
# 2. Reset MySQL passwords on all instances (see "How to Reset MySQL Passwords").

# The waiting cycle proceeds by itself on a later ClusterManager tick,
# as soon as the pre-check passes.
```
