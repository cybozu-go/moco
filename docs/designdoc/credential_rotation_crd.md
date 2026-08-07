# System User Password Rotation with CredentialRotation CRD

## Background

MOCO manages eight MySQL users: `moco-admin`, `moco-agent`, `moco-repl`, `moco-clone-donor`, `moco-exporter`, `moco-backup`, `moco-readonly`, and `moco-writable`. This document calls all eight "system users" for short. (In the code, the first six are "system users" and the last two are "end-user accounts"; rotation covers all eight.) Their passwords are generated at cluster creation and stored in a controller-managed credential Secret in the system namespace — the **controller Secret**, named `mysql-<namespace>.<name>` by `ControllerSecretName()` — and distributed to per-namespace Secrets. Once generated, these passwords never change.

> The controller Secret is distinct from the *replication source Secret* (`spec.replicationSourceSecretName`), which holds donor connection info for an intermediate-primary cluster. This document only uses "controller Secret" for the credential Secret.

If a credential leak occurs, the only recovery option today is recreating the cluster. This design introduces an in-place rotation mechanism that avoids downtime, using a dedicated **CredentialRotation** CRD with its own controller. The new kind starts at `v1beta2` to match the rest of the API group. It has no older versions of its own, and giving it a different version from the group's existing one would only cause confusion.

## Why a Dedicated CRD?

Password rotation could be implemented inside `MySQLClusterReconciler`, but a dedicated CRD is a better fit, for four reasons:

1. **Blast radius** — A dedicated controller isolates rotation failures from StatefulSet, Service, and backup CronJob reconciliation.
2. **Status bloat** — `MySQLCluster.Status` already carries conditions, backup status, replica counts, and more; adding rotation state would make it harder to read.
3. **Testability** — `MySQLClusterReconciler` is already large; rotation logic is easier to test in isolation.
4. **Separation of concerns** — Rotation is an operator-initiated, infrequent operation with its own lifecycle.

KubeDB takes the same approach with `MySQLOpsRequest` (`type: RotateAuth`): a **single-use** CRD where one object represents one operation.

## Goals and Non-goals

**Goals:**
- Rotate all 8 system user passwords without MySQL downtime
- Isolate rotation processing in a dedicated CRD and controller
- Idempotent and crash-safe (controller restart resumes correctly)
- Prevent accidental propagation of ALTER USER to cross-cluster replicas
- Operator-initiated via `kubectl moco`
- Documented manual recovery for every failure mode

**Non-goals:**
- **GitOps-driven rotation.** The CR is a single-use operation object, and creating it is the trigger. It must not be managed by GitOps tools (see [Do Not Manage the CR with GitOps](#do-not-manage-the-cr-with-gitops)). Rotation is driven by `kubectl moco` or by automation that calls it.
- Automatic periodic rotation (build externally with a CronJob that runs `kubectl moco rotate-credential`; see [Automation](#automation))
- Per-user rotation (all 8 users rotate together)
- End-user credential management
- Rollback of a started rotation (the design is roll-forward only; see [Roll-forward Only](#roll-forward-only))
- **A deadline on the verification window.** After the rotate phase, the old (leaked) password keeps working until the operator requests the discard. The design puts no upper bound on this window — discard promptly, and alert on a long `AwaitingDiscard` (see [Metrics](#metrics)).

## Assumptions

- **RETAIN is all-or-nothing.** `DualPassword=True` (the promotion precondition) is only set after `ALTER USER ... RETAIN` has succeeded on **every** instance (see [All-or-nothing](#all-or-nothing)). The RETAIN loop must never skip an unreachable instance — the core invariant depends on this. If the loop skipped an instance, that instance would keep rejecting the canonical current password. Any future change to the RETAIN loop must preserve this property.
- No MOCO system user has a dual password when rotation starts. A pre-check validates this before the first `ALTER USER` (see [Pre-check and crash recovery](#pre-check-and-crash-recovery)).
- An instance added mid-cycle clones its data — including the `mysql.user` table and any dual-password state — from an existing instance, so it inherits the donor's password state.
- MySQL version is 8.0.14+ (dual password support).

## Overview

Rotation is a **two-phase process** — **rotate** then **discard** — using MySQL's dual-password feature (8.0.14+). The operator triggers each phase explicitly. Between the two phases there is a verification window where MySQL accepts both the old and the new password.

The whole design rests on a single invariant:

> **The controller Secret's current passwords always authenticate on every MySQL instance.**

New passwords are staged as `*_PENDING` keys, applied to every instance with `ALTER USER ... RETAIN CURRENT PASSWORD`, and **promoted to current immediately after RETAIN succeeds on all instances** — at that moment every instance accepts both old and new, so making the new password canonical is safe. Distribution to per-namespace Secrets then happens through the normal `MySQLCluster` reconcile path, which always distributes current passwords. See [The Core Invariant](#the-core-invariant) for why this ordering removes whole classes of failure modes.

The CredentialRotation CR is **single-use**: one object represents one rotation. Creating it starts the rotation; setting `spec.discard: true` starts the discard; after the cycle completes, the controller deletes the object itself (after a TTL). A failed rotation stays as a `Failed` object until the operator removes it.

```
  kubectl moco rotate-credential  =  create the CR
       │
       ▼
  ┌── Rotate ────────────────────────────────────────────────────────────────┐
  │  ApplyingRetain                                                          │
  │    │ Reconciler: seed pending passwords + status.rotationID;             │
  │    │            emit RotationStarted                                     │
  │    │ ClusterManager: ALTER USER ... RETAIN on every instance             │
  │    ▼                                                                     │
  │  Promoting (DualPassword=True)                                           │
  │    │ Reconciler: promote pending → current in the controller Secret      │
  │    │            (one atomic Secret update; old values move to *_OLD)     │
  │    ▼                                                                     │
  │  AwaitingRollout                                                         │
  │    │ MySQLClusterReconciler: distribute current passwords (normal path)  │
  │    │ Reconciler: wait for distribution to catch up, add restart          │
  │    │            annotation, watch StatefulSet rollout; once settled,     │
  │    │            DiscardReady→True (verification window opens)            │
  │    ▼                                                                     │
  │  AwaitingDiscard (DiscardReady=True)                                     │
  └──────────────────────────────────────────────────────────────────────────┘
       │
       │  Operator verifies apps work with new passwords
       │  kubectl moco discard-old-credential  =  set spec.discard: true
       ▼
  ┌── Discard ───────────────────────────────────────────────────────────────┐
  │  ApplyingDiscard (DiscardReady→False/Pending; emit DiscardStarted)       │
  │    │ ClusterManager: DISCARD OLD PASSWORD + auth plugin migration        │
  │    ▼                                                                     │
  │  Finalizing (DualPassword=False)                                         │
  │    │ Reconciler: delete *_OLD, ROTATION_ID, RETAIN_STARTED keys;         │
  │    │            emit RotationCompleted                                   │
  │    ▼                                                                     │
  │  Succeeded ──(TTL)──▶ the controller deletes the CR                      │
  └──────────────────────────────────────────────────────────────────────────┘

  No running instances mid-cycle    ──▶ Blocked (non-terminal detour;
  (0 replicas or spec.offline)          resumes when the cluster runs again)
  Any unrecoverable inconsistency   ──▶ Failed (the CR stays; the operator
                                        deletes it after following the
                                        recovery procedure)
```

> Notation: `DiscardReady→False/Pending` is short for setting the condition to `status: False` with `reason: Pending`. A "ClusterManager tick" is one pass of the per-cluster clustering loop, which runs every few seconds.

State is exposed in two places:

- **`status.phase`** — where the workflow is. It moves forward and ends in `Succeeded` or `Failed`, with `Blocked` as a possible non-terminal detour. This is the field to look at for `kubectl get`, dashboards, and alerts.
- **Conditions** — independent observations that other components and scripts consume:
  - **`DiscardReady`** — `True` while the verification window is open and the operator may set `spec.discard: true`.
  - **`DualPassword`** — `True` while MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD).
  - **`Finished`** — `True` once the phase is terminal, with reason `Succeeded` or `Failed`. This is the machine-readable terminal signal for `kubectl wait` and health tooling.

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
4. **Non-destructive CR deletion.** Deleting the CR at any phase never breaks connectivity; it can only leave residue (dual passwords in MySQL, stale keys in the controller Secret). The next CR adopts `*_PENDING` residue and fails on `*_OLD` residue — see [CR deletion during rotation](#cr-deletion-during-rotation).

One caution about consequence 2: components that read passwords **into memory** (moco-agent receives them as environment variables from the user Secret; mysqld-exporter reads the `my.cnf` Secret at start) only pick up new values when their Pod restarts. The rotate phase handles this with a rolling restart before the verification window opens. The [Recovery Procedures](#recovery-procedures) must respect the same rule: never remove a password from MySQL while Pods that hold it in memory are still running.

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

The natural-looking alternative is to keep the old password canonical until the very end of the cycle and promote the new one last. That ordering forces every component to answer "current or pending?" per phase: the distribution path needs a phase-dependent branch, DISCARD has to connect with the pending password, and crash recovery needs extra markers to detect a half-finished promotion. Worse, it creates a failure mode with no recovery: if the `*_PENDING` keys are lost while Pods are already using them, the new passwords are gone for good (MySQL stores only hashes).

Promoting right after RETAIN avoids all of that. The trade-offs accepted in exchange:

- **Roll-forward only** (see below).
- The old password must be archived in `*_OLD` keys until the cycle completes, for recovery purposes.
- The all-or-nothing RETAIN assumption becomes essential (see [The Core Invariant](#the-core-invariant)).

### Roll-forward Only

Once promotion happens, the new password is canonical and the designed path only moves forward (distribute → rollout → verify → discard). There is no designed rollback. If the operator finds a problem during the verification window, the options are:

- Complete the cycle (discard) and immediately start another rotation, or
- Follow the manual reset procedure (see [Recovery Procedures](#recovery-procedures)) — possible at any time before DISCARD because the old password still authenticates as the secondary, and its value is preserved in the `*_OLD` keys.

This matches the nature of credential rotation: the old credential must be treated as leaked, so returning to it is rarely the right response.

### Why a Single-use CR?

A rotation is an **operational action**, like `kubectl rollout restart` or a manual failover — not a piece of desired state that a cluster should converge to. The CR models it as such, following established practice for one-shot operations (KubeDB `MySQLOpsRequest`, Velero `Backup`):

- **Creating the CR is the trigger.** The spec carries no counters or timestamps — only the `discard` flag.
- **One object = one cycle.** The workflow moves forward through `status.phase` and ends in `Succeeded` or `Failed`.
- **The controller cleans up after success.** A `Succeeded` CR is deleted automatically after a TTL, so the steady state has no CR at all.
- **A `Failed` CR stays and blocks the next attempt.** Because the CR name must match the cluster name, the next `create` fails with `AlreadyExists` until the operator acknowledges the failure, follows the recovery procedure, and deletes the object. This turns "you must recover before rotating again" from a documented rule into a structural guarantee.

The consequence of "creation is the trigger" is that the CR must never be managed by a tool that recreates deleted objects — see [Do Not Manage the CR with GitOps](#do-not-manage-the-cr-with-gitops).

### Why `status.phase`?

The Kubernetes API conventions discourage `phase` fields on long-lived resources, because conditions are independent observations while a phase turns the status into a state machine. A **single-use workflow object is exactly a state machine**, though: it moves forward through a fixed sequence and terminates. For this class of resource, a phase field is established practice (`MySQLOpsRequest.status.phase`, Velero `Backup.status.phase`), and it is what makes the object easy to operate: one printer column, one metric label, one alert rule ("phase not terminal for more than N minutes").

The division of labor is:

- **`status.phase`** holds the workflow position. It is written together with the conditions in the same status update, so the two never disagree in a single snapshot.
- **Conditions hold independent observations**: `DualPassword` (MySQL's physical state), `DiscardReady` (the action-availability gate that the webhook and `kubectl wait` consume), and `Finished` (the terminal signal).

Clients must treat the phase value set as **open**: new values may be added in future versions, and unknown values must be handled gracefully (per the API conventions' guidance for consumers of enums).

### Responsibility Split: Reconciler vs ClusterManager

The **CredentialRotationReconciler** handles K8s resource operations: phase/condition transitions, controller Secret management (seed / promote / cleanup), StatefulSet rolling-restart annotation, distribution catch-up wait, StatefulSet rollout wait, TTL deletion of the completed CR, and resuming from `Blocked`.

The **ClusterManager** handles DB operations: dual-password pre-checks, `ALTER USER RETAIN`, `DISCARD OLD PASSWORD`, auth plugin migration (with a temporary `super_read_only` toggle on the instances that run with it). It also writes the state that belongs to these DB operations: the `RETAIN_STARTED` marker in the controller Secret, the `DualPassword` condition, and the phase transitions out of its two phases (including `Blocked` and `Failed` when its pre-checks stop the flow).

The **MySQLClusterReconciler** distributes the controller Secret's current passwords to per-namespace Secrets — its normal job, unchanged by rotation.

Each transition has one *driver* that does the work and then writes the change — the new phase together with any condition changes, in one status update:

| Transition | Driver | What the driver writes |
|---|---|---|
| (creation) → `ApplyingRetain` | Reconciler (seed) | phase, `status.rotationID`, initial conditions |
| `ApplyingRetain` → `Promoting` | ClusterManager | phase, `DualPassword=True` |
| `Promoting` → `AwaitingRollout` | Reconciler | phase |
| `AwaitingRollout` → `AwaitingDiscard` | Reconciler¹ | phase, `DiscardReady=True` |
| `AwaitingDiscard` → `ApplyingDiscard` | Reconciler (after the operator sets `spec.discard: true`) | phase, `DiscardReady=False` (`reason=Pending`) |
| `ApplyingDiscard` → `Finalizing` | ClusterManager | phase, `DualPassword=False` |
| `Finalizing` → `Succeeded` | Reconciler | phase, `status.completionTime`, `Finished=True` |
| any → `Blocked` | whichever component hits the obstacle | phase, `status.message` (and `DiscardReady=False` (`reason=Blocked`) on the discard side) |
| `Blocked` → (resume) | Reconciler | see [Resuming from Blocked](#resuming-from-blocked) |
| any → `Failed` | whichever component detects the inconsistency | phase, `status.message`, `status.completionTime`, `Finished=True` (`reason=Failed`) |
| `Succeeded` → (deleted) | Reconciler | deletes the CR after the TTL |

¹ The Secret distribution itself is done by MySQLClusterReconciler; the Reconciler only waits for it and performs the transition.

Because the phase and the conditions are written atomically, ordering guarantees come directly from the state machine itself. For example, `DiscardReady=False` is recorded in the same update that sets phase `ApplyingDiscard`, and the `DiscardStarted` Event is emitted right after it — so ClusterManager, which acts on the phase, can never run DISCARD SQL before the request has been recorded. (Events are separate API writes and can be lost in a crash between the two; the phase and the conditions are the authoritative record.)

## CRD Definition

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata:
  name: my-cluster            # must match the target MySQLCluster name
  namespace: my-namespace     # same namespace as the MySQLCluster
spec:
  discard: false              # set to true to start the discard phase
status:                       # shown here during the verification window
  observedGeneration: 1
  phase: AwaitingDiscard
  rotationID: 8f7c9a34-...    # UUID of this cycle
  conditions:                 # illustrative; real entries also carry reason,
    - type: DiscardReady      #   message, lastTransitionTime, and
      status: "True"          #   observedGeneration
    - type: DualPassword
      status: "True"
    - type: Finished
      status: "False"
```

### Naming Convention

The CR name **must match** the target MySQLCluster name (same name, same namespace). One name gives three guarantees at once:

- **At most one rotation per cluster**, enforced by the API server itself (two objects cannot share a name). No List-based admission check, no race.
- **O(1) lookup**: the Reconciler, ClusterManager, and CLI find the CR by the cluster name, without a reference field or a label selector.
- **Fixed names in runbooks**: `kubectl wait credentialrotation <cluster-name> ...` works without discovering a generated name.

Because the CR is single-use, "at most one" also covers failure handling: a `Failed` CR keeps its name occupied, so a new rotation cannot start until the operator deletes it (see [Failure Handling](#failure-handling)).

### OwnerReference

CredentialRotation sets an ownerReference to the target MySQLCluster so that Kubernetes garbage-collects it on cluster deletion. The Reconciler adds the reference at adoption — when it first picks up the CR — and **before it writes any status** (see the seed handler). The ownerReference does not set `blockOwnerDeletion`, and the CR carries no finalizer.

### Spec / Status Fields

| Field | Type | Notes |
|---|---|---|
| `spec.discard` | bool | Defaults to `false`. Must be `false` at create time. The only allowed update is `false` → `true`, and only while `DiscardReady=True`. It can never be set back to `false`. |
| `status.observedGeneration` | int64 | Standard `metadata.generation` echo. |
| `status.phase` | string | Workflow position. See [Phase](#phase). |
| `status.message` | string | Human-readable detail for the current phase. On `Failed` it explains what went wrong and **names the matching recovery procedure**; on `Blocked` or a pause it names the obstacle; on `Succeeded` it shows the scheduled TTL deletion time. |
| `status.rotationID` | string | UUID for this cycle. Set when the pending passwords are seeded. |
| `status.completionTime` | metav1.Time | Set when the phase turns terminal. The TTL deadline is computed from it. |
| `status.conditions` | `[]metav1.Condition` | See [Conditions](#conditions). |

`spec.discard` is a bool rather than a `stage`-style enum on purpose: the spec has exactly one legal transition, and a two-value one-way flag cannot express an invalid request, while an enum would allow invalid values. The webhook admits the flip only while `DiscardReady=True`; this reads status during admission and is therefore best-effort against concurrent status writes, which is fine — the controllers re-check the actual state at execution time, so a stale admission decision can only delay the discard, never corrupt it.

The CRD enables the `status` subresource, so `metadata.generation` increments only on spec changes. Every status writer (Reconciler and ClusterManager) stamps `status.observedGeneration` and per-condition `observedGeneration` with the generation of the object it read, so kstatus-style tooling can tell whether the status reflects the latest spec.

### Phase

`status.phase` moves forward through the following values, with `Blocked` as a possible non-terminal detour. The set is **open**: clients must tolerate unknown values.

| Phase | Meaning | Terminal |
|---|---|---|
| `ApplyingRetain` | Pending passwords are seeded; ClusterManager is applying `RETAIN` on every instance. | no |
| `Promoting` | RETAIN succeeded everywhere; the Reconciler promotes pending → current in the controller Secret. | no |
| `AwaitingRollout` | Waiting for distribution to catch up and the StatefulSet rolling restart to settle. | no |
| `AwaitingDiscard` | Verification window. The operator may set `spec.discard: true`. | no |
| `ApplyingDiscard` | ClusterManager is running `DISCARD OLD PASSWORD` and the auth plugin migration. | no |
| `Finalizing` | The Reconciler removes the rotation bookkeeping keys from the controller Secret. | no |
| `Blocked` | The cluster stopped running mysqld instances mid-cycle (0 replicas or `spec.offline: true`). Resumes automatically when the cluster runs again; `status.message` records where it stopped. (Pauses caused by the stop annotations keep their current phase instead — see [Stopped Clustering or Reconciliation](#stopped-clustering-or-reconciliation).) | no |
| `Succeeded` | The full cycle completed. The controller deletes the CR after the TTL. | yes |
| `Failed` | An unrecoverable inconsistency was detected (see [Failure Handling](#failure-handling)). The CR stays until the operator deletes it. | yes |

The phase is persisted, not derived: whichever component performs a transition writes the new phase **in the same status update** as the conditions it changes, so a single read never sees the two disagree. The two writers protect their writes in different ways, matched to how long they hold a stale read. ClusterManager does long DB work between reading the CR and writing it, so it uses `retry.RetryOnConflict` with a fresh `Get`; after every fresh `Get` (including the first), it re-verifies the CR's **UID** and the transition's precondition, and aborts if either no longer holds — a retry must never apply a transition to a CR that was recreated under the same name, or to a phase that has already changed. The Reconciler writes with the plain optimistic concurrency of the object it read: a conflict fails the reconcile, and the automatic retry starts over from a fresh read, which re-establishes the same guarantees.

### Conditions

Three conditions carry the observations that other parties consume:

| Type | When `True` | When `False` |
|---|---|---|
| `DiscardReady` | Verification window is open (phase `AwaitingDiscard`): rotation done, rollout settled, dual password held. The operator may set `spec.discard: true`. | Any other point in the cycle. |
| `DualPassword` | MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD). | No dual-password state in MySQL. |
| `Finished` | The phase is terminal; the reason tells which way (`Succeeded` / `Failed`). | The cycle is still running. |

Reason vocabulary (each reason keeps a single meaning, per the API conventions):

| Reason | Appears on | With status | Meaning |
|---|---|---|---|
| `RolloutSettled` | `DiscardReady` | `True` | The verification window is open. |
| `Pending` | `DiscardReady` | `False` | Not in the verification window (before it opens, or the discard is running). Also carried through a rotate-side `Blocked`. |
| `Blocked` | `DiscardReady` | `False` | The discard cannot progress (0 replicas or offline after `spec.discard` was set). |
| `Retained` | `DualPassword` | `True` | MySQL holds a dual-password set on all system users. |
| `NotRetained` | `DualPassword` | `False` | MySQL is not currently holding a dual-password set. |
| `Succeeded` / `Failed` | `Finished` | `True` | Which terminal state was reached. |
| `Running` | `Finished` | `False` | The cycle is in progress. |

**Using `kubectl wait`.**

```console
# wait for the verification window
$ kubectl -n <ns> wait credentialrotation <cluster> --for=condition=DiscardReady

# wait for completion — terminates on BOTH outcomes, then check which:
$ kubectl -n <ns> wait credentialrotation <cluster> --for=condition=Finished --timeout=30m
$ kubectl -n <ns> get credentialrotation <cluster> -o jsonpath='{.status.phase}'
```

> **Events.** In addition to the status, the controllers emit Kubernetes Events for `kubectl describe` visibility.
>
> - On the CredentialRotation: `RotationStarted`, `PasswordsPromoted`, `AwaitingDiscard` (marks the moment the phase of the same name is entered), `DiscardStarted`, `RotationCompleted`; Warnings: `RotationBlocked`, `DiscardBlocked`, `RotationPaused`, `RotationFailed`, `StaleCredentialRotation`.
> - On the MySQLCluster (emitted by ClusterManager): `RetainApplied`, `DiscardApplied`; Warnings: `RotationBlocked`, `DiscardBlocked`, `DualPasswordExists`, `PasswordRotationError`.
>
> The two `Blocked` reasons appear on both objects: the Reconciler emits them on the CR when it blocks at seed or discard-start, ClusterManager on the MySQLCluster when its pre-checks block. `RotationFailed` carries the same detail as `status.message`. When diagnosing a stall, **describe both objects** — DB-side trouble (`DualPasswordExists`, unreachable instances) is reported on the MySQLCluster and mirrored, with less detail, into the CR's `status.message`.

## Validation Webhook

### ValidateCreate

All of the following must be true.

- The target MySQLCluster (same name, same namespace) must exist, must not be terminating (`metadata.deletionTimestamp` unset), must have `spec.replicas > 0`, and must not be offline (`spec.offline` scales the StatefulSet down to zero Pods, so the rotation would have no mysqld instance to reach).
- `spec.discard` must be `false`. The discard must be requested via update after the verification window opens; `true` at create time would skip the window.

Note what create-time validation does **not** need to check. The "at most one rotation at a time" rule is enforced by the API server through the name constraint. Leftover state in the controller Secret is checked at runtime by the seed handler, which fails the CR with a clear message when `*_OLD` residue exists (see [Failure Handling](#failure-handling)). The seed handler also re-validates `spec.discard == false` at its first reconcile, so a CR created while the webhook was unavailable cannot silently skip the verification window — it goes `Failed` instead.

### ValidateUpdate

The spec is immutable except for one transition:

- `spec.discard` may change from `false` to `true`, but only while `DiscardReady=True` (the verification window is open, so the post-promotion rollout has settled) and while the live MySQLCluster runs mysqld instances (`spec.replicas > 0` and not `spec.offline`). The flag is one-way, so admitting it while nothing runs would execute the discard automatically when the cluster comes back, without another operator confirmation. (The replicas half is defense in depth: a 0-replica cluster cannot be created through the normal API.)
- `spec.discard` can never change from `true` to `false`.
- No other spec change is allowed.
- The update is rejected when the CR is **stale** (see [Stale CR handling](#stale-cr-handling-cluster-recreated-under-the-same-name)): a discard request against a leftover CR from a deleted cluster would otherwise be admitted and then silently ignored by the controllers.

### ValidateDelete

Deletion is **always allowed** — there is no delete webhook.

The core invariant makes CR deletion non-destructive: the per-namespace Secrets always hold the canonical current passwords, which authenticate on every instance regardless of when the CR disappears. Deleting the CR mid-cycle can only leave **residue** — leftover keys and dual passwords:

- dual passwords (a harmless secondary slot) on some or all instances,
- stale `*_PENDING` / `*_OLD` / `ROTATION_ID` / `RETAIN_STARTED` keys in the controller Secret, and
- the restart annotation on the StatefulSet pod template (harmless; the next cycle overwrites its value).

None of these affect a running cluster. `*_PENDING` residue does not even block the next cycle: the next CR's seed handler reuses the leftover `ROTATION_ID` and pending passwords — they are random values that were never promoted, so adopting them is equivalent to generating fresh ones — and RETAIN resumes where the abandoned cycle stopped. `*_OLD` residue **does** block the next cycle: the next CR goes `Failed` at seed time, until the residue is cleaned up with the [Recovery Procedures](#recovery-procedures). Garbage collection is never blocked by a webhook.

## User Interface

| Command | Behavior |
|---|---|
| `kubectl moco rotate-credential <cluster>` | Create the CR (`spec.discard: false`). If a CR already occupies the name, refuse with a message that explains its state and what to do (see the list below). |
| `kubectl moco discard-old-credential <cluster>` | Refuse if stale; require `DiscardReady=True`; set `spec.discard: true` with an `Update` so a concurrent modification fails with a Conflict instead of being lost. A repeated call after the flip is a no-op. |
| `kubectl moco credential <cluster>` | Read the per-namespace user Secret (unchanged from previous releases). |

Both mutating commands also check, at the time they run, whether the cluster can make progress. They refuse when `spec.offline` is `true`, when the `moco.cybozu.com/clustering-stopped` annotation is set to `true`, or when the MySQLCluster is not `Healthy`. In addition:

- The refusal message for an occupied name describes one of four states:
  - a cycle in flight — wait for it, or delete the CR to abandon it,
  - `Succeeded`, waiting for its TTL deletion — delete it to rotate again immediately,
  - `Failed` — follow the recovery procedure in its `status.message`, then delete it,
  - stale — delete the leftover CR from the previous cluster.
- `rotate-credential` refuses when `moco.cybozu.com/reconciliation-stopped=true`, because the rotate phase depends on MySQLClusterReconciler distributing the promoted passwords. It also refuses when `spec.replicas` is 0 — the webhook would reject the create anyway, but the CLI fails faster with a clearer message.
- `discard-old-credential` needs neither extra check, because distribution finished before the verification window opened.

The webhook and the controller remain the authority; these checks only fail fast with a clear message.

`kubectl get credentialrotation` prints `PHASE`, `DISCARD` (the spec flag), and `AGE`; `-o wide` adds `MESSAGE` (priority-1 column) — the main diagnostic on `Failed` and `Blocked`.

### Do Not Manage the CR with GitOps

The CredentialRotation CR is an **operation**, not desired state. Creating it runs a rotation, and the controller deletes it when the rotation succeeds. A GitOps tool that holds the manifest would immediately recreate the deleted object — and every recreation is a new, unrequested password rotation.

Do not commit CredentialRotation manifests to a GitOps-managed repository, and exclude the resource from sync if the tool discovers it. Drive rotation with `kubectl moco` (directly or from automation such as a CronJob).

### Automation

Unattended automation (e.g. a periodic CronJob) should be written against these rules:

- Always pass `--timeout` to `kubectl wait` and re-check `status.phase` afterwards. `--for=condition=Finished` terminates on both outcomes, but if the cycle is `Blocked` or paused by a stop annotation, `kubectl wait` hangs until the timeout.
- Treat every non-zero exit of `rotate-credential` as a signal to alert, **not** to retry blindly: the refusal message distinguishes "previous rotation still in its TTL window" (retry later or delete the `Succeeded` CR) from "previous rotation `Failed`" (a human must run the recovery procedure) from "cluster not Healthy" (transient; retry later).

```console
# sketch of an unattended cycle
kubectl moco rotate-credential "$CLUSTER" -n "$NS" || exit 1   # alert on failure
kubectl -n "$NS" wait credentialrotation "$CLUSTER" --for=condition=DiscardReady --timeout=30m
# ... automated verification of the application ...
kubectl moco discard-old-credential "$CLUSTER" -n "$NS"
kubectl -n "$NS" wait credentialrotation "$CLUSTER" --for=condition=Finished --timeout=30m
test "$(kubectl -n "$NS" get credentialrotation "$CLUSTER" -o jsonpath='{.status.phase}')" = Succeeded
```

## Rotate Phase

### Reconciler: seed → `ApplyingRetain`

Runs when a CR has no phase yet, and again when the phase is `Blocked` with `spec.discard: false` (the resume path — every step below is idempotent). The handler no-ops when the MySQLCluster is terminating (`deletionTimestamp` set); the CR will be garbage-collected with it.

Before anything else — **including every status write below** — the handler adopts the CR: it adds the MySQLCluster ownerReference in a metadata update. Adoption-before-status is what makes the stale rule reliable: because status is only ever written after the ownerReference exists, a CR with a non-empty status and no ownerReference can only be an orphan (see [Stale CR handling](#stale-cr-handling-cluster-recreated-under-the-same-name)).

| # | Action | Persistence |
|---|---|---|
| 1 | If `spec.discard` is already `true` (only possible when the create webhook was bypassed): set phase `Failed` and emit `RotationFailed` — the verification window cannot be skipped. | Status.Update |
| 2 | If the cluster runs no mysqld instances (`spec.replicas <= 0` or `spec.offline: true` — changed between admission and reconcile): set phase `Blocked` with a message, seed the same initial conditions as step 5, and emit a `RotationBlocked` Warning Event on the CR; retry when the cluster runs again. Nothing has been mutated. | Status.Update |
| 3 | Take the `ROTATION_ID` stored in the controller Secret, or generate a new UUID when there is none. Reusing the stored ID makes this step idempotent across crashes, and also adopts the residue of an abandoned cycle (see ValidateDelete). The controller Secret is **updated, never created** by rotation code: if it does not exist, the handler waits — a Secret containing only bookkeeping keys must never come into existence. | — |
| 4 | Write 8 `*_PENDING` keys and `ROTATION_ID` into the controller Secret. A complete pending set that already matches the `ROTATION_ID` is kept as is. If the Secret is inconsistent or still holds `*_OLD` keys, set phase `Failed` with a message naming the recovery procedure and emit a `RotationFailed` Warning Event — that branch writes only the status, not the Secret. | Secret.Update |
| 5 | Set `status.rotationID`, phase `ApplyingRetain`, `DiscardReady=False` (`reason=Pending`), `DualPassword=False` (`reason=NotRetained`), `Finished=False` (`reason=Running`). Emit `RotationStarted` Event. | Status.Update |

### ClusterManager: `ApplyingRetain` → `Promoting`

Triggered on a ClusterManager tick when the phase is `ApplyingRetain`. Pre-checks, in order:

1. The controller Secret must hold this cycle's `*_PENDING` keys. The seed handler stages them before it sets `ApplyingRetain`, so a **clean** Secret (read uncached) means the staged passwords were lost — after ruling out a stale cached CR with an uncached re-read, set phase `Failed` with a message and emit `RotationFailed` on the CR. **Already promoted for this rotationID** → the Secret and the phase contradict each other (manual tampering): `Failed` in the same way.
2. If the cluster runs no mysqld instances (0 replicas or offline): emit a `RotationBlocked` Warning Event on the MySQLCluster and set phase `Blocked` — see [Clusters Without Running Instances](#clusters-without-running-instances-replicas0-or-offline).

| # | Action | Persistence |
|---|---|---|
| 1 | Pre-check: every instance is scanned for pre-existing dual passwords. Steps 1 and 2 are both skipped when the `RETAIN_STARTED` marker already holds this cycle's rotationID (crash recovery). | — |
| 2 | Set `RETAIN_STARTED` marker (rotationID) in the controller Secret. | Secret.Update |
| 3 | For each instance, connect with the current password. If the instance is a replica, or the intermediate primary when `spec.replicationSourceSecretName` is set, temporarily disable `super_read_only` (these instances run with it enabled) and re-enable it after the updates. Run `ALTER USER ... RETAIN CURRENT PASSWORD` for each user. A user that already has a dual password is **verified before it is skipped**: the handler confirms that the pending password authenticates for that user (a short per-user connection). If it does, this cycle's RETAIN already ran (crash retry) and the user is skipped; if it does not, the dual password came from outside the cycle — re-running RETAIN would drop the still-needed current password from the secondary slot, so the handler reports the error and names the recovery procedure instead. After each pass, the live replica count is re-read; instances added by a scale-up meanwhile are retained in another pass (bounded per tick), so the transition to `Promoting` is never written while an instance misses the pending passwords. | MySQL |
| 4 | Set `DualPassword=True` (`reason=Retained`) and phase `Promoting` in one status update. Emit `RetainApplied` Event on the MySQLCluster. | Status.Update |

An unreachable instance aborts the loop for this tick; the flow retries on later ticks with no retry limit, and the failure is reported as a `PasswordRotationError` Warning Event on the MySQLCluster and mirrored into the CR's `status.message`.

#### Pre-check and crash recovery

If any instance already has a dual password from outside this rotation cycle, the handler emits a `DualPasswordExists` Warning Event and waits (the CR's `status.message` mirrors the reason for the wait). After the pre-check succeeds, the handler stores the marker before running RETAIN. If the controller crashes and restarts, the marker — when it holds this cycle's rotationID — tells the handler to skip the pre-check and resume RETAIN. The per-user gate in step 3 makes retries safe: a dual password is treated as "this cycle's RETAIN already ran" only after verifying that the pending password authenticates, so a dual password planted outside the cycle during the crash window cannot slip through to promotion.

#### All-or-nothing

Step 3 aborts on the first instance that cannot be reached or fails `ALTER USER`. `DualPassword=True` is only written after **every** instance holds the dual-password set. This is the precondition that makes the next step (promotion) safe — see [The Core Invariant](#the-core-invariant).

### Reconciler: `Promoting` → `AwaitingRollout`

At this point every instance accepts both the old and the new password, so the new password can safely become canonical.

| # | Action | Persistence |
|---|---|---|
| 1 | Validate the controller Secret with `RotationState`: all 8 `*_PENDING` keys present with matching `ROTATION_ID` → proceed. If instead all 8 `*_OLD` keys are present with matching `ROTATION_ID` and no `*_PENDING` keys remain, promotion already happened (crash recovery) — skip to step 3. Any other state (a clean Secret, partial key groups, `ROTATION_ID` mismatch) means the staged state was lost or tampered with: set phase `Failed` with a message and emit `RotationFailed`. | — |
| 2 | **One atomic Secret update**: copy current values to `*_OLD` keys, copy `*_PENDING` values to current keys, delete the `*_PENDING` keys and `RETAIN_STARTED`. `ROTATION_ID` is kept until the cycle completes. | Secret.Update |
| 3 | Set phase `AwaitingRollout`; emit `PasswordsPromoted` Event. `DiscardReady` stays `False` (`reason=Pending`) until the StatefulSet rollout is complete. | Status.Update |

The presence of the `*_OLD` key group also serves as the "promotion done" marker: no extra revision annotation is needed to distinguish "promoted, status not yet updated" from "not yet promoted" after a crash.

From this moment "current" means the new password instead of the old one. Both passwords still work on every instance because of the dual-password window.

### `AwaitingRollout`: distribution and rollout

Distribution itself is **not** performed by the CredentialRotationReconciler. `MySQLClusterReconciler.reconcileV1Secret` distributes the controller Secret's current passwords — its normal behavior — and a watch on the controller Secret triggers it promptly after promotion (see [MySQLClusterReconciler](#mysqlclusterreconciler)).

The CredentialRotationReconciler gates progress:

| # | Action | Persistence |
|---|---|---|
| 1 | Wait until the per-namespace user Secret and `my.cnf` Secret are derived from the controller Secret's **current** passwords (content comparison against the distributed Secrets). If not yet, requeue — MySQLClusterReconciler will catch up. | — |
| 2 | If the cluster runs no mysqld instances (`spec.replicas <= 0` or `spec.offline: true`): hold this phase and requeue, recording the pause in `status.message` and emitting a `RotationPaused` Event. `spec.offline` scales the StatefulSet down to zero Pods, and a 0-replica StatefulSet passes every rollout check in step 4 trivially — without this hold, the verification window would open even though no Pod has restarted with the new passwords. (The phase stays `AwaitingRollout` rather than going `Blocked`: the `Blocked` resume path with `spec.discard: false` re-runs the seed, which treats the promoted controller Secret as a failure.) | Status.Update (message only) |
| 3 | Add `moco.cybozu.com/password-rotation-restart: <rotationID>` to the StatefulSet pod template with a **merge patch** under field manager `moco-credential-rotation`. A patch — unlike a server-side apply, which is an upsert — fails with NotFound when the StatefulSet was deleted between the cached read and the write, so this step can never create a minimal StatefulSet that would poison the real one with immutable-field conflicts. Skip the patch when the pod template already carries **this rotation's** annotation value. | StatefulSet.Patch |
| 4 | Check whether the StatefulSet rollout is complete — only against a pod template that carries this rotation's annotation, never against a stale pre-annotation object. Confirm that `status.observedGeneration` has caught up with `metadata.generation`, `status.currentRevision` matches `status.updateRevision`, and `status.replicas`, `status.updatedReplicas`, and `status.readyReplicas` all equal the desired `spec.replicas`. If any check is not satisfied, requeue and check again later. | — |
| 5 | After all rollout checks pass, set phase `AwaitingDiscard` and `DiscardReady=True` (`reason=RolloutSettled`). This records that the new password has been distributed and all Pods are ready, so the verification window is open. Emit an `AwaitingDiscard` Event. | Status.Update |

The skip condition in step 3 compares the annotation **value**, not just its presence: a template that still carries a *previous* rotation's rotationID is re-applied, and that re-apply is what triggers this cycle's rolling restart. The skip itself is required for another reason. Every StatefulSet update — even one that changes nothing — passes the `StatefulSetDefaulter` mutating webhook, which resets `spec.updateStrategy.rollingUpdate.partition` to `replicas` to guard rollouts. If the annotation were re-applied on every reconcile, the partition would be reset again and again while the partition controller tries to lower it, and the rollout would never complete.

#### Why the annotation waits for distribution (step 1 before step 3)

Connectivity is never at risk. A Pod that restarts early reads the old password from the namespace Secret, which has not been updated yet, and the old password still works during the dual-password window. The ordering protects the **discard gate**: if the annotation triggered a rollout while the namespace Secret still held old values, the rollout could settle with Pods running on the old password, `DiscardReady` would flip to `True`, and a subsequent DISCARD would remove the very password those Pods use.

#### Why the reconciler waits for the rollout

The verification window is safe only after every Pod has restarted and its MySQL clients have picked up the new password from the distributed Secrets. For example, `moco-agent` reads credentials from the per-namespace user Secret, and `mysqld-exporter` reads them from the `my.cnf` Secret — both only at Pod start. If the controller opened the window before the rollout finished, `kubectl wait --for=condition=DiscardReady` could return while some Pods' clients still used the old password. An automation script could then start the discard step. `DISCARD OLD PASSWORD` would remove the secondary password that those clients still need, and they could lose access to MySQL.

The rollout is a Kubernetes operation, so the Reconciler checks the StatefulSet status and opens the verification window. The ClusterManager performs the MySQL `DISCARD` operation only after this check has succeeded.

## Discard Phase

### Reconciler: `AwaitingDiscard` → `ApplyingDiscard`

Triggered when `spec.discard` is `true` and the phase is `AwaitingDiscard`, or `Blocked` with `spec.discard: true` (the resume path).

| # | Action | Persistence |
|---|---|---|
| 1 | If the cluster runs no mysqld instances (`spec.replicas <= 0` or `spec.offline: true`): set phase `Blocked` and `DiscardReady=False` (`reason=Blocked`) with a message; emit a `DiscardBlocked` Warning Event. The webhook forbids unsetting `spec.discard`, so the cycle simply waits for the cluster to run again. | Status.Update |
| 2 | Set phase `ApplyingDiscard` and `DiscardReady=False` (`reason=Pending`) in one update; emit `DiscardStarted` Event. Subsequent reconciles just requeue while ClusterManager drives DISCARD. | Status.Update |

### ClusterManager: `ApplyingDiscard` → `Finalizing`

Triggered on a ClusterManager tick when the phase is `ApplyingDiscard`. (The atomic phase+condition write above guarantees the `DiscardStarted` Event and `DiscardReady=False` are already recorded — no extra handshake is needed.) Pre-checks, in order:

1. The controller Secret must be in the **promoted** state for this rotationID (the `*_OLD` group with a matching `ROTATION_ID`, which stays in the Secret until `Finalizing`). Any other state — pending keys still present, or a Secret without bookkeeping keys (e.g. restored from a pre-rotation backup) — cannot prove that the current keys hold the promoted passwords. Running DISCARD would risk the core invariant, so: set phase `Failed` with a message and emit `RotationFailed` on the CR.
2. If the cluster runs no mysqld instances (0 replicas or offline): emit a `DiscardBlocked` Warning Event on the MySQLCluster and set phase `Blocked` and `DiscardReady=False` (`reason=Blocked`); the discard resumes when the cluster runs again.

| # | Action | Persistence |
|---|---|---|
| 1 | Determine the target auth plugin via `GetAuthPlugin` on the primary. | MySQL (read-only) |
| 2 | For each instance, connect with the **current** password. If the instance is a replica, or the intermediate primary when `spec.replicationSourceSecretName` is set, temporarily disable `super_read_only` (these instances run with it enabled) and re-enable it after the updates. Run `DISCARD OLD PASSWORD` for each user, skipping users that no longer have a dual password; then, in a second pass, migrate each user's auth plugin, skipping users already on the target plugin. | MySQL |
| 3 | Re-read the cluster's replica count from the live object (the tick's snapshot would hide a mid-step scale-up) and verify that no system user on any instance still holds a dual password — an instance added during step 2 cloned its donor's dual-password state. If one is found, run the discard pass again and re-verify. The loop is bounded per tick (3 rounds); if instances are still dirty after that, the handler reports the error and retries on a later tick. | MySQL |
| 4 | Set `DualPassword=False` (`reason=NotRetained`) and phase `Finalizing`. Emit `DiscardApplied` Event on the MySQLCluster. | Status.Update |

An unreachable instance aborts the loop for this tick; the flow retries on later ticks with no retry limit, and the failure is reported the same way as in the RETAIN flow (`PasswordRotationError` on the MySQLCluster, mirrored into the CR's `status.message`).

**Connecting with the current password is always correct.** DISCARD removes the *secondary* (old) password; the current (new) password is the primary and is unaffected before, during, and after DISCARD. There is no ordering hazard here — this is the invariant at work.

### Reconciler: `Finalizing` → `Succeeded`

This step is **bookkeeping only** — no password values move. The canonical current passwords were already promoted before distribution.

| # | Action | Persistence |
|---|---|---|
| 1 | Delete the `*_OLD` keys, `ROTATION_ID`, and `RETAIN_STARTED` from the controller Secret. Deleting absent keys is a no-op, so a crash-retry re-runs safely. | Secret.Update |
| 2 | Set phase `Succeeded`, `status.completionTime`, and `Finished=True` (`reason=Succeeded`); put the scheduled TTL deletion time into `status.message`. Emit `RotationCompleted` Event. | Status.Update |

If the Secret holds any other inconsistent state at this point (unpromoted pending keys, a partial `*_OLD` group, or a `ROTATION_ID` from a different cycle — only possible through manual tampering), the handler sets phase `Failed` with a message and emits `RotationFailed`, instead of hiding the inconsistency by deleting the bookkeeping around it.

## Resuming from Blocked

`Blocked` always means "the cluster stopped running mysqld instances mid-cycle" (0 replicas or `spec.offline: true`), and the **Reconciler owns every resume**. Its dispatch for phase `Blocked` needs no memory of the previous phase, because the resume target is fully determined by persisted state:

- `spec.discard: false` → re-run the **seed** handler. Seeding is idempotent (`ROTATION_ID` reuse, existing pending set kept), so this is correct whether the block happened before or after the original seeding; the handler ends by setting phase `ApplyingRetain`, and ClusterManager takes over. A block during `Promoting`/`AwaitingRollout` cannot occur: `Promoting` needs no running instances, and `AwaitingRollout` holds its own phase instead of going `Blocked` (see step 2 of its table) — precisely because this resume path could not replay that phase.
- `spec.discard: true` → re-run the **discard-start** handler, which re-checks the cluster and moves to `ApplyingDiscard`.

ClusterManager never acts on phase `Blocked`; it acts only on `ApplyingRetain` and `ApplyingDiscard`. The MySQLCluster watch (replicas and offline changes) makes the resume prompt.

## Completion and TTL Cleanup

A `Succeeded` CR is deleted by the Reconciler once `status.completionTime + TTL` has passed (controller flag `--credential-rotation-ttl`, default `1h`). The TTL keeps an observation window open: scripts can still read the final status, `kubectl wait --for=condition=Finished` completes normally, and `kubectl describe` shows the cycle's Events. The scheduled deletion time is visible in `status.message`.

Details:

- The cleanup order is strict: the controller Secret bookkeeping is removed in `Finalizing`, **before** the phase turns `Succeeded` — so deleting the CR never races with the Secret cleanup. A crash between the terminal status update and the TTL deletion just re-arms the TTL on the next reconcile (`completionTime` is persisted).
- The TTL delete is issued with a **UID precondition** (`DeleteOptions.Preconditions`), so a delayed deletion can never remove a new CR that was created under the same name after a manual delete.
- The TTL applies to `Succeeded` CRs regardless of staleness — deleting a terminal CR is always safe.
- During the TTL window the name is still occupied, so `rotate-credential` refuses with a message that says the previous rotation succeeded and the object may be deleted to start the next one immediately.
- After the TTL deletion the CR carries no history (by design — see Non-goals); the durable records are the `moco_credential_rotation_completed_timestamp_seconds` metric (see [Metrics](#metrics)) and the Kubernetes audit log.

## Failure Handling

The phase turns `Failed` when a controller detects a state it must not repair on its own:

| Detected at | Cause | Recovery procedure named in `status.message` |
|---|---|---|
| seed | `spec.discard: true` before the window ever opened (webhook bypass) | delete the CR, create a normal one |
| seed | `*_OLD` residue from an abandoned earlier cycle | [Leftover Old Passwords](#leftover-old-passwords-abandoned-cycle-after-promotion) |
| seed / `ApplyingRetain` / `Promoting` / `Finalizing` | partial key group, `ROTATION_ID` mismatch, or staged state lost (hand-edited or restored Secret) | [Inconsistent Controller Secret](#inconsistent-controller-secret) |
| `ApplyingRetain` | Secret already promoted for this rotationID (contradicts the phase) | [Inconsistent Controller Secret](#inconsistent-controller-secret) |
| `ApplyingDiscard` | Secret not in the promoted state (cannot prove current = promoted) | [Inconsistent Controller Secret](#inconsistent-controller-secret) |
| `ApplyingRetain` / `AwaitingRollout` / `ApplyingDiscard` | a current password key is missing from the controller Secret (hand-edited or restored Secret) — retrying cannot repair this, so the controller fails the CR instead of requeuing forever | [Inconsistent Controller Secret](#inconsistent-controller-secret) |
| any | the CR is stale (leftover from a deleted cluster; see below) | delete the CR |

Detection covers the **consistency of the bookkeeping keys** and the **presence** of the current password keys. A changed *value* of a current key cannot be detected (MySQL stores only hashes); that case is handled by the [reset procedure](#how-to-reset-mysql-passwords), not by this state machine.

A `Failed` CR:

- **stays** — the controller never deletes it,
- **blocks the next rotation** — the name is occupied: a raw `create` fails with `AlreadyExists`, and `rotate-credential` refuses with a message that names the recovery procedure, until the operator deletes the CR,
- carries the diagnosis in `status.message`, which names the recovery procedure, and in a Warning Event (`RotationFailed`; the stale case uses `StaleCredentialRotation` instead),
- sets `Finished=True` (`reason=Failed`) and `status.completionTime`, so waiting scripts terminate.

Recovery is always the same shape: follow the named [Recovery Procedure](#recovery-procedures), **delete the Failed CR**, and create a new one. The new CR starts from a clean slate — fresh UID, fresh status — so no state from the failed attempt can leak into the next cycle.

`Blocked` is different from `Failed`: it is a non-terminal pause and resumes automatically (see [Resuming from Blocked](#resuming-from-blocked)).

## Clusters Without Running Instances (replicas=0 or offline)

Two spec settings leave a MySQLCluster with no running mysqld instance: `spec.replicas: 0` and `spec.offline: true`. Rotation must treat them the same. `spec.offline` scales the StatefulSet down to zero Pods while `spec.replicas` keeps its positive value, so **every check in this section looks at both fields**.

> In practice a 0-replica MySQLCluster cannot be created through the normal API today: the CRD schema defaults `replicas` to 1, and the MySQLCluster webhook accepts only positive odd values and rejects decreases. The replicas half of the handling below is defense in depth (a bypassed webhook, a raw patch with an explicit 0) and future-proofing for scale-down support. The offline case can occur through the normal API at any time.

A cluster without running instances stops rotation at three points:

- At admission: the webhook rejects CR creation when `spec.replicas` is 0 or `spec.offline` is `true`.
- At reconcile time, before any mutation: if the cluster stopped running after admission, the seed handler sets phase `Blocked`. Nothing has been mutated; the cycle starts when the cluster runs again.
- Mid-cycle: the handlers set phase `Blocked` (with a `RotationBlocked` / `DiscardBlocked` Warning Event) and the Reconciler resumes the cycle when the cluster runs again.

A cluster taken down after promotion does not corrupt the cycle in `AwaitingRollout`: a 0-Pod StatefulSet would pass the rollout checks trivially, so the handler holds the phase (step 2 of its table) instead of opening the verification window, and the webhook keeps rejecting the discard flip while nothing runs. In all of these paused states the cluster keeps working with the canonical current passwords.

A scale-**up** mid-cycle is also safe: a new instance clones an existing instance's data, including its password and dual-password state (see [Assumptions](#assumptions)). Both MySQL-side flows additionally re-read the live replica count instead of trusting their tick's snapshot — RETAIN retains instances added mid-pass before it declares success (RETAIN step 3), and the discard flow re-verifies all instances before finishing (DISCARD step 3) — so an instance whose clone predates its donor's RETAIN or DISCARD cannot slip through.

## Stopped Clustering or Reconciliation

The [stop clustering / stop reconciliation feature](../usage.md#stop-clustering-and-reconciliation) also pauses a rotation in flight. A pause keeps the current phase (it is not `Blocked`); the Reconciler emits a `RotationPaused` Warning Event and sets `status.message` to name the blocking annotation, so the pause stays visible after the Event expires.

- With **reconciliation stopped**, `MySQLClusterReconciler` does not distribute the promoted passwords, so the cycle pauses in `AwaitingRollout` — but only while distribution has not finished yet. If the annotation is added after the promoted passwords were already distributed, the rest of the rollout continues and the cycle completes normally.
- With **clustering stopped**, the ClusterManager loop is paused, so `ApplyingRetain` and `ApplyingDiscard` cannot run their SQL. In addition, stopping clustering sets the cluster's `Healthy` condition to `Unknown`, and the partition controller only advances a rolling restart while the cluster is `Healthy` — so a cycle in `AwaitingRollout` freezes too. The `RotationPaused` Event and `status.message` cover all three affected phases (`ApplyingRetain`, `AwaitingRollout`, and `ApplyingDiscard`).

This is intentional: the stop annotations are explicit operator requests, and the rotation controllers must not bypass them. The paused states are safe — the current passwords keep authenticating everywhere — and the cycle resumes automatically when the operator runs `kubectl moco start clustering` / `start reconciliation`. The CLI refuses to *start* a step that a stop annotation would pause (see [User Interface](#user-interface)); the Event and `status.message` cover the case where the annotation is added mid-cycle.

A discard in flight is not affected by stopped reconciliation: the promoted passwords were distributed before `AwaitingDiscard`, and the discard phase runs on the ClusterManager and the CredentialRotationReconciler only.

## Metrics

The controller exports, per CredentialRotation (labels `name`, `namespace`):

- `moco_credential_rotation_phase{phase=...}` — 1 for the current phase, 0 otherwise (one series per known phase, mirroring the `moco_cluster_*` convention).
- `moco_credential_rotation_completed_timestamp_seconds` — `status.completionTime` of the last **`Succeeded`** rotation as a Unix timestamp; use it to compute the time since the last successful rotation for a cluster. It survives the CR's TTL deletion; the MySQLCluster finalizer removes it when the cluster itself is deleted, so a cluster recreated under the same name cannot inherit the previous cluster's success record.

Recommended alerts:

- **Stuck rotation**: a CR exists whose `phase` is not terminal for more than N minutes (`phase` gauge + object age). This covers `Blocked`, stop-annotation pauses, and unreachable-instance stalls alike; `status.message` and the Events on both objects identify which.
- **Failed rotation**: `phase="Failed"` > 0.
- **Long verification window**: `phase="AwaitingDiscard"` for more than N hours — the leaked old password keeps authenticating until the discard runs (see Non-goals).

## Controller Secret Layout

The controller Secret (in the system namespace) always holds the canonical current passwords, plus rotation bookkeeping keys during a cycle:

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

Partial states (some keys of a group missing, `ROTATION_ID` without either group, or a `ROTATION_ID` mismatch) are reported as an error. The two groups never coexist: promotion replaces the pending group with the old group in a single Secret update.

The `*_OLD` keys exist **only for recovery**: no controller logic reads them on the happy path. They preserve the previous password's plaintext (MySQL stores only hashes) so an operator can identify and reset the secondary password if a cycle is abandoned, and they serve as the promotion marker for crash recovery.

### Why a Single Controller Secret (No Candidate Secret)?

Staging the pending passwords in a separate Secret would make promotion a two-object operation ("read from A, write to B"), and a crash between the two writes could lose the new passwords for good. Inside a single Secret, promotion is a key rename applied by one API-server PUT — atomic by design. The `*_OLD` archive is included in the same update.

## Component Details

### CredentialRotationReconciler (new)

The reconciler watches:
- `CredentialRotation` (primary watched resource).
- `MySQLCluster` (update events filtered to `spec.replicas` changes, `spec.offline` flips, and `deletionTimestamp` flips; create and delete events pass through), mapped to the same namespace/name — so a `Blocked` cycle resumes immediately when the cluster runs again.
- `Secret` in the system namespace (the predicate filters on the namespace; the mapping function then parses the `mysql-<ns>.<name>` naming pattern), so promotion-related Secret changes are picked up without waiting for the 15-second requeue.
- `Secret` in cluster namespaces (the per-namespace user Secret `moco-<name>` and `my.cnf` Secret `moco-my-cnf-<name>`), so the `AwaitingRollout` distribution catch-up check runs as soon as MySQLClusterReconciler redistributes. A Secret named `moco-my-cnf-<x>` is ambiguous (it is also the user Secret of a cluster literally named `my-cnf-<x>`), so both interpretations are enqueued; the extra request is a cheap no-op.
- `StatefulSet` (`moco-<name>`), so the verification window opens as soon as the rollout settles instead of on the next periodic requeue.

For phases owned by ClusterManager (`ApplyingRetain`, `ApplyingDiscard`), the reconciler requeues every 15s while observing the status for progress. The TTL deletion of `Succeeded` CRs uses `RequeueAfter` until the deadline.

The rolling-restart annotation is written under the dedicated field manager `moco-credential-rotation`. `MySQLClusterReconciler`'s server-side apply never lists this annotation key, so the key stays owned by the Reconciler and survives the regular reconciles.

The reconciler never writes per-namespace Secrets, and rotation code never creates the controller Secret (update-only; see seed step 3).

### ClusterManager

ClusterManager reads the CredentialRotation CR inside each tick and dispatches on `status.phase`:
- `ApplyingRetain` → run the RETAIN flow on this cluster.
- `ApplyingDiscard` → run the DISCARD flow.
- Any other phase → no-op for rotation; normal clustering continues. (`Blocked` resumes are the Reconciler's job — see [Resuming from Blocked](#resuming-from-blocked).)

A stale CR (see below) is ignored. ClusterManager applies a stricter rule than the other components: it also ignores a CR that has no MySQLCluster ownerReference yet, and waits until the Reconciler has adopted it.

### MySQLClusterReconciler

`reconcileV1Secret` is **unchanged by rotation**: it always distributes the controller Secret's current passwords to the per-namespace user Secret and `my.cnf` Secret. It does not read the CredentialRotation CR and has no rotation-specific branches. Thanks to the core invariant, whatever it distributes — pre-promotion old values or post-promotion new values — authenticates on every instance.

One addition is needed for **speed**, not correctness: a watch on the controller Secret in the system namespace (filtered by the `mysql-<ns>.<name>` naming pattern, mapped to the owning cluster — the same pattern as the existing moco-agent certificate watch). Without it, redistribution after promotion would wait for the next unrelated reconcile. The CredentialRotationReconciler's `AwaitingRollout` phase waits on the result, so distribution latency directly delays the verification window.

## Crash Safety

Every row below preserves the core invariant: at each crash point, the controller Secret's current passwords authenticate on every instance, so no component ever loses access.

| Crash point | Recovery |
|---|---|
| CR created, pending passwords not yet generated | Reconciler re-seeds on next reconcile |
| Pending passwords generated, status not updated | `ROTATION_ID` reuse + `SetPendingPasswords` returns the existing pending set |
| Pre-check passed, `RETAIN_STARTED` marker set, RETAIN not yet executed | Marker skips pre-check; the per-user dual-password gate (verified against the pending password) makes RETAIN idempotent |
| RETAIN partially applied | The `RETAIN_STARTED` marker and the per-user verified gate make re-execution safe |
| RETAIN complete, phase not yet updated | Re-run sees all users already retained → writes the transition |
| `Promoting`, Secret not yet promoted | Promotion is a single atomic update; re-run performs it |
| `Promoting`, Secret promoted but phase not updated | `*_OLD` group present with matching `ROTATION_ID` and no `*_PENDING` group → promotion done → writes the transition |
| `AwaitingRollout`, distribution not yet caught up | MySQLClusterReconciler self-heals; rotation reconciler requeues |
| `AwaitingRollout`, restart annotation applied, rollout not settled | The pod template already carries this rotation's annotation → re-apply is skipped; the rollout check proceeds |
| DISCARD partially applied | `HasDualPassword` gates DISCARD → re-run skips finished users |
| DISCARD done on an instance, auth plugin not yet migrated | The plugin comparison gates migration → re-run migrates only users still on the old plugin |
| DISCARD complete, phase not yet updated | Re-run skips all users (and re-verifies) → writes the transition |
| `Finalizing`, keys deleted but phase not updated | The Secret is already clean → cleanup is a no-op → status re-runs (key deletion itself is one atomic Secret update, so a partial deletion cannot occur) |
| `Succeeded`, TTL deletion not yet performed | `completionTime` is persisted → the TTL re-arms and the deletion retries (with a UID precondition) |

### Why query MySQL instead of tracking per-user status?

MySQL holds only one secondary password slot per user. A second RETAIN with the same pending password would overwrite the secondary slot — evicting the original old password and breaking the controller's ability to connect. Per-user progress stored in Kubernetes status could drift from the real MySQL state. Instead, ClusterManager queries MySQL directly, so MySQL stays the source of truth: `HasDualPassword` (`mysql.user.User_attributes` for `additional_password`) detects the dual-password state, and `VerifyUserPassword` (a short connection as that user, using the pending password) proves that the dual password belongs to this cycle before the RETAIN for that user is skipped. Both checks are read-only and safe to re-run.

### Idempotency of DISCARD

`ALTER USER ... DISCARD OLD PASSWORD` is a no-op when there is no secondary password to discard. The DISCARD handler still queries `HasDualPassword` per user and skips users whose secondary password is already gone — mirroring the RETAIN gate and making a retry explicit in the logs.

## Deletion Handling

### CR deletion during rotation

Deletion is allowed at any phase (see [ValidateDelete](#validation-webhook)). Deleting the CR mid-cycle never breaks connectivity — the canonical current passwords keep working — but leaves residue. `*_PENDING` residue is harmless: the next CR adopts it and rolls forward. `*_OLD` residue sends the next CR to `Failed` until it is cleaned up:

| Deleted during | Residue | Effect on the next rotation |
|---|---|---|
| `ApplyingRetain` (before any RETAIN ran) | `*_PENDING` keys, `ROTATION_ID` | None — the seed handler adopts the staged pending passwords (never-promoted random values, equivalent to fresh ones) |
| `ApplyingRetain` (partial RETAIN) | Above + `RETAIN_STARTED` + dual passwords on some instances | None — RETAIN resumes where the abandoned cycle stopped (the marker skips the pre-check; per-user `HasDualPassword` keeps it idempotent) |
| `Promoting` (before the atomic Secret update) | Same as the partial-RETAIN row, with dual passwords on all instances | None — same as above |
| `Promoting` (Secret already promoted) .. `AwaitingRollout` .. `AwaitingDiscard` .. `ApplyingDiscard` | `*_OLD` keys, `ROTATION_ID`, dual passwords on all (or, mid-DISCARD, some) instances. **If the CR was deleted before the rollout settled, running Pods may still hold the old password in memory.** | Next CR goes `Failed` at seed — [Leftover Old Passwords](#leftover-old-passwords-abandoned-cycle-after-promotion), which checks the rollout before removing anything from MySQL |
| `ApplyingDiscard` (DISCARD done, plugin migration partial) | `*_OLD` keys, `ROTATION_ID`, no dual passwords, mixed auth plugins | Next CR goes `Failed` at seed — remove the keys; the plugin mix is harmless and converges on the next completed rotation |
| `Finalizing` | `*_OLD` keys, `ROTATION_ID` (no dual passwords) | Next CR goes `Failed` at seed — remove the keys |

In every row the StatefulSet pod template also keeps the restart annotation (`moco.cybozu.com/password-rotation-restart`) with the abandoned cycle's value. This is harmless: the next cycle overwrites it with its own rotationID, which is exactly what triggers that cycle's rolling restart.

The CR does **not** use a finalizer for automatic rollback: rollback requires connecting to every MySQL instance (which may not be possible during deletion), and a partial rollback is worse than no rollback. With the core invariant, skipping rollback costs nothing but residue.

### MySQLCluster deletion

The CR carries an ownerReference to its MySQLCluster, so garbage collection deletes the CR when the cluster is deleted. The ownerReference does not set `blockOwnerDeletion`: nothing about rotation needs to delay cluster termination, and CR deletion is always allowed. No special teardown is needed — the MySQL instances are being destroyed too.

While the cluster is terminating, all rotation handlers no-op (the cluster's finalizer deletes the controller Secret, and rotation code never creates it — see seed step 3 — so no half-Secret can come into existence).

Garbage collection is asynchronous. With the default background cascading deletion, the MySQLCluster object disappears first and the CR is collected shortly after. A new cluster with the same name can be created inside that window, which is why the stale-CR handling below exists.

One case escapes garbage collection entirely: the cluster is deleted after the CR is admitted but **before the Reconciler adopts it** (adds the ownerReference). Such a CR has no ownerReference and no status, so nothing would ever delete it — and when a cluster is created later under the same name, the Reconciler would treat the CR as fresh, adopt it, and start a rotation nobody asked for. The Reconciler closes this window when it sees the cluster gone: a CR without an ownerReference is marked `Failed` ("the target MySQLCluster was deleted before the rotation started; delete this CredentialRotation") with a `StaleCredentialRotation` Warning Event. The deletion is first confirmed with an uncached read, so an informer cache that lags a just-created cluster cannot cause a false failure. As with the stale handling below, writing the terminal status is not adoption.

### Stale CR handling (cluster recreated under the same name)

If a `MySQLCluster` is deleted and another is recreated under the same name before garbage collection reclaims the original CR, the leftover CR matches the new cluster by `namespace/name` but belongs to the old cluster. Adopting it would let leftover rotation state break the new cluster's credentials.

**"Stale" means either of:**

- the CR has a MySQLCluster ownerReference whose UID does **not** match the UID of the live cluster, or
- the CR has **no** MySQLCluster ownerReference but its status is not empty — an orphan from `kubectl delete --cascade=orphan`, which strips ownerReferences. (A CR with no ownerReference **and** an empty status is genuinely new and is treated as fresh.)

Behavior on a stale CR:

| Component | Behavior |
|---|---|
| `CredentialRotationReconciler` | Set phase `Failed` with `Finished=True` (`reason=Failed`) and message "leftover from a previous cluster; delete this CR", emit `StaleCredentialRotation` Warning Event, and take no other action. (Writing the terminal status is not adoption — no ownerReference is added and no rotation action runs. It makes the situation visible in `kubectl get` and terminates waiting scripts.) A stale CR that is already `Succeeded` is still TTL-deleted — deleting a terminal CR is always safe. |
| `ClusterManager` | Return early; do not run RETAIN / DISCARD |
| Validation webhook | Reject spec updates (the discard flip) on a stale CR |
| `MySQLClusterReconciler` | (does not read the CR at all) |
| `kubectl moco rotate-credential` / `discard-old-credential` | Refuse with an error instructing the user to delete the stale CR |

## Security Considerations

- `RotateUserPassword`, `DiscardOldPassword`, and `MigrateUserAuthPlugin` interpolate user names directly into SQL (MySQL does not support placeholders in the user position of `ALTER USER`; password values do use bind placeholders). Every rotation operation validates the user name against the fixed constants in `pkg/constants/users.go` at runtime before building the statement.
- `MigrateUserAuthPlugin` interpolates the plugin name into `IDENTIFIED WITH`. The value is validated against `^[a-zA-Z0-9_]+$` and derived from `@@global.authentication_policy` on the primary, never from user input.
- All `ALTER USER` rotation calls run under `SET sql_log_bin=0` on a dedicated `db.Conn` to prevent cross-cluster propagation.
- During a cycle the controller Secret temporarily holds one extra password set (`*_PENDING` before promotion, `*_OLD` after). Both live in the same Secret as the current passwords, so no new Secret permissions are needed. The controller additionally needs `delete` on `credentialrotations` for the TTL cleanup.

## Recovery Procedures

> Recovery is a MOCO-administrator action, not a tenant action: it reads and edits the controller Secret in the system namespace and runs `mysql` via `kubectl exec` on the cluster Pods.

All recovery procedures share one principle: **reset MySQL passwords to the current values in the controller Secret.** Thanks to the core invariant, the current values always authenticate, so recovery never needs to guess which password set is live. Note that `ALTER USER ... IDENTIFIED BY` (without RETAIN) only replaces the primary password — it does **not** remove a retained secondary password. For this reason, the reset scripts also run `ALTER USER ... DISCARD OLD PASSWORD` for every user. DISCARD removes the secondary password if one exists and does nothing otherwise, so MySQL returns to a clean single-password state. Without the DISCARD statements, the secondary passwords would stay, and the RETAIN pre-check (`DualPasswordExists`) would block the next rotation.

**Check the rollout before removing anything from MySQL.** moco-agent and mysqld-exporter read their passwords only at Pod start. If the abandoned cycle promoted the new passwords but its rolling restart did not complete, some running Pods still hold the **old** password in memory — and the reset script's DISCARD statements would cut them off. Before running a reset, confirm that every Pod restarted after the promotion (compare the Pods' start times with the controller Secret's last update, or check that the rollout triggered by the pod template's `moco.cybozu.com/password-rotation-restart` annotation has settled). If not, first trigger a rolling restart (`kubectl rollout restart statefulset moco-<name>`) and wait for it to finish. Pods that already run on the current passwords never need a restart.

After the MySQL/Secret recovery, the CR-side step is always the same: **delete the `Failed` CR, then create a new one** (`kubectl moco rotate-credential`).

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

### Inconsistent Controller Secret

**Symptom:** phase `Failed` with a `RotationFailed` Warning Event; `status.message` names this procedure. Covers failures detected at seed, `ApplyingRetain`, `Promoting`, `ApplyingDiscard`, and `Finalizing`.

**Cause:** The rotation bookkeeping in the controller Secret does not match the cycle: a partial key group, a `ROTATION_ID` mismatch, staged state lost, or a promoted/unpromoted state that contradicts the phase — typically because the Secret was edited by hand or restored from a backup. (A **complete** pending set left behind by a deleted CR is *not* this state: the next CR adopts it and continues — see [CR deletion during rotation](#cr-deletion-during-rotation).)

```console
# 0. If the failure happened at or after promotion (any *_OLD keys, or the
#    message says so): confirm every Pod restarted after the promotion; if
#    not, roll the StatefulSet first (see "Check the rollout" above).

# 1. If RETAIN may have run (RETAIN_STARTED or *_OLD keys present, the
#    Secret's history is unknown, or in doubt): reset MySQL passwords on
#    all instances (see "How to Reset MySQL Passwords"). This clears any
#    dual-password state. Skip only when you know the Secret's history and
#    neither RETAIN_STARTED nor *_OLD keys are present.

# 2. Clean the controller Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_PENDING keys, *_OLD keys, ROTATION_ID, and RETAIN_STARTED.

# 3. Delete the Failed CR and retry.
$ kubectl -n <namespace> delete credentialrotation <cluster-name>
$ kubectl moco rotate-credential <cluster-name>
```

### Leftover Old Passwords (Abandoned Cycle After Promotion)

**Symptom:** `*_OLD` keys and `ROTATION_ID` remain in the controller Secret with no active cycle (the CR was deleted between promotion and completion). The next `rotate-credential` creates a CR that immediately goes `Failed` at seed time, and `status.message` names this procedure.

**Impact:** None on the running cluster — the current passwords are canonical and authenticate everywhere. Instances may still hold the old password as a harmless secondary.

```console
# 0. Confirm every Pod restarted after the promotion (the abandoned cycle
#    may have been deleted before its rolling restart settled); if not,
#    roll the StatefulSet first (see "Check the rollout" above).

# 1. Reset MySQL passwords on all instances (see "How to Reset MySQL
#    Passwords"). This clears the leftover secondary passwords.
#    If the discard already completed before the CR was deleted (no
#    instance holds a dual password — e.g. deletion during Finalizing),
#    steps 0 and 1 can be skipped; the keys are the only residue.

# 2. Clean the controller Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_OLD keys and ROTATION_ID.

# 3. Delete the Failed CR (if one was created) and retry.
$ kubectl -n <namespace> delete credentialrotation <cluster-name>
$ kubectl moco rotate-credential <cluster-name>
```

### Dual Password Exists Outside the Current Cycle

**Symptom:** Warning Event `DualPasswordExists` on the MySQLCluster (mirrored into the CR's `status.message`), repeated on every ClusterManager tick while a rotation cycle waits in `ApplyingRetain`.

**Cause:** A system user already had `additional_password` set when the cycle's pre-check ran. Either a previous recovery did not fully clear MySQL state, or someone ran `ALTER USER ... RETAIN CURRENT PASSWORD` manually. The cycle waits; MySQL has not been changed (the pending passwords are already staged in the controller Secret).

**Why DISCARD is unsafe here:** After a manual RETAIN, the primary password is the new (unknown) value and the secondary is the old (known) value. DISCARD would remove the secondary, leaving only the unknown primary — breaking connectivity.

**Recovery:** No CR deletion or Secret cleanup needed.

```console
# 1. (recommended) Verify Pods can connect with current credentials.
# 2. Reset MySQL passwords on all instances (see "How to Reset MySQL Passwords").

# The waiting cycle proceeds by itself on a later ClusterManager tick,
# as soon as the pre-check passes.
```
