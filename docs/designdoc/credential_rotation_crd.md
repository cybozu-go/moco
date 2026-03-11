# CredentialRotation CRD Design

## Background

[Issue #849](https://github.com/cybozu-go/moco/issues/849) introduces in-place system user password rotation for MOCO.
The initial design embeds rotation state into `MySQLCluster.Status.SystemUserRotation` and drives the rotation logic from within `MySQLClusterReconciler`.

This document proposes an alternative architecture: a dedicated **CredentialRotation** CRD with its own controller.

## Motivation

Embedding password rotation into MySQLCluster has several drawbacks:

1. **Blast radius** — If the rotation handler stalls or panics, the entire `MySQLClusterReconciler` reconcile loop is blocked. This impacts unrelated operations such as StatefulSet reconciliation, Service management, and backup CronJob creation.

2. **Status bloat** — `MySQLCluster.Status` already contains conditions, backup status, reconcile info, replica counts, and more. Adding `SystemUserRotationStatus` (Phase, RotationID, LastRotationID) further enlarges the CRD. Kubernetes CRD objects have an etcd-imposed size limit (~1.5 MB), so splitting when there is a clean boundary is preferable.

3. **Testability** — `MySQLClusterReconciler` is already large and complex. Inserting password rotation into its reconcile loop makes it harder to test rotation logic in isolation.

4. **Separation of concerns** — Password rotation is an operator-initiated, infrequent operation with its own lifecycle. It does not belong in the same reconcile loop as continuous cluster management.

KubeDB takes a similar approach with `MySQLOpsRequest` (type: RotateAuth) as a separate CRD for credential rotation operations.

## Goals

- Define a `CredentialRotation` CRD that encapsulates the full rotation lifecycle
- Isolate rotation processing in a dedicated `CredentialRotationReconciler`
- Minimize changes to `MySQLClusterReconciler` (only a guard in secret distribution)
- Preserve all crash safety and idempotency properties from the original design
- Keep the two-step (rotate → verify → discard) user interaction model

## Non-goals

- Automatic periodic rotation (can be built externally with a CronJob that creates CredentialRotation CRs)
- Per-user rotation (all 8 system users rotate together)
- End-user credential management

## CRD Definition

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata:
  name: my-cluster            # Same name as the target MySQLCluster
  namespace: my-namespace     # Same namespace as the target MySQLCluster
  ownerReferences:
    - apiVersion: moco.cybozu.com/v1beta2
      kind: MySQLCluster
      name: my-cluster
      uid: ...
spec:
  discardOldPassword: false   # User sets true to trigger discard phase
status:
  phase: ""                   # Current rotation phase
  rotationID: ""              # UUID for this rotation cycle
  conditions:
    - type: RotateCompleted
    - type: DiscardCompleted
```

### Naming Convention

The CredentialRotation resource name **must match** the target MySQLCluster name (same name, same namespace).
This naturally enforces at most one active rotation per cluster and simplifies lookups — both the controller and ClusterManager can find the CR by the cluster name without a separate reference field.

After a rotation completes (Phase=Completed), the user deletes the CR before starting a new rotation.
The `kubectl moco` CLI handles this transparently — deleting a completed CR before creating a new one.

### OwnerReference

CredentialRotation sets an ownerReference to the target MySQLCluster.
This ensures garbage collection when the MySQLCluster is deleted.

## User Interface

```console
# Rotate: create a CredentialRotation CR
$ kubectl moco credential rotate <cluster-name>

# Check status
$ kubectl get credentialrotation <cluster-name>
NAME         PHASE     AGE
my-cluster   Rotated   5m

# Discard: update the existing CR
$ kubectl moco credential discard <cluster-name>

# Show current credentials (unchanged from original design)
$ kubectl moco credential show <cluster-name>
```

### kubectl moco behavior

| Command | Action |
|---------|--------|
| `credential rotate` | Delete existing Completed CR (if any) → Create new CredentialRotation CR |
| `credential discard` | Validate Phase=Rotated → Patch `spec.discardOldPassword=true` |
| `credential show` | Read per-namespace user Secret (unchanged) |

The CLI sets the ownerReference on the CR and validates preconditions (MySQLCluster with the same name exists, replicas > 0, no in-progress rotation).

Users can also interact with the CR directly via `kubectl`:

```console
# Manual rotation trigger
$ kubectl apply -f credential-rotation.yaml

# Manual discard trigger
$ kubectl patch credentialrotation my-cluster --type=merge \
    -p '{"spec":{"discardOldPassword":true}}'
```

## Phase Transitions

```
CredentialRotationReconciler              ClusterManager
────────────────────────────              ──────────────

"" ──► Rotating
  Generate pending passwords
  in source Secret
                                          Rotating ──► Retained
                                            ALTER USER ... RETAIN CURRENT PASSWORD
                                            on all instances (sql_log_bin=0)
Retained ──► Rotated
  Distribute pending passwords
  to per-namespace Secrets
  Trigger rolling restart

  ──── User verifies applications ────

Rotated ──► Discarding
  (triggered by discardOldPassword=true)
  Wait for StatefulSet rollout
                                          Discarding ──► Discarded
                                            DISCARD OLD PASSWORD
                                            + auth plugin migration
                                            on all instances (sql_log_bin=0)
Discarded ──► Completed
  Promote pending → current
  in source Secret
```

### Phase Values

| Phase | Meaning |
|-------|---------|
| `""` (empty) | Initial state; rotation not yet started |
| `Rotating` | Pending passwords generated; waiting for RETAIN |
| `Retained` | ALTER USER RETAIN completed on all instances |
| `Rotated` | New passwords distributed; rolling restart triggered; awaiting user verification |
| `Discarding` | Rollout verified; waiting for DISCARD |
| `Discarded` | DISCARD OLD PASSWORD completed on all instances |
| `Completed` | Passwords promoted; rotation finished |

## Component Responsibilities

### CredentialRotationReconciler (new)

Handles all K8s resource operations for the rotation lifecycle:

| Phase transition | Actions |
|---|---|
| `""` → `Rotating` | Generate UUID as rotationID. Write pending passwords to source Secret via `password.SetPendingPasswords()`. Update Phase. |
| `Retained` → `Rotated` | Distribute pending passwords to per-namespace Secrets (user Secret + my.cnf Secret). Add restart annotation to StatefulSet Pod template. Update Phase. Set condition `RotateCompleted=True`. |
| `Rotated` → `Discarding` | Validate `spec.discardOldPassword=true`. Check StatefulSet rollout completion (ObservedGeneration, CurrentRevision==UpdateRevision, UpdatedReplicas==Replicas, ReadyReplicas==Replicas). Update Phase. If rollout incomplete, `RequeueAfter: 15s`. |
| `Discarded` → `Completed` | Promote pending → current in source Secret via `password.ConfirmPendingPasswords()`. Update Phase. Set condition `DiscardCompleted=True`. |

For phases where the ClusterManager drives progress (`Rotating`, `Discarding`), the Reconciler requeues every 15 seconds to check for phase advancement.

**Scaled-down clusters (replicas=0):**
Refuse rotation at CR creation time (validation webhook rejects, or Reconciler emits a Warning Event and does not advance past the initial phase).

### ClusterManager (modified)

Handles MySQL-level operations. The change is minimal — read/write the CredentialRotation CR instead of `MySQLCluster.Status.SystemUserRotation`.

| Phase | Actions |
|---|---|
| `Rotating` → `Retained` | Pre-check `HasDualPassword` on all instances. Execute `ALTER USER ... RETAIN CURRENT PASSWORD` with `sql_log_bin=0` on every instance. Update CredentialRotation status Phase to `Retained`. |
| `Discarding` → `Discarded` | Determine target auth plugin via `GetAuthPlugin()`. Execute `DISCARD OLD PASSWORD` + auth plugin migration with `sql_log_bin=0` on every instance. Update CredentialRotation status Phase to `Discarded`. |

```go
func (p *managerProcess) handlePasswordRotation(ctx context.Context, ss *StatusSet) (bool, error) {
    var cr mocov1beta2.CredentialRotation
    err := p.client.Get(ctx, types.NamespacedName{
        Namespace: p.name.Namespace,
        Name:      p.name.Name,
    }, &cr)
    if apierrors.IsNotFound(err) {
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

The DB operation logic (`rotateInstanceUsers`, `discardInstanceUsers`, `checkInstanceDualPasswords`) is **reused as-is**. Only the status update target changes from `MySQLCluster.Status` to `CredentialRotation.Status`.

### MySQLClusterReconciler (minimal change)

The only change is a guard in `reconcileV1Secret` that checks whether an active CredentialRotation exists:

```go
func (r *MySQLClusterReconciler) reconcileV1Secret(ctx context.Context, ...) (ctrl.Result, error) {
    // ... source Secret creation (unchanged) ...

    // During credential rotation, the CredentialRotationReconciler owns
    // secret distribution. Skip to prevent overwriting pending passwords.
    var cr mocov1beta2.CredentialRotation
    if err := r.Get(ctx, client.ObjectKey{
        Namespace: cluster.Namespace,
        Name:      cluster.Name,
    }, &cr); err == nil {
        phase := cr.Status.Phase
        if phase != "" && phase != mocov1beta2.RotationPhaseCompleted {
            return nil
        }
    }

    // ... normal secret distribution ...
}
```

The CredentialRotation CR itself is the source of truth — no intermediate annotation is needed.
The `r.Get()` reads from the informer cache, which controller-runtime starts on demand for the CredentialRotation GVK.
The overhead is negligible (CredentialRotation objects are few and small).

**Cache lag safety:**
If the cache briefly shows a stale Phase, the guard is still safe:
- Phase appears as `""` (CR just created, pending not yet distributed to user Secrets) → no skip → distributes old passwords → harmless
- Phase appears as `Rotating` (pending not yet distributed to user Secrets) → skip → harmless
- Any Phase from `Retained` onward → skip → correct

The only theoretical risk is if the cache does not yet reflect the CR's existence at all, while the CredentialRotationReconciler has already distributed pending passwords (Retained → Rotated).
In practice this cannot happen because RETAIN executes on all MySQL instances (takes seconds to minutes), far exceeding the cache propagation delay (~hundreds of milliseconds).

**Removed from MySQLClusterReconciler:**
- `reconcileV1PasswordRotation()` and all rotation handler methods
- `controllers/password_rotation.go` (entire file)
- Rotation annotation handling logic

**Removed from MySQLCluster CRD:**
- `SystemUserRotationStatus` type and `SystemUserRotation` field from `MySQLClusterStatus`
- `AnnPasswordRotate`, `AnnPasswordDiscard` annotation constants

## Go Type Definitions

```go
// api/v1beta2/credentialrotation_types.go

// CredentialRotationSpec defines the desired state of CredentialRotation.
// The target MySQLCluster is identified by the CR's own name and namespace
// (CredentialRotation name must equal MySQLCluster name).
type CredentialRotationSpec struct {
    // DiscardOldPassword triggers the discard phase.
    // Can only be set to true after Phase reaches Rotated.
    // Once set to true, cannot be reverted to false.
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

    // Conditions represent the latest available observations.
    // +optional
    Conditions []metav1.Condition `json:"conditions,omitempty"`
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

## Validation Webhook

```go
func (r *CredentialRotation) ValidateCreate(ctx context.Context, ...) {
    // 1. MySQLCluster with the same name must exist in the same namespace
    // 2. MySQLCluster replicas must be > 0
}

func (r *CredentialRotation) ValidateUpdate(ctx context.Context, ...) {
    // 1. discardOldPassword: only false→true transition allowed
    // 2. discardOldPassword=true requires Phase==Rotated
}
```

## Source Secret Layout

Unchanged from the original design.
During rotation, the source Secret (in the controller namespace) holds both current and pending passwords:

```
ADMIN_PASSWORD:         <current>
AGENT_PASSWORD:         <current>
...
ADMIN_PASSWORD_PENDING: <new>       # only during rotation
AGENT_PASSWORD_PENDING: <new>       # only during rotation
...
ROTATION_ID:            <uuid>      # only during rotation
```

## Crash Safety

All crash safety properties from the original design are preserved:

| Crash Point | Recovery |
|---|---|
| CR created, pending passwords not yet generated | Reconciler re-generates on next reconcile |
| Pending passwords generated, RETAIN not started | ClusterManager picks up Phase=Rotating |
| RETAIN partially applied (some instances) | `HasDualPassword` makes re-execution idempotent |
| RETAIN complete, Phase=Retained not yet set | ClusterManager re-runs → all skip → sets Retained |
| Phase=Retained, Secrets not yet distributed | Reconciler distributes on next reconcile |
| Phase=Discarding, DISCARD not yet executed | ClusterManager picks up Phase=Discarding |
| DISCARD complete, Phase=Discarded not yet set | DISCARD is idempotent → re-run → sets Discarded |
| Phase=Discarded, Secret not yet promoted | `ConfirmPendingPasswords` is idempotent |

### Stale Rotation Detection

The `LastRotationID` field is no longer needed.
The CredentialRotation CR itself represents the rotation lifecycle.
A Completed CR is an explicit record; the user (or CLI) deletes it before starting a new rotation.

### Status Update Conflict Handling

The ClusterManager uses `retry.RetryOnConflict` with `Status().Update()` on the CredentialRotation CR for phase transitions, the same pattern as the current `MySQLCluster` status updates.

## Interaction with Other Reconcile Steps

### reconcileV1Secret

During an active rotation, `reconcileV1Secret` checks for the existence of a CredentialRotation CR via `r.Get()` (informer cache).
If one exists with a non-empty, non-Completed Phase, secret distribution is skipped to prevent overwriting pending passwords.

After rotation completes (Phase=Completed or CR deleted), the source Secret already contains promoted passwords, so normal distribution resumes correctly.

### GatherStatus and ClusterManager Connection

Unchanged. GatherStatus reads passwords from the per-namespace user Secret.
During rotation phases, the user Secret always contains passwords that MySQL accepts (via dual password or newly distributed credentials).
The rotation handler reads passwords from the source Secret independently.

### StatefulSet Rolling Restart

The CredentialRotationReconciler patches the StatefulSet Pod template annotation (`moco.cybozu.com/password-rotation-restart`) to trigger rolling restart.
Since `MySQLClusterReconciler` uses server-side apply with its own field manager, the annotation set by a different field manager is preserved.

## Deletion Handling

### Mid-rotation Deletion

If a user deletes the CredentialRotation CR while rotation is in progress, MySQL may be left in an inconsistent state (some instances with dual passwords, pending passwords in the source Secret).

**Approach:** The same manual recovery procedures from the original design apply. The CredentialRotation CR does **not** use a finalizer for automatic rollback, because:
- Rollback requires connecting to every MySQL instance, which may not be possible during deletion (e.g., if the cluster is being scaled down)
- Partial rollback is worse than no rollback — it is safer to leave the state for manual inspection

The CLI warns before deleting an in-progress rotation:
```console
$ kubectl moco credential cancel my-cluster
WARNING: Rotation is in progress (Phase=Retained). Manual recovery may be required.
Proceed? [y/N]
```

### MySQLCluster Deletion

OwnerReference ensures the CredentialRotation CR is garbage-collected when the MySQLCluster is deleted. No special handling is needed because the MySQL instances are also being destroyed.

## Recovery Procedures

Recovery procedures are largely unchanged from the original design.
The difference is that the user resets the CredentialRotation CR (delete it) instead of editing `MySQLCluster.Status`.

### Stale Pending Passwords

```console
# Step 1: Delete the CredentialRotation CR
$ kubectl delete credentialrotation my-cluster

# Step 2: Clean the source Secret
$ kubectl -n <system-namespace> edit secret <controller-secret-name>
# Delete all *_PENDING keys and ROTATION_ID

# Step 3: Wait for rollout with old passwords
$ kubectl -n <namespace> rollout status statefulset <cluster-name>

# Step 4: Reset MySQL passwords on all instances
# See "How to Reset MySQL Passwords" in the original design doc
```

### Dual Password Exists While No CredentialRotation

```console
# Step 1: Verify no CredentialRotation CR exists
$ kubectl get credentialrotation my-cluster

# Step 2: Reset MySQL passwords on all instances
# See "How to Reset MySQL Passwords" in the original design doc
```

## Impact Summary

| Category | Files |
|---|---|
| **New** | `api/v1beta2/credentialrotation_types.go`, `controllers/credentialrotation_controller.go`, validation webhook |
| **Major change** | `clustering/password_rotation.go` (status read/write target), `cmd/kubectl-moco/cmd/credential.go` (annotation → CR) |
| **Minor change** | `controllers/mysqlcluster_controller.go` (guard in `reconcileV1Secret`), `cmd/moco-controller/cmd/run.go` (register new reconciler) |
| **Delete** | `controllers/password_rotation.go`, `SystemUserRotationStatus` from `mysqlcluster_types.go`, `AnnPasswordRotate`/`AnnPasswordDiscard` annotation constants |
| **Reuse as-is** | `pkg/password/rotation.go`, `pkg/dbop/password.go`, all DB operation logic |
