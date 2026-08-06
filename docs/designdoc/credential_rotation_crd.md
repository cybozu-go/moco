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
- Automatic periodic rotation (build externally with a CronJob that runs `kubectl moco rotate-credential`)
- Per-user rotation (all 8 users rotate together)
- End-user credential management
- Rollback of a started rotation (the design is roll-forward only; see [Roll-forward Only](#roll-forward-only))

## Assumptions

- **RETAIN is all-or-nothing.** `DualPassword=True` (the promotion precondition) is only set after `ALTER USER ... RETAIN` succeeded on **every** instance. The RETAIN loop must never skip an unreachable instance — the core invariant depends on this. If the loop skipped an instance, that instance would keep rejecting the canonical current password. Any future change to the RETAIN loop must preserve this property.
- No MOCO system user has a dual password when rotation starts. The pre-check on `ApplyingRetain` validates this; on violation it emits a `DualPasswordExists` Warning Event and waits. (The pre-check is skipped on crash recovery, when this cycle's `RETAIN_STARTED` marker is already set — per-user `HasDualPassword` checks take over.)
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
  │    │ ClusterManager (runs SQL only after DiscardReady=False/Pending):    │
  │    │            DISCARD OLD PASSWORD + auth plugin migration             │
  │    ▼                                                                     │
  │  Finalizing (DualPassword=False)                                         │
  │    │ Reconciler: delete *_OLD, ROTATION_ID, RETAIN_STARTED keys;         │
  │    │            emit RotationCompleted                                   │
  │    ▼                                                                     │
  │  Succeeded ──(TTL)──▶ the controller deletes the CR                      │
  └──────────────────────────────────────────────────────────────────────────┘

  Any unrecoverable inconsistency ──▶ Failed (the CR stays; the operator
                                      deletes it after following the
                                      recovery procedure)
```

State is exposed in two places:

- **`status.phase`** — where the workflow is. A single value that moves forward and ends in `Succeeded` or `Failed`. This is the field to look at for `kubectl get`, dashboards, and alerts.
- **Conditions** — independent observations that other components and scripts consume:
  - **`DiscardReady`** — `True` while the verification window is open and the operator may set `spec.discard: true`.
  - **`DualPassword`** — `True` while MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD).

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
- **Conditions hold independent observations**: `DualPassword` (MySQL's physical state) and `DiscardReady` (the action-availability gate that the webhook and `kubectl wait` consume).

Clients must treat the phase value set as **open**: new values may be added in future versions, and unknown values must be handled gracefully (per the API conventions' guidance for consumers of enums).

### Responsibility Split: Reconciler vs ClusterManager

The **CredentialRotationReconciler** handles K8s resource operations: phase/condition transitions, controller Secret management (seed / promote / cleanup), StatefulSet rolling-restart annotation, distribution catch-up wait, StatefulSet rollout wait, and TTL deletion of the completed CR.

The **ClusterManager** handles DB operations: dual-password pre-checks, `ALTER USER RETAIN`, `DISCARD OLD PASSWORD`, auth plugin migration (with a temporary `super_read_only` toggle on the instances that run with it). It also writes the state that belongs to these DB operations: the `RETAIN_STARTED` marker in the controller Secret, the `DualPassword` condition, and the `Blocked` phase.

The **MySQLClusterReconciler** distributes the controller Secret's current passwords to per-namespace Secrets — its normal job, unchanged by rotation.

Each phase has one *driver* that does the work and then writes the change that moves the CR to the next phase:

| Phase | Driver | What the driver writes on completion |
|---|---|---|
| `ApplyingRetain` | Reconciler (seed), then ClusterManager (SQL) | `DualPassword=True`, phase → `Promoting` |
| `Promoting` | Reconciler | phase → `AwaitingRollout` |
| `AwaitingRollout` | Reconciler (distribution itself: MySQLClusterReconciler) | `DiscardReady=True`, phase → `AwaitingDiscard` |
| `AwaitingDiscard` | (steady state — operator action) | (operator sets `spec.discard: true`) |
| `ApplyingDiscard` (initial) | Reconciler | `DiscardReady=False` (`reason=Pending`) |
| `ApplyingDiscard` (DB work) | ClusterManager | `DualPassword=False`, phase → `Finalizing` |
| `Finalizing` | Reconciler | phase → `Succeeded` |
| `Succeeded` | Reconciler | deletes the CR after the TTL |

Inside `ApplyingDiscard`, both components can run. ClusterManager waits until it observes `DiscardReady=False` (`reason=Pending`) before it runs any SQL, so the `DiscardStarted` Event and that condition stay visible.

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
  conditions:                 # illustrative; real entries also carry
    - type: DiscardReady      #   reason, message, lastTransitionTime
      status: "True"
    - type: DualPassword
      status: "True"
```

### Naming Convention

The CR name **must match** the target MySQLCluster name (same name, same namespace). One name buys three guarantees at once:

- **At most one rotation per cluster**, enforced by the API server itself (two objects cannot share a name). No List-based admission check, no race.
- **O(1) lookup**: the Reconciler, ClusterManager, and CLI find the CR by the cluster name, without a reference field or a label selector.
- **Fixed names in runbooks**: `kubectl wait credentialrotation <cluster-name> ...` works without discovering a generated name.

Because the CR is single-use, "at most one" also covers failure handling: a `Failed` CR keeps its name occupied, so a new rotation cannot start until the operator deletes it (see [Failure Handling](#failure-handling)).

### OwnerReference

CredentialRotation sets an ownerReference to the target MySQLCluster so that Kubernetes garbage-collects it on cluster deletion. The ownerReference does not set `blockOwnerDeletion`, and the CR carries no finalizer.

### Spec / Status Fields

| Field | Type | Notes |
|---|---|---|
| `spec.discard` | bool | Defaults to `false`. Must be `false` at create time. The only allowed update is `false` → `true`, and only while `DiscardReady=True`. It can never be set back to `false`. |
| `status.observedGeneration` | int64 | Standard `metadata.generation` echo for kstatus and similar tools. |
| `status.phase` | string | Workflow position. See [Phase](#phase). |
| `status.message` | string | Human-readable detail for the current phase; on `Failed`, it explains what went wrong and points to the recovery procedure. |
| `status.rotationID` | string | UUID for this cycle. Set when the pending passwords are seeded. |
| `status.conditions` | `[]metav1.Condition` | See [Conditions](#conditions). |

### Phase

`status.phase` moves forward through the following values. The set is **open**: clients must tolerate unknown values.

| Phase | Meaning | Terminal |
|---|---|---|
| `ApplyingRetain` | Pending passwords are seeded; ClusterManager is applying `RETAIN` on every instance. | no |
| `Promoting` | RETAIN succeeded everywhere; the Reconciler promotes pending → current in the controller Secret. | no |
| `AwaitingRollout` | Waiting for distribution to catch up and the StatefulSet rolling restart to settle. | no |
| `AwaitingDiscard` | Verification window. The operator may set `spec.discard: true`. | no |
| `ApplyingDiscard` | ClusterManager is running `DISCARD OLD PASSWORD` and the auth plugin migration. | no |
| `Finalizing` | The Reconciler removes the rotation bookkeeping keys from the controller Secret. | no |
| `Blocked` | The cycle cannot progress (e.g. the cluster was scaled to 0 replicas mid-cycle). Resumes automatically when the obstacle is removed; `status.message` and the conditions record where it stopped. | no |
| `Succeeded` | The full cycle completed. The controller deletes the CR after the TTL. | yes |
| `Failed` | An unrecoverable inconsistency was detected (see [Failure Handling](#failure-handling)). The CR stays until the operator deletes it. | yes |

The phase is persisted, not derived: whichever component (Reconciler or ClusterManager) performs a transition writes the new phase **in the same status update** as the conditions it changes, so a single read never sees the two disagree. Both writers use `retry.RetryOnConflict` with a fresh `Get`, so concurrent status updates are safe.

### Conditions

Two conditions carry the observations that other parties consume:

| Type | When `True` | When `False` |
|---|---|---|
| `DiscardReady` | Verification window is open (phase `AwaitingDiscard`): rotation done, rollout settled, dual password held. The operator may set `spec.discard: true`. | Any other point in the cycle. |
| `DualPassword` | MySQL holds a dual-password set on the system users (between successful RETAIN and successful DISCARD). | No dual-password state in MySQL. |

Reason vocabulary (each reason keeps a single meaning, per the API conventions):

| Reason | Appears on | With status | Meaning |
|---|---|---|---|
| `Reconciled` | `DiscardReady` | `True` | The verification window is open. |
| `Pending` | `DiscardReady` | `False` | Not in the verification window (before it opens, or the discard is running). |
| `Blocked` | `DiscardReady` | `False` | The discard cannot progress (e.g. 0 replicas). |
| `Retained` | `DualPassword` | `True` | MySQL holds a dual-password set on all system users. |
| `NotRetained` | `DualPassword` | `False` | MySQL is not currently holding a dual-password set. |

**Using `kubectl wait`.**

```console
# wait for the verification window
$ kubectl -n <ns> wait credentialrotation <cluster> --for=condition=DiscardReady

# wait for completion (works during the TTL window)
$ kubectl -n <ns> wait credentialrotation <cluster> --for=jsonpath='{.status.phase}'=Succeeded

# alternative: absence also means success (after the TTL)
$ kubectl -n <ns> wait credentialrotation <cluster> --for=delete
```

> **Events.** In addition to the status, the controllers emit Kubernetes Events for `kubectl describe` visibility. Events on the CredentialRotation (emitted by the Reconciler): `RotationStarted`, `PasswordsPromoted`, `AwaitingDiscard`, `DiscardStarted`, `RotationCompleted`, and the Warnings `RotationPaused`, `RotationFailed`, `StaleCredentialRotation`. Events on the MySQLCluster (emitted by the ClusterManager): `RetainApplied`, `DiscardApplied`, and the Warnings `RotationBlocked`, `DiscardBlocked`, `DualPasswordExists`, `PasswordRotationError`. `RotationFailed` carries the same detail as `status.message`.

## Validation Webhook

### ValidateCreate

All of the following must be true.

- The target MySQLCluster (same name, same namespace) must exist.
- `cluster.Spec.Replicas` must be `> 0`.
- `spec.discard` must be `false`. The discard must be requested via update after the verification window opens; `true` at create time would skip the window.

Note what create-time validation does **not** need to check: "is another rotation in flight" is enforced by the API server through the name constraint, and leftover state in the controller Secret is checked at runtime by the seed handler (which fails the CR with a clear message if `*_OLD` residue exists — see [Failure Handling](#failure-handling)).

### ValidateUpdate

The spec is immutable except for one transition:

- `spec.discard` may change from `false` to `true`, and only while `DiscardReady=True` (the verification window is open, so the post-promotion rollout has settled).
- `spec.discard` can never change from `true` to `false`.
- No other spec change is allowed.

### ValidateDelete

Deletion is **always allowed** — there is no delete webhook.

The core invariant makes CR deletion non-destructive: the per-namespace Secrets always hold the canonical current passwords, which authenticate on every instance regardless of when the CR disappears. Deleting the CR mid-cycle can only leave **residue**:

- dual passwords (a harmless secondary slot) on some or all instances, and
- stale `*_PENDING` / `*_OLD` / `ROTATION_ID` / `RETAIN_STARTED` keys in the controller Secret.

Neither affects a running cluster. `*_PENDING` residue does not even block the next cycle: the next CR's seed handler reuses the leftover `ROTATION_ID` and pending passwords — they are random values that were never promoted, so adopting them is equivalent to generating fresh ones — and RETAIN resumes where the abandoned cycle stopped. `*_OLD` residue **does** block the next cycle: the next CR goes `Failed` at seed time, until the residue is cleaned up with the [Recovery Procedures](#recovery-procedures). Garbage collection is never blocked by a webhook.

## User Interface

| Command | Behavior |
|---|---|
| `kubectl moco rotate-credential <cluster>` | Create the CR (`spec.discard: false`). If a CR already exists, refuse with a message that explains its state: in flight (wait or delete), `Succeeded` (waiting for TTL deletion — delete it to rotate again immediately), or `Failed` (follow the recovery procedure, then delete it). |
| `kubectl moco discard-old-credential <cluster>` | Refuse if stale; require `DiscardReady=True`; set `spec.discard: true` with an `Update` so a concurrent modification fails with a Conflict instead of being lost. |
| `kubectl moco credential <cluster>` | Read the per-namespace user Secret (unchanged from previous releases). |

Both mutating commands also check, at the time they run, whether the cluster can make progress. They refuse when `spec.offline` is `true`, when the `moco.cybozu.com/clustering-stopped` annotation is set to `true`, or when the MySQLCluster is not `Healthy`. In addition:

- `rotate-credential` refuses when `moco.cybozu.com/reconciliation-stopped=true`, because the rotation phase depends on MySQLClusterReconciler distributing the promoted passwords. It also refuses when `cluster.Spec.Replicas` is 0 — the webhook would reject the create anyway, but the CLI fails faster with a clearer message.
- `discard-old-credential` needs neither extra check, because distribution finished before the verification window opened.

The webhook and the controller remain the authority; these checks only fail fast with a clear message.

`kubectl get credentialrotation` prints `PHASE`, `DISCARD` (the spec flag), and `AGE`.

### Do Not Manage the CR with GitOps

The CredentialRotation CR is an **operation**, not desired state. Creating it runs a rotation, and the controller deletes it when the rotation succeeds. A GitOps tool that holds the manifest would immediately recreate the deleted object — and every recreation is a new, unrequested password rotation.

Do not commit CredentialRotation manifests to a GitOps-managed repository, and exclude the resource from sync if the tool discovers it. Drive rotation with `kubectl moco` (directly or from automation such as a CronJob).

## Rotation Phase

### Reconciler: seed on creation → `ApplyingRetain`

Runs when a CR appears with no phase yet.

| # | Action | Persistence |
|---|---|---|
| 1 | If `cluster.Spec.Replicas <= 0` (scaled down between admission and reconcile): set phase `Blocked` with a message; retry when replicas > 0. Nothing has been mutated. | Status.Update |
| 2 | Take the `ROTATION_ID` stored in the controller Secret, or generate a new UUID when there is none. Reusing the stored ID makes this step idempotent across crashes, and also adopts the residue of an abandoned cycle (see ValidateDelete). | — |
| 3 | Write 8 `*_PENDING` keys and `ROTATION_ID` into the controller Secret. A complete pending set that already matches the `ROTATION_ID` is kept as is. If the Secret is inconsistent or still holds `*_OLD` keys, set phase `Failed` with a message pointing to the recovery procedure and emit a `RotationFailed` Warning Event — that branch writes only the status, not the Secret. | Secret.Update |
| 4 | Set `status.rotationID`, phase `ApplyingRetain`, `DiscardReady=False` (`reason=Pending`), `DualPassword=False` (`reason=NotRetained`). Emit `RotationStarted` Event. | Status.Update |

### ClusterManager: `ApplyingRetain` → `Promoting`

Triggered on a ClusterManager tick when the phase is `ApplyingRetain`. The handler first checks the controller Secret: it waits until the Reconciler has written this cycle's `*_PENDING` keys, and it refuses a Secret that is already promoted for this rotationID — that combination contradicts the phase and needs manual recovery. Next, if the cluster was scaled down to 0 replicas, it emits a `RotationBlocked` Warning Event and sets phase `Blocked` — see [Scaled-down Clusters](#scaled-down-clusters-replicas0).

| # | Action | Persistence |
|---|---|---|
| 1 | Pre-check: every instance is scanned for pre-existing dual passwords. Steps 1 and 2 are both skipped when the `RETAIN_STARTED` marker already holds this cycle's rotationID (crash recovery). | — |
| 2 | Set `RETAIN_STARTED` marker (rotationID) in the controller Secret. | Secret.Update |
| 3 | For each instance, connect with the current password. If the instance is a replica, or the intermediate primary when `spec.replicationSourceSecretName` is set, temporarily disable `super_read_only` (these instances run with it enabled) and re-enable it after the updates. Run `ALTER USER ... RETAIN CURRENT PASSWORD` for each user, skipping users that already have a dual password. | MySQL |
| 4 | Set `DualPassword=True` (`reason=Retained`) and phase `Promoting` in one status update — this also clears a previous `Blocked` phase. Emit `RetainApplied` Event on the MySQLCluster. | Status.Update |

#### Pre-check and crash recovery

If any instance already has a dual password from outside this rotation cycle, emit a `DualPasswordExists` Warning Event and wait. After the pre-check succeeds, store the marker before running RETAIN. If the controller crashes and restarts, the marker — when it holds this cycle's rotationID — tells the handler to skip the pre-check and resume RETAIN. The `HasDualPassword` check for each user makes retries safe and idempotent.

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

From this moment the invariant flips from "current = old" to "current = new". Both remain true statements on every instance because of the dual-password window.

### `AwaitingRollout`: distribution and rollout

Distribution itself is **not** performed by the CredentialRotationReconciler. `MySQLClusterReconciler.reconcileV1Secret` distributes the controller Secret's current passwords — its normal behavior — and a watch on the controller Secret triggers it promptly after promotion (see [MySQLClusterReconciler](#mysqlclusterreconciler)).

The CredentialRotationReconciler gates progress:

| # | Action | Persistence |
|---|---|---|
| 1 | Wait until the per-namespace user Secret and `my.cnf` Secret are derived from the controller Secret's **current** passwords (content comparison; `CurrentPasswordsMatch` for the user Secret). If not yet, requeue — MySQLClusterReconciler will catch up. | — |
| 2 | Add `moco.cybozu.com/password-rotation-restart: <rotationID>` to the StatefulSet pod template with server-side apply (SSA), under field manager `moco-credential-rotation` with `ForceOwnership`. Skip the apply when the pod template already carries **this rotation's** annotation value. | StatefulSet.Apply |
| 3 | Check whether the StatefulSet rollout is complete — only against a pod template that carries this rotation's annotation, never against a stale pre-annotation object. Confirm that `status.observedGeneration` has caught up with `metadata.generation`, `status.currentRevision` matches `status.updateRevision`, and `status.replicas`, `status.updatedReplicas`, and `status.readyReplicas` all equal the desired `spec.replicas`. If any check is not satisfied, requeue and check again later. | — |
| 4 | After all rollout checks pass, set phase `AwaitingDiscard` and `DiscardReady=True` (`reason=Reconciled`). This records that the new password has been distributed and all Pods are ready, so the verification window is open. Emit an `AwaitingDiscard` Event. | Status.Update |

The skip condition in step 2 compares the annotation **value**, not just its presence: a template that still carries a *previous* rotation's rotationID is re-applied, and that re-apply is what triggers this cycle's rolling restart. The skip itself is required because every StatefulSet update that is not a pure partition change — including a content-no-op re-apply — passes the `StatefulSetDefaulter` mutating webhook, which resets `spec.updateStrategy.rollingUpdate.partition` to `replicas` to guard rollouts. Re-applying on each reconcile would keep resetting the partition that the partition controller walks down, and the rollout would never complete.

#### Why the annotation waits for distribution (step 1 before step 2)

Connectivity is never at risk. A Pod that restarts early reads the old password from the namespace Secret, which has not been updated yet, and the old password still works during the dual-password window. The ordering protects the **discard gate**: if the annotation triggered a rollout while the namespace Secret still held old values, the rollout could settle with Pods running on the old password, `DiscardReady` would flip to `True`, and a subsequent DISCARD would remove the very password those Pods use.

#### Why the reconciler waits for the rollout

The verification window is safe only after every Pod has restarted and its MySQL clients have picked up the new password from the distributed Secrets. For example, `moco-agent` reads credentials from the per-namespace user Secret, and `mysqld-exporter` reads them from the `my.cnf` Secret. If the controller opened the window before the rollout finished, `kubectl wait --for=condition=DiscardReady` could return while some Pods' clients still used the old password. An automation script could then start the discard step. `DISCARD OLD PASSWORD` would remove the secondary password that those clients still need, and they could lose access to MySQL.

The rollout is a Kubernetes operation, so the CredentialRotation Reconciler checks the StatefulSet status and opens the verification window. The ClusterManager performs the MySQL `DISCARD` operation only after this check has succeeded.

## Discard Phase

### Reconciler: `AwaitingDiscard` → `ApplyingDiscard`

Triggered when `spec.discard` is `true` and the phase is `AwaitingDiscard` or `Blocked` (during the discard).

| # | Action | Persistence |
|---|---|---|
| 1 | If `cluster.Spec.Replicas <= 0`: set phase `Blocked` with a message. The webhook forbids unsetting `spec.discard`, so the cycle simply waits for the cluster to be scaled up. | Status.Update |
| 2 | Set phase `ApplyingDiscard` and `DiscardReady=False` (`reason=Pending`), emit `DiscardStarted` Event, requeue. Subsequent reconciles just requeue while ClusterManager drives DISCARD. | Status.Update |

### ClusterManager: `ApplyingDiscard` → `Finalizing`

Triggered on a ClusterManager tick when the phase is `ApplyingDiscard`. The handler runs three pre-checks before any SQL, in this order:

1. The controller Secret must be in the **promoted** state for this rotationID (the `*_OLD` group with a matching `ROTATION_ID`, which stays in the Secret until `Finalizing`). Any other state — pending keys still present, or a Secret without bookkeeping keys (e.g. restored from a pre-rotation backup) — cannot prove that the current keys hold the promoted passwords, so the handler refuses to run DISCARD and reports the error (`PasswordRotationError` Warning Event) instead of risking the core invariant.
2. If the cluster was scaled down to 0 replicas, emit a `DiscardBlocked` Warning Event and set phase `Blocked`; the discard resumes when the cluster is scaled back up.
3. `DiscardReady` must already be `False` (`reason=Pending`), written by the CredentialRotationReconciler after the discard request is observed. If not yet, ClusterManager skips rotation work for that tick and retries on a later tick. This ordering records the in-flight state and emits the `DiscardStarted` Event before the DISCARD SQL.

| # | Action | Persistence |
|---|---|---|
| 1 | Determine the target auth plugin via `GetAuthPlugin` on the primary. | MySQL (read-only) |
| 2 | For each instance, connect with the **current** password. If the instance is a replica, or the intermediate primary when `spec.replicationSourceSecretName` is set, temporarily disable `super_read_only` (these instances run with it enabled) and re-enable it after the updates. Run `DISCARD OLD PASSWORD` for each user, skipping users that no longer have a dual password; then, in a second pass, migrate each user's auth plugin, skipping users already on the target plugin. | MySQL |
| 3 | Set `DualPassword=False` (`reason=NotRetained`) and phase `Finalizing`. Emit `DiscardApplied` Event on the MySQLCluster. | Status.Update |

**Connecting with the current password is always correct.** DISCARD removes the *secondary* (old) password; the current (new) password is the primary and is unaffected before, during, and after DISCARD. There is no ordering hazard here — this is the invariant at work.

### Reconciler: `Finalizing` → `Succeeded`

This step is **bookkeeping only** — no password values move. The canonical current passwords were already promoted before distribution.

| # | Action | Persistence |
|---|---|---|
| 1 | Delete the `*_OLD` keys, `ROTATION_ID`, and `RETAIN_STARTED` from the controller Secret. Deleting absent keys is a no-op, so a crash-retry re-runs safely. | Secret.Update |
| 2 | Set phase `Succeeded` and record the completion time. Emit `RotationCompleted` Event. | Status.Update |

If the Secret holds any other inconsistent state at this point (unpromoted pending keys, a partial `*_OLD` group, or a `ROTATION_ID` from a different cycle — only possible through manual tampering), the handler sets phase `Failed` with a message and emits `RotationFailed`, instead of hiding the inconsistency by deleting the bookkeeping around it.

## Completion and TTL Cleanup

A `Succeeded` CR is deleted by the Reconciler after a TTL (controller flag `--credential-rotation-ttl`, default `1h`). The TTL keeps an observation window open: scripts can still read the final status, `kubectl wait --for=jsonpath='{.status.phase}'=Succeeded` completes normally, and `kubectl describe` shows the cycle's Events.

Details:

- The cleanup order is strict: the controller Secret bookkeeping is removed in `Finalizing`, **before** the phase turns `Succeeded` — so deleting the CR never races with the Secret cleanup. A crash between the terminal status update and the TTL deletion just re-arms the TTL on the next reconcile.
- During the TTL window the name is still occupied, so `rotate-credential` refuses with a message that says the previous rotation succeeded and the object may be deleted to start the next one immediately.
- The last completion time is also recorded as a `RotationCompleted` Event on the CR and can be exported as a metric; after the TTL deletion the CR itself carries no history (by design — see Non-goals).

## Failure Handling

The phase turns `Failed` when the controller detects a state it must not repair on its own — in every case an inconsistent controller Secret:

- `*_OLD` residue from an abandoned earlier cycle at seed time,
- a partial key group or a `ROTATION_ID` mismatch (manual edits, a Secret restored from backup),
- pending passwords lost without promotion.

A `Failed` CR:

- **stays** — the controller never deletes it,
- **blocks the next rotation** — the name is occupied, so `rotate-credential` fails with `AlreadyExists` until the operator deletes the CR,
- carries the diagnosis in `status.message` and a `RotationFailed` Warning Event.

Recovery is always the same shape: follow the matching [Recovery Procedure](#recovery-procedures) (reset MySQL if needed, clean the controller Secret), **delete the Failed CR**, and create a new one. The new CR starts from a clean slate — fresh UID, fresh status — so no state from the failed attempt can leak into the next cycle.

`Blocked` is different from `Failed`: it is a non-terminal pause (typically `replicas: 0` mid-cycle) and resumes automatically when the obstacle is removed.

## Scaled-down Clusters (replicas=0)

A cluster with 0 replicas stops rotation at three points:

- At admission: the webhook rejects CR creation when `cluster.Spec.Replicas <= 0`.
- At reconcile time, before any mutation: if the cluster was scaled down to 0 after admission, the seed handler sets phase `Blocked`. Nothing has been mutated; the cycle starts when the cluster is scaled up.
- Mid-cycle: the ClusterManager handlers set phase `Blocked` (with a `RotationBlocked` / `DiscardBlocked` Warning Event) and resume when `cluster.Spec.Replicas > 0` again.

A scale-down after promotion does not park the cycle in `AwaitingRollout`: a 0-replica StatefulSet counts as settled, so the CR still reaches `AwaitingDiscard`. The discard handlers then stop at 0 replicas as described above. In all of these paused states the cluster keeps working with the canonical current passwords.

## Stopped Clustering or Reconciliation

The [stop clustering / stop reconciliation feature](../usage.md#stop-clustering-and-reconciliation) also pauses a rotation in flight:

- With **reconciliation stopped**, `MySQLClusterReconciler` does not distribute the promoted passwords, so the cycle pauses in `AwaitingRollout`. The cycle only pauses if distribution has not finished yet. If the annotation is added after the promoted passwords were already distributed, the rest of the rollout continues (the partition controller does not depend on `MySQLClusterReconciler`) and the cycle completes normally.
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

Staging the pending passwords in a separate Secret would make promotion a two-object operation ("read from A, write to B"), and a crash between the two writes could lose the new passwords for good. Inside a single Secret, promotion is a key rename applied by one API-server PUT — atomic by construction. The `*_OLD` archive rides in the same update.

## Component Details

### CredentialRotationReconciler (new)

The reconciler watches:
- `CredentialRotation` (primary watched resource).
- `MySQLCluster` (update events filtered to `Spec.Replicas` changes and `DeletionTimestamp` flips; create and delete events pass through), mapped to the same namespace/name — so a `Blocked` cycle resumes immediately on scale-up.
- `Secret` in the system namespace (the predicate filters on the namespace; the mapping function then parses the `mysql-<ns>.<name>` naming pattern), so promotion-related Secret changes are picked up without waiting for the 15-second requeue.
- `Secret` in cluster namespaces (the per-namespace user Secret `moco-<name>` and `my.cnf` Secret `moco-my-cnf-<name>`), so the `AwaitingRollout` distribution catch-up check runs as soon as MySQLClusterReconciler redistributes. A Secret named `moco-my-cnf-<x>` is ambiguous (it is also the user Secret of a cluster literally named `my-cnf-<x>`), so both interpretations are enqueued; the extra request is a cheap no-op.
- `StatefulSet` (`moco-<name>`), so the verification window opens as soon as the rollout settles instead of on the next periodic requeue.

For phases owned by ClusterManager (`ApplyingRetain`, `ApplyingDiscard` DB work), the reconciler requeues every 15s while observing the status for progress. The TTL deletion of `Succeeded` CRs uses `RequeueAfter` until the deadline.

The rolling-restart annotation is applied with the dedicated field manager `moco-credential-rotation` and `ForceOwnership`. This keeps the annotation owned by the credential rotation reconciler, so the regular `MySQLClusterReconciler` does not remove it during its next reconcile.

The reconciler never writes per-namespace Secrets.

### ClusterManager

ClusterManager reads the CredentialRotation CR inside each tick and dispatches on `status.phase`:
- `ApplyingRetain` → run the RETAIN flow on this cluster.
- `ApplyingDiscard` → run the DISCARD flow (only after observing `DiscardReady=False` (`reason=Pending`), written by the Reconciler).
- `Blocked` → re-run the matching flow when the obstacle is gone (`spec.discard` tells which flow).
- Any other phase → no-op for rotation; normal clustering continues.

A CR whose ownerReference UID does not match the live cluster (stale CR) is ignored. ClusterManager applies a stricter rule than the stale-CR definition used elsewhere: it also ignores a CR that has no MySQLCluster ownerReference yet, and waits until the Reconciler has adopted it.

### MySQLClusterReconciler

`reconcileV1Secret` is **unchanged by rotation**: it always distributes the controller Secret's current passwords to the per-namespace user Secret and `my.cnf` Secret. It does not read the CredentialRotation CR and has no rotation-specific branches. Thanks to the core invariant, whatever it distributes — pre-promotion old values or post-promotion new values — authenticates on every instance.

One addition is needed for **promptness** (not correctness): a watch on the controller Secret in the system namespace (filtered by the `mysql-<ns>.<name>` naming pattern, mapped to the owning cluster — the same pattern as the existing moco-agent certificate watch). Without it, redistribution after promotion would wait for the next unrelated reconcile. The CredentialRotationReconciler's `AwaitingRollout` phase waits on the result, so distribution latency directly delays the verification window.

## Crash Safety

Every row below preserves the core invariant: at each crash point, the controller Secret's current passwords authenticate on every instance, so no component ever loses access.

| Crash point | Recovery |
|---|---|
| CR created, pending passwords not yet generated | Reconciler re-seeds on next reconcile |
| Pending passwords generated, status not updated | `ROTATION_ID` reuse + `SetPendingPasswords` returns the existing pending set |
| Pre-check passed, `RETAIN_STARTED` marker set, RETAIN not yet executed | Marker skips pre-check; `HasDualPassword` makes RETAIN idempotent |
| RETAIN partially applied | `RETAIN_STARTED` marker + per-user `HasDualPassword` makes re-execution safe |
| RETAIN complete, phase not yet updated | Re-run sees all users already retained → writes the transition |
| `Promoting`, Secret not yet promoted | Promotion is a single atomic update; re-run performs it |
| `Promoting`, Secret promoted but phase not updated | `*_OLD` group present with matching `ROTATION_ID` and no `*_PENDING` group → promotion done → writes the transition |
| `AwaitingRollout`, distribution not yet caught up | MySQLClusterReconciler self-heals; rotation reconciler requeues |
| `AwaitingRollout`, restart annotation applied, rollout not settled | The pod template already carries this rotation's annotation → re-apply is skipped; the rollout check proceeds |
| `ApplyingDiscard`, `DiscardReady` not yet flipped to `False` (`reason=Pending`) | Reconciler flips it on next reconcile; ClusterManager skips DISCARD until it observes the condition |
| DISCARD partially applied | `HasDualPassword` gates DISCARD → re-run skips finished users |
| DISCARD done on an instance, auth plugin not yet migrated | The plugin comparison gates migration → re-run migrates only users still on the old plugin |
| DISCARD complete, phase not yet updated | Re-run skips all users → writes the transition |
| `Finalizing`, keys deleted but phase not updated | The Secret is already clean → cleanup is a no-op → status re-runs (key deletion itself is one atomic Secret update, so a partial deletion cannot occur) |
| `Succeeded`, TTL deletion not yet performed | The TTL re-arms on the next reconcile and the deletion retries |

### Why `HasDualPassword` instead of per-user status tracking?

MySQL holds only one secondary password slot per user. A second RETAIN with the same pending password would overwrite the secondary slot — evicting the original old password and breaking the controller's ability to connect. Per-user progress stored in Kubernetes status could drift from the real MySQL state. Instead, ClusterManager queries MySQL directly (`mysql.user.User_attributes` for `additional_password`), so MySQL stays the source of truth. The query is read-only and safe to re-run.

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
| `Promoting` (Secret already promoted) .. `ApplyingDiscard` | `*_OLD` keys, `ROTATION_ID`, dual passwords on all instances | Next CR goes `Failed` at seed — reset MySQL, then remove the keys ([Leftover Old Passwords](#leftover-old-passwords-abandoned-cycle-after-promotion)) |
| `Finalizing` | `*_OLD` keys, `ROTATION_ID` (no dual passwords) | Next CR goes `Failed` at seed — remove the keys |

The CR does **not** use a finalizer for automatic rollback: rollback requires connecting to every MySQL instance (which may not be possible during deletion), and a partial rollback is worse than no rollback. With the core invariant, skipping rollback costs nothing but residue.

### MySQLCluster deletion

The CR carries an ownerReference to its MySQLCluster, so garbage collection deletes the CR when the cluster is deleted. The ownerReference does not set `blockOwnerDeletion`: nothing about rotation needs to delay cluster termination, and CR deletion is always allowed. No special teardown is needed — the MySQL instances are being destroyed too.

Garbage collection is asynchronous. With the default background cascading deletion, the MySQLCluster object disappears first and the CR is collected shortly after. A new cluster with the same name can be created inside that window, which is why the stale-CR handling below exists. A `Failed` or TTL-pending CR can also outlive its cluster this way.

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
- During a cycle the controller Secret temporarily holds one extra password set (`*_PENDING` before promotion, `*_OLD` after). Both live in the same Secret as the current passwords, so no new Secret permissions are needed. The controller additionally needs `delete` on `credentialrotations` for the TTL cleanup.

## Recovery Procedures

All recovery procedures share one principle: **reset MySQL passwords to the current values in the controller Secret.** Thanks to the core invariant, the current values always authenticate, so recovery never needs to guess which password set is live. Note that `ALTER USER ... IDENTIFIED BY` (without RETAIN) only replaces the primary password — it does **not** remove a retained secondary password. For this reason, the reset scripts also run `ALTER USER ... DISCARD OLD PASSWORD` for every user. DISCARD removes the secondary password if one exists and does nothing otherwise, so MySQL returns to a clean single-password state. Without the DISCARD statements, the secondary passwords would stay, and the RETAIN pre-check (`DualPasswordExists`) would block the next rotation.

Recovery never requires restarting Pods: per-namespace Secrets only ever hold the canonical current values, so no Pod depends on a password that recovery would take away.

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

### Inconsistent Controller Secret (`Failed` before promotion)

**Symptom:** phase `Failed` with a `RotationFailed` Warning Event; the message names the inconsistency.

**Cause:** The rotation bookkeeping in the controller Secret is inconsistent: a partial `*_PENDING` group, a `ROTATION_ID` that does not match the cycle, or a `ROTATION_ID` without a key group — typically because the Secret was edited by hand or restored from a backup. (A **complete** pending set left behind by a deleted CR is *not* this state: the next CR adopts it and continues — see [CR deletion during rotation](#cr-deletion-during-rotation).)

```console
# 1. If RETAIN may have run (RETAIN_STARTED is present, or in doubt):
#    reset MySQL passwords on all instances (see "How to Reset MySQL
#    Passwords"). This clears any partial dual-password state.

# 2. Clean the controller Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_PENDING keys, *_OLD keys, ROTATION_ID, and RETAIN_STARTED.

# 3. Delete the Failed CR and retry.
$ kubectl -n <namespace> delete credentialrotation <cluster-name>
$ kubectl moco rotate-credential <cluster-name>
```

If `RETAIN_STARTED` is absent **and no `*_OLD` keys are present**, no `ALTER USER` was ever executed and step 1 can be skipped — the stale keys are the only residue. (Promotion removes the marker, so `*_OLD` keys mean RETAIN ran on every instance despite the missing marker.)

### Leftover Old Passwords (Abandoned Cycle After Promotion)

**Symptom:** `*_OLD` keys and `ROTATION_ID` remain in the controller Secret with no active cycle (the CR was deleted between promotion and completion). The next `rotate-credential` creates a CR that immediately goes `Failed` at seed time.

**Impact:** None on the running cluster — the current passwords are canonical and authenticate everywhere. Instances may still hold the old password as a harmless secondary.

```console
# 1. Reset MySQL passwords on all instances (see "How to Reset MySQL
#    Passwords"). This clears the leftover secondary passwords.

# 2. Clean the controller Secret.
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_OLD keys and ROTATION_ID.

# 3. Delete the Failed CR (if one was created) and retry.
$ kubectl -n <namespace> delete credentialrotation <cluster-name>
$ kubectl moco rotate-credential <cluster-name>
```

### Dual Password Exists Outside the Current Cycle

**Symptom:** Warning Event `DualPasswordExists` on the MySQLCluster, repeated on every ClusterManager tick while a rotation cycle waits in `ApplyingRetain`.

**Cause:** A system user already had `additional_password` set when the cycle's pre-check ran. Either a previous recovery didn't fully clear MySQL state, or someone ran `ALTER USER ... RETAIN CURRENT PASSWORD` manually. The cycle waits; MySQL has not been changed (the pending passwords are already staged in the controller Secret).

**Why DISCARD is unsafe here:** After a manual RETAIN, the primary password is the new (unknown) value and the secondary is the old (known) value. DISCARD would remove the secondary, leaving only the unknown primary — breaking connectivity.

**Recovery:** No CR deletion or Secret cleanup needed.

```console
# 1. (recommended) Verify Pods can connect with current credentials.
# 2. Reset MySQL passwords on all instances (see "How to Reset MySQL Passwords").

# The waiting cycle proceeds by itself on a later ClusterManager tick,
# as soon as the pre-check passes.
```
