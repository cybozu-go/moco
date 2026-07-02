package v1beta2

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// withCondition is a small helper for building test CRs.
func withCondition(cr *CredentialRotation, condType string, status metav1.ConditionStatus, reason string) *CredentialRotation {
	cr.Status.Conditions = append(cr.Status.Conditions, metav1.Condition{
		Type:   condType,
		Status: status,
		Reason: reason,
	})
	return cr
}

// Fixture constructors mirror the steady-state condition triples that
// the controllers leave the CR in for each Step. Inlined here instead
// of importing controller helpers because v1beta2 must not depend on
// the controller package. ApplyingRetain and Finalizing collapse to
// the same triple (F/F/F); DistributingPassword and ApplyingDiscard
// collapse to the same triple (F/F/T) — the spec/status generation
// fields disambiguate which Step the CR is in.
func crWith(spec CredentialRotationSpec, status CredentialRotationStatus, rotStatus, discStatus, dpStatus metav1.ConditionStatus, rotReason, discReason, dpReason string) *CredentialRotation {
	cr := &CredentialRotation{Spec: spec, Status: status}
	withCondition(cr, ConditionRotationReady, rotStatus, rotReason)
	withCondition(cr, ConditionDiscardReady, discStatus, discReason)
	withCondition(cr, ConditionDualPassword, dpStatus, dpReason)
	return cr
}

func crIdle(spec CredentialRotationSpec, status CredentialRotationStatus) *CredentialRotation {
	return crWith(spec, status,
		metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionFalse,
		ReasonReconciled, ReasonPending, ReasonNotRetained)
}

func crAwaitingDiscard(spec CredentialRotationSpec, status CredentialRotationStatus) *CredentialRotation {
	return crWith(spec, status,
		metav1.ConditionFalse, metav1.ConditionTrue, metav1.ConditionTrue,
		ReasonPending, ReasonReconciled, ReasonRetained)
}

// crCycleNoDualPw covers Step=ApplyingRetain and Step=Finalizing — both
// project to RotationReady=False/Pending, DiscardReady=False/Pending,
// DualPassword=False/NotRetained.
func crCycleNoDualPw(spec CredentialRotationSpec, status CredentialRotationStatus) *CredentialRotation {
	return crWith(spec, status,
		metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse,
		ReasonPending, ReasonPending, ReasonNotRetained)
}

// crCycleDualPw covers Step=DistributingPassword and Step=ApplyingDiscard
// — both project to RotationReady=False/Pending, DiscardReady=False/Pending,
// DualPassword=True/Retained.
func crCycleDualPw(spec CredentialRotationSpec, status CredentialRotationStatus) *CredentialRotation {
	return crWith(spec, status,
		metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionTrue,
		ReasonPending, ReasonPending, ReasonRetained)
}

func TestStep(t *testing.T) {
	cases := []struct {
		name string
		cr   *CredentialRotation
		want RotationStep
	}{
		{
			name: "fresh CR with no conditions is Idle",
			cr: &CredentialRotation{
				Spec: CredentialRotationSpec{RotationGeneration: 1},
			},
			want: StepIdle,
		},
		{
			name: "first cycle: RETAIN in flight (RotationReady=False/Pending, DualPassword=False)",
			cr: crCycleNoDualPw(
				CredentialRotationSpec{RotationGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 0, ObservedDiscardGeneration: 0},
			),
			want: StepApplyingRetain,
		},
		{
			name: "first cycle: RETAIN done (DualPassword=True) is DistributingPassword",
			cr: crCycleDualPw(
				CredentialRotationSpec{RotationGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 0, ObservedDiscardGeneration: 0},
			),
			want: StepDistributingPassword,
		},
		{
			name: "cycle 1 fully completed, no spec change is Idle",
			cr: crIdle(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 1},
			),
			want: StepIdle,
		},
		{
			// Regression: cycle 2 must NOT jump straight to ApplyingRetain
			// just because newRotation=true && DualPassword=False. The
			// RotationReady=True from cycle 1 is stale; handleStartRotation
			// has not yet seeded pending passwords.
			name: "cycle 2: rotationGeneration bumped after completion is Idle (regression)",
			cr: crIdle(
				CredentialRotationSpec{RotationGeneration: 2, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 1},
			),
			want: StepIdle,
		},
		{
			name: "cycle 2: after handleStartRotation seeded the cycle is ApplyingRetain",
			cr: crCycleNoDualPw(
				CredentialRotationSpec{RotationGeneration: 2, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 1},
			),
			want: StepApplyingRetain,
		},
		{
			// Post-distribute, pre-rollout-complete: generations match
			// for rotation (observed bumped in handleDistributingPassword)
			// but DiscardReady has not yet been flipped to True.
			name: "post-distribute pre-rollout-complete (DualPassword=True, DiscardReady=False) is AwaitingRollout",
			cr: crCycleDualPw(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 0},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: StepAwaitingRollout,
		},
		{
			name: "awaiting discard (DiscardReady=True/Reconciled, DualPassword=True)",
			cr: crAwaitingDiscard(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 0},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: StepAwaitingDiscard,
		},
		{
			// Upgrade compatibility: a CR written by the previous
			// "generation tracking" controller could carry both
			// RotationReady=True and DiscardReady=True while holding a
			// dual-password set (awaiting-discard window). The new
			// controller must still classify it as AwaitingDiscard so
			// the webhook allows the operator to bump discardGeneration.
			name: "legacy awaiting-discard (RotationReady=True, DiscardReady=True, DualPassword=True) maps to AwaitingDiscard",
			cr: crWith(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 0},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
				metav1.ConditionTrue, metav1.ConditionTrue, metav1.ConditionTrue,
				ReasonReconciled, ReasonReconciled, ReasonRetained),
			want: StepAwaitingDiscard,
		},
		{
			// Regression: discardGeneration bumped from AwaitingDiscard
			// must transition to ApplyingDiscard so handleApplyingDiscard
			// can flip DiscardReady to Pending.
			name: "discardGeneration bumped from AwaitingDiscard is ApplyingDiscard (stale DiscardReady=True OK)",
			cr: crAwaitingDiscard(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: StepApplyingDiscard,
		},
		{
			name: "discard in flight (DiscardReady=False/Pending, DualPassword=True)",
			cr: crCycleDualPw(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: StepApplyingDiscard,
		},
		{
			name: "DISCARD finished but observedDiscardGeneration not yet promoted is Finalizing",
			cr: crCycleNoDualPw(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: StepFinalizing,
		},
		{
			name: "rotation Refused (replicas=0 at start) is RotationRefused",
			cr: crWith(
				CredentialRotationSpec{RotationGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 0, ObservedDiscardGeneration: 0},
				metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse,
				ReasonRefused, ReasonPending, ReasonNotRetained),
			want: StepRotationRefused,
		},
		{
			name: "rotation Blocked (replicas=0 mid-RETAIN) is RotationBlocked",
			cr: crWith(
				CredentialRotationSpec{RotationGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 0, ObservedDiscardGeneration: 0},
				metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse,
				ReasonBlocked, ReasonPending, ReasonNotRetained),
			want: StepRotationBlocked,
		},
		{
			name: "discard Refused (replicas=0 when discard requested) is DiscardRefused",
			cr: crWith(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
				metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionTrue,
				ReasonPending, ReasonRefused, ReasonRetained),
			want: StepDiscardRefused,
		},
		{
			name: "discard Blocked (replicas=0 mid-DISCARD) is DiscardBlocked",
			cr: crWith(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
				metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionTrue,
				ReasonPending, ReasonBlocked, ReasonRetained),
			want: StepDiscardBlocked,
		},
		{
			name: "rotation Stale takes priority over generation comparison",
			cr: crWith(
				CredentialRotationSpec{RotationGeneration: 2, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 1},
				metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse,
				ReasonStale, ReasonPending, ReasonNotRetained),
			want: StepStalePending,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cr.Step()
			if got != tc.want {
				t.Errorf("Step() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsIdle(t *testing.T) {
	cases := []struct {
		name string
		cr   *CredentialRotation
		want bool
	}{
		{
			name: "fresh CR is idle",
			cr:   &CredentialRotation{Spec: CredentialRotationSpec{RotationGeneration: 1}},
			want: true,
		},
		{
			name: "cycle 1 fully completed is idle",
			cr: crIdle(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 1},
			),
			want: true,
		},
		{
			name: "cycle 2 bump observed before first reconcile is idle",
			cr: crIdle(
				CredentialRotationSpec{RotationGeneration: 2, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 1},
			),
			want: true,
		},
		{
			name: "rotation Refused is idle (no mutations to recover from)",
			cr: crWith(
				CredentialRotationSpec{RotationGeneration: 1},
				CredentialRotationStatus{},
				metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse,
				ReasonRefused, ReasonPending, ReasonNotRetained),
			want: true,
		},
		{
			name: "RETAIN in flight is not idle",
			cr: crCycleNoDualPw(
				CredentialRotationSpec{RotationGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 0, ObservedDiscardGeneration: 0},
			),
			want: false,
		},
		{
			name: "awaiting discard is not idle",
			cr: crAwaitingDiscard(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 0},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cr.IsIdle()
			if got != tc.want {
				t.Errorf("IsIdle() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsAwaitingDiscard(t *testing.T) {
	cases := []struct {
		name string
		cr   *CredentialRotation
		want bool
	}{
		{
			name: "awaiting discard steady state",
			cr: crAwaitingDiscard(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 0},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: true,
		},
		{
			name: "idle is not awaiting discard",
			cr: crIdle(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 1},
			),
			want: false,
		},
		{
			name: "discard in flight is not awaiting discard",
			cr: crCycleDualPw(
				CredentialRotationSpec{RotationGeneration: 1, DiscardGeneration: 1},
				CredentialRotationStatus{ObservedRotationGeneration: 1, ObservedDiscardGeneration: 0},
			),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cr.IsAwaitingDiscard()
			if got != tc.want {
				t.Errorf("IsAwaitingDiscard() = %v, want %v", got, tc.want)
			}
		})
	}
}
