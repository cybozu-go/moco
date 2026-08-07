package v1beta2

import (
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestIsTerminal(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		phase RotationPhase
		want  bool
	}{
		{"", false},
		{PhaseApplyingRetain, false},
		{PhasePromoting, false},
		{PhaseAwaitingRollout, false},
		{PhaseAwaitingDiscard, false},
		{PhaseApplyingDiscard, false},
		{PhaseFinalizing, false},
		{PhaseBlocked, false},
		{PhaseSucceeded, true},
		{PhaseFailed, true},
	}
	for _, tc := range testCases {
		t.Run(string(tc.phase), func(t *testing.T) {
			cr := &CredentialRotation{}
			cr.Status.Phase = tc.phase
			if got := cr.IsTerminal(); got != tc.want {
				t.Errorf("IsTerminal() with phase %q = %v, want %v", tc.phase, got, tc.want)
			}
		})
	}
}

func TestIsDiscardReady(t *testing.T) {
	t.Parallel()

	cr := &CredentialRotation{}
	if cr.IsDiscardReady() {
		t.Error("IsDiscardReady() must be false with no conditions")
	}
	cr.SetDiscardReady(metav1.ConditionFalse, ReasonPending, "t")
	if cr.IsDiscardReady() {
		t.Error("IsDiscardReady() must be false with DiscardReady=False")
	}
	cr.SetDiscardReady(metav1.ConditionTrue, ReasonRolloutSettled, "t")
	if !cr.IsDiscardReady() {
		t.Error("IsDiscardReady() must be true with DiscardReady=True")
	}
}

func TestInitConditions(t *testing.T) {
	t.Parallel()

	cr := &CredentialRotation{}
	cr.InitConditions()

	for _, tc := range []struct {
		condType string
		status   metav1.ConditionStatus
		reason   string
	}{
		{ConditionDiscardReady, metav1.ConditionFalse, ReasonPending},
		{ConditionDualPassword, metav1.ConditionFalse, ReasonNotRetained},
		{ConditionFinished, metav1.ConditionFalse, ReasonRunning},
	} {
		cond := apimeta.FindStatusCondition(cr.Status.Conditions, tc.condType)
		if cond == nil {
			t.Fatalf("condition %s not initialised", tc.condType)
		}
		if cond.Status != tc.status || cond.Reason != tc.reason {
			t.Errorf("condition %s = (%s, %s), want (%s, %s)", tc.condType, cond.Status, cond.Reason, tc.status, tc.reason)
		}
	}

	// InitConditions must not overwrite existing conditions.
	cr.SetDualPassword(metav1.ConditionTrue, ReasonRetained, "t")
	cr.InitConditions()
	if !apimeta.IsStatusConditionTrue(cr.Status.Conditions, ConditionDualPassword) {
		t.Error("InitConditions must keep an existing DualPassword condition")
	}
}

func TestFailAndSucceed(t *testing.T) {
	t.Parallel()

	now := metav1.Now()

	fail := &CredentialRotation{}
	fail.Fail("broken", now)
	if fail.Status.Phase != PhaseFailed {
		t.Errorf("Fail: phase = %q, want Failed", fail.Status.Phase)
	}
	if fail.Status.Message != "broken" {
		t.Errorf("Fail: message = %q", fail.Status.Message)
	}
	if fail.Status.CompletionTime == nil || !fail.Status.CompletionTime.Equal(&now) {
		t.Error("Fail: completionTime not set")
	}
	cond := apimeta.FindStatusCondition(fail.Status.Conditions, ConditionFinished)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != ReasonFailed {
		t.Errorf("Fail: Finished condition = %+v", cond)
	}
	if !fail.IsTerminal() {
		t.Error("Fail: must be terminal")
	}
	// Fail also seeds the other conditions so a Failed CR always carries a
	// complete condition set.
	if apimeta.FindStatusCondition(fail.Status.Conditions, ConditionDiscardReady) == nil {
		t.Error("Fail: DiscardReady must be seeded")
	}

	ok := &CredentialRotation{}
	ok.Succeed("done", now)
	if ok.Status.Phase != PhaseSucceeded {
		t.Errorf("Succeed: phase = %q, want Succeeded", ok.Status.Phase)
	}
	if ok.Status.CompletionTime == nil {
		t.Error("Succeed: completionTime not set")
	}
	cond = apimeta.FindStatusCondition(ok.Status.Conditions, ConditionFinished)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != ReasonSucceeded {
		t.Errorf("Succeed: Finished condition = %+v", cond)
	}
}

func TestHasStatus(t *testing.T) {
	t.Parallel()

	cr := &CredentialRotation{}
	if cr.HasStatus() {
		t.Error("a fresh CR must not have status")
	}
	cr.Status.Phase = PhaseApplyingRetain
	if !cr.HasStatus() {
		t.Error("a CR with a phase has status")
	}

	cr = &CredentialRotation{}
	cr.Status.RotationID = "b7b7c37a-0000-0000-0000-000000000000"
	if !cr.HasStatus() {
		t.Error("a CR with a rotationID has status")
	}

	cr = &CredentialRotation{}
	cr.InitConditions()
	if !cr.HasStatus() {
		t.Error("a CR with conditions has status")
	}
}

func TestIsStaleFor(t *testing.T) {
	t.Parallel()

	clusterUID := types.UID("11111111-1111-1111-1111-111111111111")
	otherUID := types.UID("22222222-2222-2222-2222-222222222222")
	clusterCreated := metav1.NewTime(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	cluster := &MySQLCluster{}
	cluster.UID = clusterUID
	cluster.CreationTimestamp = clusterCreated

	ownerRef := func(uid types.UID) metav1.OwnerReference {
		return metav1.OwnerReference{
			APIVersion: GroupVersion.String(),
			Kind:       "MySQLCluster",
			Name:       "test",
			UID:        uid,
		}
	}

	testCases := []struct {
		name      string
		refs      []metav1.OwnerReference
		hasStatus bool
		created   metav1.Time
		want      bool
	}{
		{
			name: "matching ownerReference",
			refs: []metav1.OwnerReference{ownerRef(clusterUID)},
			want: false,
		},
		{
			name:      "matching ownerReference with status",
			refs:      []metav1.OwnerReference{ownerRef(clusterUID)},
			hasStatus: true,
			want:      false,
		},
		{
			name: "mismatching ownerReference",
			refs: []metav1.OwnerReference{ownerRef(otherUID)},
			want: true,
		},
		{
			name: "no ownerReference, empty status (fresh)",
			want: false,
		},
		{
			name:      "no ownerReference, non-empty status (orphan)",
			hasStatus: true,
			want:      true,
		},
		{
			name: "non-MySQLCluster ownerReference only, empty status",
			refs: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "StatefulSet",
				Name:       "x",
				UID:        otherUID,
			}},
			want: false,
		},
		{
			// The CR predates the live cluster: it was created for a
			// previous incarnation and never reconciled (controller
			// downtime through a delete-and-recreate).
			name:    "no ownerReference, empty status, older than the cluster",
			created: metav1.NewTime(clusterCreated.Add(-time.Minute)),
			want:    true,
		},
		{
			// Same-second creation must stay fresh: the create webhook
			// requires the cluster to exist, so a fresh CR is never
			// strictly older than its cluster.
			name:    "no ownerReference, empty status, same creation time",
			created: clusterCreated,
			want:    false,
		},
		{
			name:    "older than the cluster but matching ownerReference",
			refs:    []metav1.OwnerReference{ownerRef(clusterUID)},
			created: metav1.NewTime(clusterCreated.Add(-time.Minute)),
			want:    false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cr := &CredentialRotation{}
			cr.OwnerReferences = tc.refs
			if tc.created.IsZero() {
				cr.CreationTimestamp = metav1.NewTime(clusterCreated.Add(time.Minute))
			} else {
				cr.CreationTimestamp = tc.created
			}
			if tc.hasStatus {
				cr.Status.Phase = PhaseApplyingRetain
			}
			if got := cr.IsStaleFor(cluster); got != tc.want {
				t.Errorf("IsStaleFor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsConditionFalseWithReason(t *testing.T) {
	t.Parallel()

	cr := &CredentialRotation{}
	if IsConditionFalseWithReason(cr, ConditionDiscardReady, ReasonBlocked) {
		t.Error("must be false when the condition is absent")
	}
	cr.SetDiscardReady(metav1.ConditionFalse, ReasonBlocked, "t")
	if !IsConditionFalseWithReason(cr, ConditionDiscardReady, ReasonBlocked) {
		t.Error("must be true for (False, Blocked)")
	}
	if IsConditionFalseWithReason(cr, ConditionDiscardReady, ReasonPending) {
		t.Error("must be false for a different reason")
	}
	cr.SetDiscardReady(metav1.ConditionTrue, ReasonRolloutSettled, "t")
	if IsConditionFalseWithReason(cr, ConditionDiscardReady, ReasonRolloutSettled) {
		t.Error("must be false when the status is True")
	}
}
