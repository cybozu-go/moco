package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	mocoevent "github.com/cybozu-go/moco/pkg/event"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/cybozu-go/moco/pkg/password"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	credRotationRequeueInterval = 15 * time.Second

	// DefaultCredentialRotationTTL is how long a Succeeded CredentialRotation
	// is kept before the controller deletes it. The TTL keeps an observation
	// window open for scripts and kubectl describe.
	DefaultCredentialRotationTTL = time.Hour

	// credRotationFieldManager is the field manager for the CredentialRotation
	// reconciler's writes. It is intentionally distinct from
	// MySQLClusterReconciler's "moco-controller" so that fields written here
	// (notably the rolling-restart annotation on the StatefulSet pod
	// template) are not removed when MySQLClusterReconciler re-applies its own
	// view of the StatefulSet, which does not declare the rotation annotation.
	credRotationFieldManager = "moco-credential-rotation"
)

// errCorruptControllerSecret marks a controller Secret whose current
// password keys cannot be read (manual edit or a restore from backup).
// Retrying cannot repair it, so callers turn it into a terminal Failed
// phase instead of requeuing forever.
var errCorruptControllerSecret = errors.New("the controller Secret is missing current password keys")

// CredentialRotationReconciler reconciles a CredentialRotation object
type CredentialRotationReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	SystemNamespace         string
	MaxConcurrentReconciles int
	// TTL is how long a Succeeded CR is kept before automatic deletion.
	// Zero means DefaultCredentialRotationTTL.
	TTL time.Duration
}

//+kubebuilder:rbac:groups=moco.cybozu.com,resources=credentialrotations,verbs=get;list;watch;update;patch;delete
//+kubebuilder:rbac:groups=moco.cybozu.com,resources=credentialrotations/status,verbs=get;update;patch

func (r *CredentialRotationReconciler) ttl() time.Duration {
	if r.TTL > 0 {
		return r.TTL
	}
	return DefaultCredentialRotationTTL
}

func (r *CredentialRotationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	cr := &mocov1beta2.CredentialRotation{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		if apierrors.IsNotFound(err) {
			// The phase series end with the object. The completed
			// timestamp survives CR deletion as the record of the last
			// successful rotation; it is removed by the MySQLCluster
			// finalizer instead, so a cluster recreated under the same
			// name cannot inherit the previous cluster's success record.
			// (Checking the cluster from the cache here would race both
			// a not-yet-drained deletion and a same-name recreation.)
			deleteRotationPhaseMetrics(req.Name, req.Namespace)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	defer func() { updateRotationMetrics(cr) }()

	cluster := &mocov1beta2.MySQLCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			// With an ownerReference, garbage collection deletes the CR;
			// nothing to do. Without one, the cluster disappeared before
			// this controller could adopt the CR, so garbage collection
			// will never come: mark the CR Failed (unless it already is
			// terminal), or a cluster recreated under the same name would
			// later adopt the leftover and start a rotation nobody asked
			// for. Writing the terminal status is not adoption: no
			// ownerReference is added and no rotation action runs.
			if len(cr.OwnerReferences) == 0 && !cr.IsTerminal() {
				log.Info("marking a CredentialRotation orphaned before adoption as Failed")
				mocoevent.StaleCredentialRotation.Emit(cr, r.Recorder)
				return ctrl.Result{}, r.markFailed(ctx, cr,
					"the target MySQLCluster was deleted before the rotation started; delete this CredentialRotation")
			}
			log.Info("MySQLCluster not found, skipping")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// A terminating cluster deletes its controller Secret through the
	// cluster finalizer, and the CR will be garbage-collected with the
	// cluster. Every rotation action must no-op meanwhile.
	if cluster.DeletionTimestamp != nil {
		log.Info("MySQLCluster is terminating, skipping")
		return ctrl.Result{}, nil
	}

	// A stale CR (a leftover from a previous cluster incarnation) is never
	// acted on. A Succeeded one is still TTL-deleted — deleting a terminal
	// CR is always safe. Anything else is marked Failed once, so the
	// situation is visible in kubectl get and waiting scripts terminate.
	// Writing the terminal status is not adoption: no ownerReference is
	// added and no rotation action runs.
	if cr.IsStaleFor(cluster) {
		switch cr.Status.Phase {
		case mocov1beta2.PhaseSucceeded:
			return r.handleSucceeded(ctx, cr)
		case mocov1beta2.PhaseFailed:
			return ctrl.Result{}, nil
		default:
			log.Info("marking stale CredentialRotation as Failed")
			mocoevent.StaleCredentialRotation.Emit(cr, r.Recorder)
			return ctrl.Result{}, r.markFailed(ctx, cr,
				"this CredentialRotation is a leftover from a previous MySQLCluster; delete it")
		}
	}

	// Adoption: add the MySQLCluster ownerReference BEFORE any status
	// write. Status-only-after-adoption is what makes the orphan half of
	// the stale rule sound (see CredentialRotation.IsStaleFor).
	if !hasOwnerReference(cr, cluster) {
		if err := controllerutil.SetOwnerReference(cluster, cr, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set ownerReference: %w", err)
		}
		if err := r.Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	switch cr.Status.Phase {
	case "":
		return r.handleSeed(ctx, cr, cluster)

	case mocov1beta2.PhaseBlocked:
		// The Reconciler owns every resume. The resume target is fully
		// determined by spec.discard: false → re-run the (idempotent)
		// seed; true → re-run the discard start.
		if cr.Spec.Discard {
			return r.handleStartDiscard(ctx, cr, cluster)
		}
		return r.handleSeed(ctx, cr, cluster)

	case mocov1beta2.PhaseApplyingRetain:
		// ClusterManager owns RETAIN. If clustering is stopped, its loop
		// is paused and RETAIN will not run; surface that instead of
		// waiting silently.
		if isClusteringStopped(cluster) {
			r.surfacePause(ctx, cr, fmt.Sprintf(
				"clustering is stopped (annotation %s=true), so RETAIN cannot run", constants.AnnClusteringStopped),
				"the annotation is removed")
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.PhasePromoting:
		return r.handlePromoting(ctx, cr, cluster)

	case mocov1beta2.PhaseAwaitingRollout:
		return r.handleAwaitingRollout(ctx, cr, cluster)

	case mocov1beta2.PhaseAwaitingDiscard:
		if cr.Spec.Discard {
			return r.handleStartDiscard(ctx, cr, cluster)
		}
		// Verification window: wait for the operator.
		return ctrl.Result{}, nil

	case mocov1beta2.PhaseApplyingDiscard:
		// ClusterManager owns DISCARD; same pause surfacing as RETAIN.
		if isClusteringStopped(cluster) {
			r.surfacePause(ctx, cr, fmt.Sprintf(
				"clustering is stopped (annotation %s=true), so DISCARD cannot run", constants.AnnClusteringStopped),
				"the annotation is removed")
		}
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil

	case mocov1beta2.PhaseFinalizing:
		return r.handleFinalize(ctx, cr, cluster)

	case mocov1beta2.PhaseSucceeded:
		return r.handleSucceeded(ctx, cr)

	case mocov1beta2.PhaseFailed:
		// Terminal; the operator recovers and deletes the CR.
		return ctrl.Result{}, nil

	default:
		log.Info("unknown rotation phase; ignoring", "phase", cr.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// handleSeed starts (or resumes) the rotate phase: it stages the pending
// passwords in the controller Secret and moves the CR to ApplyingRetain.
// Every step is idempotent, so it also serves as the resume path from a
// Blocked start.
func (r *CredentialRotationReconciler) handleSeed(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Defense in depth for a bypassed create webhook: a discard request
	// that predates the verification window would skip it silently.
	if cr.Spec.Discard {
		return r.fail(ctx, cr,
			"spec.discard was true before the verification window ever opened (the create webhook was bypassed); delete this CR and create one with discard: false")
	}

	if cluster.IsOfflineOrZeroReplicas() {
		// Nothing has been mutated. Avoid a no-op status write on every
		// requeue while the cluster stays down.
		if cr.Status.Phase == mocov1beta2.PhaseBlocked {
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		cr.InitConditions()
		cr.SetPhase(mocov1beta2.PhaseBlocked,
			"MySQLCluster has 0 replicas or is offline; the rotation starts when the cluster is running again. Nothing has been mutated.")
		if err := r.updateStatus(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Blocked: %w", err)
		}
		mocoevent.RotationBlocked.Emit(cr, r.Recorder)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		if apierrors.IsNotFound(err) {
			// Rotation code never creates the controller Secret: a Secret
			// holding only bookkeeping keys must never come into
			// existence. Wait for MySQLClusterReconciler to create it.
			log.Info("controller secret does not exist yet; waiting")
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get controller secret: %w", err)
	}

	// Reuse a stored ROTATION_ID: this makes seeding idempotent across
	// crashes and adopts the pending residue of an abandoned cycle (safe:
	// the leftover pending passwords are never-promoted random values).
	rotationID := password.GetRotationID(controllerSecret)
	if rotationID == "" {
		rotationID = uuid.New().String()
	}

	if state, err := password.RotationState(controllerSecret, rotationID); err != nil {
		return r.fail(ctx, cr, fmt.Sprintf(
			"cannot stage pending passwords: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	} else if state == password.RotationSecretPromoted {
		return r.fail(ctx, cr,
			"the controller Secret holds *_OLD keys from an abandoned cycle; follow the 'Leftover Old Passwords' recovery procedure, then delete this CR and create a new one")
	}
	if _, err := password.SetPendingPasswords(controllerSecret, rotationID); err != nil {
		return r.fail(ctx, cr, fmt.Sprintf(
			"cannot stage pending passwords: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	}
	if err := r.Update(ctx, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update controller secret with pending passwords: %w", err)
	}

	cr.Status.RotationID = rotationID
	cr.InitConditions()
	cr.SetPhase(mocov1beta2.PhaseApplyingRetain,
		"Applying ALTER USER ... RETAIN CURRENT PASSWORD on every instance.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to ApplyingRetain: %w", err)
	}

	log.Info("started rotation", "rotationID", rotationID)
	mocoevent.RotationStarted.Emit(cr, r.Recorder, rotationID)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handlePromoting promotes the pending passwords to current in the
// controller Secret. RETAIN succeeded on every instance (DualPassword=True),
// so both old and new passwords authenticate everywhere and the new set can
// safely become canonical.
func (r *CredentialRotationReconciler) handlePromoting(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controller secret: %w", err)
	}

	state, err := password.RotationState(controllerSecret, cr.Status.RotationID)
	if err != nil {
		return r.fail(ctx, cr, fmt.Sprintf(
			"inconsistent controller Secret at promotion: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	}
	switch state {
	case password.RotationSecretPending:
		if err := password.PromotePendingPasswords(controllerSecret, cr.Status.RotationID); err != nil {
			return r.fail(ctx, cr, fmt.Sprintf(
				"failed to promote the pending passwords: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
		}
		if err := r.Update(ctx, controllerSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update controller secret after promotion: %w", err)
		}
	case password.RotationSecretPromoted:
		// Crash recovery: the atomic Secret update already happened.
	default: // RotationSecretClean
		return r.fail(ctx, cr,
			"the staged pending passwords were lost without promotion (the controller Secret is clean); follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one")
	}

	cr.SetPhase(mocov1beta2.PhaseAwaitingRollout,
		"Waiting for the promoted passwords to be distributed and for the rolling restart to settle.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to AwaitingRollout: %w", err)
	}

	log.Info("promoted pending passwords to current", "rotationID", cr.Status.RotationID)
	mocoevent.PasswordsPromoted.Emit(cr, r.Recorder, cr.Status.RotationID)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleAwaitingRollout gates the verification window: it waits for the
// per-namespace Secrets to be derived from the promoted current passwords,
// triggers the rolling restart via the pod-template annotation, and waits
// for the StatefulSet rollout to settle.
func (r *CredentialRotationReconciler) handleAwaitingRollout(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	caughtUp, err := r.distributionCaughtUp(ctx, cluster)
	if err != nil {
		// Retrying cannot repair an unreadable controller Secret (a
		// current password key is missing — manual edit or a restore from
		// backup); fail with the recovery procedure like every other
		// controller-Secret inconsistency, so waiters on the Finished
		// condition do not hang forever.
		if errors.Is(err, errCorruptControllerSecret) {
			return r.fail(ctx, cr, fmt.Sprintf(
				"the controller Secret's current passwords are unreadable after promotion: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
		}
		return ctrl.Result{}, err
	}
	if !caughtUp {
		// Distribution is MySQLClusterReconciler's job; if reconciliation
		// is stopped, the promoted passwords will never propagate and the
		// rotation stays parked here. Surface that instead of waiting
		// silently. The wait itself is safe: the current passwords keep
		// authenticating everywhere.
		if isReconciliationStopped(cluster) {
			r.surfacePause(ctx, cr, fmt.Sprintf(
				"reconciliation is stopped (annotation %s=true), so the promoted passwords are not distributed", constants.AnnReconciliationStopped),
				"the annotation is removed")
		}
		log.Info("waiting for per-namespace Secrets to catch up with the promoted passwords",
			"rotationID", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	// Stopping clustering flips the cluster's Healthy condition to
	// Unknown, and the partition controller only advances a rollout while
	// the cluster is Healthy — so the rollout below freezes too.
	if isClusteringStopped(cluster) {
		r.surfacePause(ctx, cr, fmt.Sprintf(
			"clustering is stopped (annotation %s=true), so the rolling restart cannot advance", constants.AnnClusteringStopped),
			"the annotation is removed")
	}

	// spec.offline scales the StatefulSet down to zero Pods while
	// spec.replicas stays positive, and a 0-replica StatefulSet trivially
	// satisfies every rollout check below — the verification window would
	// open without a single restarted Pod. Hold this phase instead until
	// the cluster runs again. (Blocked is not used here: its resume path
	// with spec.discard=false re-runs the seed, and the seed treats the
	// promoted controller Secret as a failure.)
	if cluster.IsOfflineOrZeroReplicas() {
		r.surfacePause(ctx, cr,
			"the MySQLCluster has 0 replicas or is offline, so the rolling restart cannot settle",
			"the cluster is running again")
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	stsName := cluster.PrefixedName()
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      stsName,
	}, sts); err != nil {
		if apierrors.IsNotFound(err) {
			// The StatefulSet has not been created yet (or was deleted);
			// requeue and try again.
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get StatefulSet for rollout check: %w", err)
	}

	// Add the restart annotation to the StatefulSet Pod template via
	// Server-Side Apply under a dedicated field manager so the rotation
	// annotation key is not silently removed by the next MySQLCluster
	// reconcile.
	//
	// Apply ONLY when the pod template does not already carry this
	// rotation's annotation VALUE (a template with a previous cycle's
	// value is re-applied — that re-apply is what triggers this cycle's
	// rolling restart). A content-no-op re-apply is NOT harmless here:
	// every StatefulSet update that is not a pure partition change passes
	// the StatefulSetDefaulter mutating webhook, which resets
	// spec.updateStrategy.rollingUpdate.partition to replicas to guard
	// rollouts. Re-applying on each reconcile would keep resetting the
	// partition that StatefulSetPartitionReconciler walks down, and the
	// rollout would never complete. (MySQLClusterReconciler avoids the
	// same trap with its ExtractStatefulSet + DeepEqual gate.)
	//
	// The Get above is served from the informer cache, but reconciles are
	// triggered after the cache has stored the update, so at most one
	// extra apply can occur — and it also checks the rollout only against
	// an annotated template, never against a stale pre-annotation object.
	if sts.Spec.Template.Annotations[constants.AnnPasswordRotationRestart] != cr.Status.RotationID {
		// Use a merge patch, NOT server-side apply: SSA is an upsert, and
		// if the StatefulSet was deleted between the (cached) Get above
		// and the write, an apply would CREATE a minimal StatefulSet that
		// poisons the real one (immutable-field conflicts on the next
		// MySQLClusterReconciler apply). A merge patch fails with
		// NotFound instead, and the requeue retries safely.
		annJSON, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"template": map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]string{
							constants.AnnPasswordRotationRestart: cr.Status.RotationID,
						},
					},
				},
			},
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to marshal the rotation annotation patch: %w", err)
		}
		if err := r.Patch(ctx, sts, client.RawPatch(types.MergePatchType, annJSON), client.FieldOwner(credRotationFieldManager)); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to patch the rotation annotation onto the StatefulSet: %w", err)
		}
		log.Info("patched the rotation restart annotation onto the StatefulSet",
			"rotationID", cr.Status.RotationID)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	if !isStatefulSetRolloutComplete(sts) {
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	cr.SetPhase(mocov1beta2.PhaseAwaitingDiscard,
		"Verification window open: verify the applications, then request the discard with 'kubectl moco discard-old-credential'.")
	cr.SetDiscardReady(metav1.ConditionTrue, mocov1beta2.ReasonRolloutSettled,
		"The promoted passwords were distributed and the rolling restart settled; the discard may be requested.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after rollout: %w", err)
	}

	log.Info("post-promotion rollout settled; verification window open",
		"rotationID", cr.Status.RotationID)
	mocoevent.AwaitingDiscard.Emit(cr, r.Recorder, cr.Status.RotationID)
	return ctrl.Result{}, nil
}

// handleStartDiscard records the discard request: it moves the CR to
// ApplyingDiscard and flips DiscardReady to False/Pending in one atomic
// status update, so ClusterManager (which acts on the phase) can never run
// the DISCARD SQL before the request was recorded.
func (r *CredentialRotationReconciler) handleStartDiscard(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cluster.IsOfflineOrZeroReplicas() {
		// Avoid a no-op status write on every requeue while the cluster
		// stays down.
		if cr.Status.Phase == mocov1beta2.PhaseBlocked &&
			mocov1beta2.IsConditionFalseWithReason(cr, mocov1beta2.ConditionDiscardReady, mocov1beta2.ReasonBlocked) {
			return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
		}
		cr.SetPhase(mocov1beta2.PhaseBlocked,
			"MySQLCluster has 0 replicas or is offline; the discard resumes when the cluster is running again.")
		cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonBlocked,
			"The discard cannot progress: the cluster has 0 replicas or is offline.")
		if err := r.updateStatus(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status to Blocked: %w", err)
		}
		mocoevent.DiscardBlocked.Emit(cr, r.Recorder)
		return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
	}

	cr.SetPhase(mocov1beta2.PhaseApplyingDiscard,
		"Running DISCARD OLD PASSWORD and the auth plugin migration on every instance.")
	cr.SetDiscardReady(metav1.ConditionFalse, mocov1beta2.ReasonPending,
		"The discard is running; the verification window is closed.")
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to ApplyingDiscard: %w", err)
	}

	log.Info("discard requested", "rotationID", cr.Status.RotationID)
	mocoevent.DiscardStarted.Emit(cr, r.Recorder, cr.Status.RotationID)

	return ctrl.Result{RequeueAfter: credRotationRequeueInterval}, nil
}

// handleFinalize removes the rotation bookkeeping keys (*_OLD, ROTATION_ID,
// RETAIN_STARTED) from the controller Secret and moves the CR to Succeeded.
// No password values move — the canonical current passwords were promoted
// before distribution. Every action here is idempotent.
func (r *CredentialRotationReconciler) handleFinalize(ctx context.Context, cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get controller secret for cleanup: %w", err)
	}

	if err := password.CleanupRotationKeys(controllerSecret, cr.Status.RotationID); err != nil {
		// An inconsistent secret at Finalizing (unpromoted pending keys,
		// a partial *_OLD group, or a ROTATION_ID from a different cycle)
		// means manual tampering — refuse to hide that by deleting the
		// bookkeeping around it.
		return r.fail(ctx, cr, fmt.Sprintf(
			"inconsistent controller Secret at cleanup: %v; follow the 'Inconsistent Controller Secret' recovery procedure, then delete this CR and create a new one", err))
	}
	if err := r.Update(ctx, controllerSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update controller secret after cleanup: %w", err)
	}

	now := metav1.Now()
	deleteAt := now.Add(r.ttl())
	cr.Succeed(fmt.Sprintf(
		"Rotation completed. This object will be deleted automatically around %s.", deleteAt.UTC().Format(time.RFC3339)), now)
	if err := r.updateStatus(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status to Succeeded: %w", err)
	}

	log.Info("rotation completed", "rotationID", cr.Status.RotationID)
	mocoevent.RotationCompleted.Emit(cr, r.Recorder, cr.Status.RotationID)

	return ctrl.Result{RequeueAfter: r.ttl()}, nil
}

// handleSucceeded deletes the CR once the TTL after completion has passed.
// The delete carries a UID precondition so a delayed deletion can never
// remove a new CR created under the same name after a manual delete.
func (r *CredentialRotationReconciler) handleSucceeded(ctx context.Context, cr *mocov1beta2.CredentialRotation) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if cr.Status.CompletionTime == nil {
		// Should not happen (Succeed always sets it); repair so the TTL
		// can fire instead of never deleting the object.
		now := metav1.Now()
		cr.Status.CompletionTime = &now
		if err := r.updateStatus(ctx, cr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to backfill completionTime: %w", err)
		}
		return ctrl.Result{RequeueAfter: r.ttl()}, nil
	}

	remaining := time.Until(cr.Status.CompletionTime.Add(r.ttl()))
	if remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	uid := cr.UID
	if err := r.Delete(ctx, cr, client.Preconditions{UID: &uid}); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
		return ctrl.Result{}, fmt.Errorf("failed to delete the completed CredentialRotation: %w", err)
	}
	log.Info("deleted the completed CredentialRotation after the TTL")
	return ctrl.Result{}, nil
}

// markFailed moves the CR to the terminal Failed phase and persists it.
func (r *CredentialRotationReconciler) markFailed(ctx context.Context, cr *mocov1beta2.CredentialRotation, message string) error {
	log := crlog.FromContext(ctx)

	cr.Fail(message, metav1.Now())
	if err := r.updateStatus(ctx, cr); err != nil {
		return fmt.Errorf("failed to update status to Failed: %w", err)
	}
	log.Info("rotation failed", "message", message)
	return nil
}

// fail is markFailed plus the RotationFailed Warning Event. The stale
// branch uses markFailed directly: it emits StaleCredentialRotation
// instead.
func (r *CredentialRotationReconciler) fail(ctx context.Context, cr *mocov1beta2.CredentialRotation, message string) (ctrl.Result, error) {
	if err := r.markFailed(ctx, cr, message); err != nil {
		return ctrl.Result{}, err
	}
	mocoevent.RotationFailed.Emit(cr, r.Recorder, message)
	return ctrl.Result{}, nil
}

// surfacePause records a stop-annotation pause: it emits the RotationPaused
// Warning Event and mirrors the reason into status.message (Events expire
// after about an hour; the message is the durable signal). The phase keeps
// its current value — a pause is not Blocked. The message write is skipped
// when it would be a no-op.
func (r *CredentialRotationReconciler) surfacePause(ctx context.Context, cr *mocov1beta2.CredentialRotation, reason, resume string) {
	log := crlog.FromContext(ctx)

	log.Info("rotation is paused", "reason", reason, "rotationID", cr.Status.RotationID)
	mocoevent.RotationPaused.Emit(cr, r.Recorder, reason)
	msg := "Paused: " + reason + ". The rotation resumes automatically when " + resume + "."
	if cr.Status.Message == msg {
		return
	}
	cr.Status.Message = msg
	if err := r.updateStatus(ctx, cr); err != nil {
		// Best-effort: the pause is also visible through the Event and
		// the log; the next requeue retries the message write.
		log.Error(err, "failed to record the pause in status.message")
	}
}

// distributionCaughtUp reports whether the per-namespace user Secret and
// my.cnf Secret are derived from the controller Secret's current
// passwords. It never writes — MySQLClusterReconciler is the only writer
// of the per-namespace Secrets, and its watch on the controller Secret
// triggers redistribution promptly after promotion.
func (r *CredentialRotationReconciler) distributionCaughtUp(ctx context.Context, cluster *mocov1beta2.MySQLCluster) (bool, error) {
	controllerSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: r.SystemNamespace,
		Name:      cluster.ControllerSecretName(),
	}, controllerSecret); err != nil {
		return false, fmt.Errorf("failed to get controller secret: %w", err)
	}

	// Validate the controller Secret's current keys before any content
	// comparison: CurrentPasswordsMatch reports a missing key as a plain
	// mismatch, which would requeue forever instead of surfacing the
	// corruption. NewMySQLPasswordFromSecret validates only the version
	// annotation; CurrentKeyMap validates that every current key is
	// present.
	passwd, err := password.NewMySQLPasswordFromSecret(controllerSecret)
	if err == nil {
		_, err = password.CurrentKeyMap(controllerSecret)
	}
	if err != nil {
		return false, fmt.Errorf("%w: %w", errCorruptControllerSecret, err)
	}

	userSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.UserSecretName(),
	}, userSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get user secret: %w", err)
	}
	if !password.CurrentPasswordsMatch(controllerSecret, userSecret) {
		return false, nil
	}

	expectedMyCnf := passwd.ToMyCnfSecret()
	mycnfSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.MyCnfSecretName(),
	}, mycnfSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get my.cnf secret: %w", err)
	}
	for key, expected := range expectedMyCnf.Data {
		actual, ok := mycnfSecret.Data[key]
		if !ok || string(actual) != string(expected) {
			return false, nil
		}
	}
	return true, nil
}

// isStatefulSetRolloutComplete reports whether the StatefulSet has fully
// rolled out the latest revision (mirrors the helper in clustering/).
func isStatefulSetRolloutComplete(sts *appsv1.StatefulSet) bool {
	if sts.Status.ObservedGeneration < sts.Generation {
		return false
	}
	if sts.Status.CurrentRevision != sts.Status.UpdateRevision {
		return false
	}
	if sts.Spec.Replicas != nil && sts.Status.Replicas != *sts.Spec.Replicas {
		return false
	}
	if sts.Spec.Replicas != nil && sts.Status.UpdatedReplicas != *sts.Spec.Replicas {
		return false
	}
	if sts.Spec.Replicas != nil && sts.Status.ReadyReplicas != *sts.Spec.Replicas {
		return false
	}
	return true
}

// allRotationPhases lists the phase values the metric exports. Keep in
// sync with the RotationPhase constants.
var allRotationPhases = []mocov1beta2.RotationPhase{
	mocov1beta2.PhaseApplyingRetain,
	mocov1beta2.PhasePromoting,
	mocov1beta2.PhaseAwaitingRollout,
	mocov1beta2.PhaseAwaitingDiscard,
	mocov1beta2.PhaseApplyingDiscard,
	mocov1beta2.PhaseFinalizing,
	mocov1beta2.PhaseBlocked,
	mocov1beta2.PhaseSucceeded,
	mocov1beta2.PhaseFailed,
}

// updateRotationMetrics exports the CR's phase (1 for the current value, 0
// for the others) and, on success, the completion timestamp. Metrics may
// be unregistered in tests; guard against nil vectors.
func updateRotationMetrics(cr *mocov1beta2.CredentialRotation) {
	if metrics.CredentialRotationPhaseVec == nil {
		return
	}
	for _, ph := range allRotationPhases {
		v := 0.0
		if cr.Status.Phase == ph {
			v = 1.0
		}
		metrics.CredentialRotationPhaseVec.WithLabelValues(cr.Name, cr.Namespace, string(ph)).Set(v)
	}
	if cr.Status.Phase == mocov1beta2.PhaseSucceeded && cr.Status.CompletionTime != nil {
		metrics.CredentialRotationCompletedTimestampVec.WithLabelValues(cr.Name, cr.Namespace).
			Set(float64(cr.Status.CompletionTime.Unix()))
	}
}

func deleteRotationPhaseMetrics(name, namespace string) {
	if metrics.CredentialRotationPhaseVec == nil {
		return
	}
	metrics.CredentialRotationPhaseVec.DeletePartialMatch(prometheus.Labels{
		"name":      name,
		"namespace": namespace,
	})
}

func deleteRotationCompletedMetric(name, namespace string) {
	if metrics.CredentialRotationCompletedTimestampVec == nil {
		return
	}
	metrics.CredentialRotationCompletedTimestampVec.DeleteLabelValues(name, namespace)
}

// updateStatus stamps the current metadata.generation into
// status.observedGeneration before sending the Status().Update so that
// external tooling (kstatus, ArgoCD, Flux) can detect that the controller
// has caught up with the latest spec change.
func (r *CredentialRotationReconciler) updateStatus(ctx context.Context, cr *mocov1beta2.CredentialRotation) error {
	cr.StampObservedGeneration()
	return r.Status().Update(ctx, cr)
}

func hasOwnerReference(cr *mocov1beta2.CredentialRotation, cluster *mocov1beta2.MySQLCluster) bool {
	for _, ref := range cr.OwnerReferences {
		if ref.UID == cluster.UID {
			return true
		}
	}
	return false
}

func (r *CredentialRotationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mocov1beta2.CredentialRotation{}).
		// Wake the reconciler when the target MySQLCluster changes — most
		// importantly when Spec.Replicas transitions to or from 0 or
		// Spec.Offline flips, so a Blocked cycle resumes immediately
		// instead of waiting for the periodic requeue.
		Watches(
			&mocov1beta2.MySQLCluster{},
			handler.EnqueueRequestsFromMapFunc(mapClusterToCR),
			builder.WithPredicates(clusterReplicasChangedPredicate{}),
		).
		// Wake the reconciler when the controller Secret in the system
		// namespace changes, so promotion-related Secret changes are
		// picked up without waiting for the periodic requeue.
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapControllerSecretToCR),
			builder.WithPredicates(controllerSecretPredicate(r.SystemNamespace)),
		).
		// Wake the reconciler when a per-namespace user Secret changes —
		// handleAwaitingRollout waits for MySQLClusterReconciler to
		// distribute the promoted passwords, and this watch lets it
		// observe the catch-up without waiting for the periodic requeue.
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapUserSecretToCR),
		).
		// Wake the reconciler when the cluster's StatefulSet changes —
		// handleAwaitingRollout waits for the rolling restart to settle,
		// and this watch lets it open the verification window as soon as
		// the rollout completes.
		Watches(
			&appsv1.StatefulSet{},
			handler.EnqueueRequestsFromMapFunc(mapStatefulSetToCR),
		).
		WithOptions(
			controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles},
		).
		Complete(r)
}

// mapClusterToCR maps a MySQLCluster event to a CredentialRotation
// reconcile request. A CredentialRotation always shares its
// namespace/name with the cluster it manages.
func mapClusterToCR(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}}
}

// mapControllerSecretToCR reverse-parses a controller-namespace Secret name
// ("mysql-<ns>.<name>") back to the CredentialRotation that may be
// interested in it. Splitting at the first "." is unambiguous: namespace
// names are RFC 1123 DNS labels and can never contain a dot, while any
// dots in the cluster name are preserved on the right-hand side. The
// reverse parse is best-effort: if the name does not match the expected
// pattern, no request is enqueued.
func (r *CredentialRotationReconciler) mapControllerSecretToCR(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() != r.SystemNamespace {
		return nil
	}
	const prefix = "mysql-"
	name := obj.GetName()
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	rest := name[len(prefix):]
	idx := strings.Index(rest, ".")
	if idx <= 0 || idx == len(rest)-1 {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: rest[:idx],
			Name:      rest[idx+1:],
		},
	}}
}

// mapUserSecretToCR maps a per-namespace user Secret ("moco-<cluster>")
// or my.cnf Secret ("moco-my-cnf-<cluster>") to the CredentialRotation of
// the same cluster, so handleAwaitingRollout observes distribution
// catch-up of both Secrets without waiting for the periodic requeue.
// System-namespace Secrets are handled by mapControllerSecretToCR
// instead. Best-effort: a non-matching name enqueues nothing; a false
// positive (another Secret that happens to carry the prefix) only causes
// a cheap no-op reconcile.
func (r *CredentialRotationReconciler) mapUserSecretToCR(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() == r.SystemNamespace {
		return nil
	}
	name := obj.GetName()
	const prefix = "moco-"
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	// "moco-my-cnf-<x>" is ambiguous: it is the my.cnf Secret of cluster
	// "<x>" but also the user Secret of a cluster literally named
	// "my-cnf-<x>". Enqueue both interpretations rather than guessing —
	// the extra request is a cheap no-op when no such CR exists.
	reqs := []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      name[len(prefix):],
		},
	}}
	const mycnfPrefix = "moco-my-cnf-"
	if strings.HasPrefix(name, mycnfPrefix) {
		reqs = append(reqs, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      name[len(mycnfPrefix):],
			},
		})
	}
	return reqs
}

// mapStatefulSetToCR maps a cluster StatefulSet ("moco-<cluster>") to the
// CredentialRotation of the same cluster.
func mapStatefulSetToCR(_ context.Context, obj client.Object) []reconcile.Request {
	const prefix = "moco-"
	name := obj.GetName()
	if !strings.HasPrefix(name, prefix) {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      name[len(prefix):],
		},
	}}
}

// clusterReplicasChangedPredicate filters MySQLCluster events down to
// those that may unblock or refresh a rotation: creation, deletion, or
// changes of Spec.Replicas or Spec.Offline.
type clusterReplicasChangedPredicate struct {
	predicate.Funcs
}

func (clusterReplicasChangedPredicate) Update(e event.UpdateEvent) bool {
	oldCluster, ok := e.ObjectOld.(*mocov1beta2.MySQLCluster)
	if !ok {
		return false
	}
	newCluster, ok := e.ObjectNew.(*mocov1beta2.MySQLCluster)
	if !ok {
		return false
	}
	if oldCluster.Spec.Replicas != newCluster.Spec.Replicas {
		return true
	}
	// An offline flip blocks or resumes a rotation exactly like a replicas
	// change, so it must prompt a reconcile just as promptly.
	if oldCluster.Spec.Offline != newCluster.Spec.Offline {
		return true
	}
	return (oldCluster.DeletionTimestamp == nil) != (newCluster.DeletionTimestamp == nil)
}

func (clusterReplicasChangedPredicate) Generic(_ event.GenericEvent) bool { return false }

// controllerSecretPredicate filters Secret events to only those that live in
// the controller's system namespace. This avoids reconciling for every
// secret change cluster-wide.
func controllerSecretPredicate(systemNamespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == systemNamespace
	})
}
