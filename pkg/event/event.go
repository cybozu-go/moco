package event

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

type MOCOEvent struct {
	Type    string
	Reason  string
	Message string
}

func (e MOCOEvent) Emit(obj runtime.Object, r record.EventRecorder, args ...any) {
	r.Eventf(obj, e.Type, e.Reason, e.Message, args...)
}

func (e MOCOEvent) ToEvent(ref *corev1.ObjectReference, args ...any) *corev1.Event {
	msg := fmt.Sprintf(e.Message, args...)
	t := metav1.Now()
	namespace := ref.Namespace
	if namespace == "" {
		namespace = metav1.NamespaceDefault
	}
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v.%x", ref.Name, t.UnixNano()),
			Namespace: namespace,
		},
		InvolvedObject: *ref,
		Reason:         e.Reason,
		Message:        msg,
		FirstTimestamp: t,
		LastTimestamp:  t,
		Count:          1,
		Type:           e.Type,
	}
}

var (
	InitCloneSucceeded = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "InitCloned",
		Message: "Clone from an external mysqld succeeded",
	}
	InitCloneFailed = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "InitCloneFailed",
		Message: "Clone from an external mysqld failed: %v",
	}
	SwitchOverSucceeded = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "SwitchOver",
		Message: "The primary was changed to instance %d due to a switchover",
	}
	SwitchOverFailed = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "SwitchOverFailed",
		Message: "The primary could not be changed: %v",
	}
	FailOverSucceeded = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "FailOver",
		Message: "The primary was changed to instance %d due to a failover",
	}
	FailOverFailed = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "FailOverFailed",
		Message: "The primary could not be changed: %v",
	}
	CloneSucceeded = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "Cloned",
		Message: "Clone from the primary succeeded for instance %d",
	}
	CloneFailed = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "CloneFailed",
		Message: "Clone from the primary failed for instance %d: %v",
	}
	SetWritable = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "Writable",
		Message: "The primary became writable",
	}
	BackupCreated = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "BackupCreated",
		Message: "Backup created",
	}
	BackupNoBinlog = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "BackupNoBinlog",
		Message: "Backup created w/o binlog files",
	}
	Restored = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "Restored",
		Message: "Successfully restored data from backup",
	}
)

// Events emitted during credential rotation. Events whose involved object is
// the CredentialRotation are emitted by CredentialRotationReconciler; events
// whose involved object is the MySQLCluster are emitted by the clustering
// manager.
var (
	StaleCredentialRotation = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "StaleCredentialRotation",
		Message: "CredentialRotation is owned by a different MySQLCluster UID than the live cluster; delete this CR before starting a new rotation",
	}
	RotationRefused = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "RotationRefused",
		Message: "Cannot start rotation: MySQLCluster replicas is 0",
	}
	RotationPendingSetFailed = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "RotationPendingError",
		Message: "Failed to set pending passwords: %v. Manual cleanup required: See MOCO documentation for recovery procedures",
	}
	RotationPendingInconsistent = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "RotationPendingError",
		Message: "Pending password state inconsistency: %v. Manual cleanup required: See MOCO documentation for recovery procedures",
	}
	RotationPendingConfirmInconsistent = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "RotationPendingError",
		Message: "Pending password state inconsistency during confirm: %v. Manual cleanup required: See MOCO documentation for recovery procedures",
	}
	RotationStarted = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "RotationStarted",
		Message: "Started rotation cycle (rotationID: %s, generation: %d)",
	}
	MissingRotationPending = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "MissingRotationPending",
		Message: "Pending passwords not found in controller secret for rotationID %s. Manual cleanup required: See MOCO documentation for recovery procedures",
	}
	SecretsDistributed = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "SecretsDistributed",
		Message: "Distributed new passwords and triggered rolling restart (rotationID: %s)",
	}
	AwaitingDiscard = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "AwaitingDiscard",
		Message: "Post-distribute StatefulSet rollout settled; verification window open (rotationID: %s)",
	}
	DiscardRefused = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "DiscardRefused",
		Message: "Cannot start discard: MySQLCluster replicas is 0. Scale the cluster up first.",
	}
	DiscardStarted = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "DiscardStarted",
		Message: "Discard requested; ClusterManager will wait for StatefulSet rollout before DISCARD (rotationID: %s)",
	}
	InconsistentRotationState = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "InconsistentState",
		Message: "No pending passwords found for rotationID %s and controller Secret does not match user Secret. Manual cleanup required: See MOCO documentation for recovery procedures",
	}
	RotationCrashRecovery = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "CrashRecovery",
		Message: "No pending passwords found for rotationID %s; confirmed prior promotion via user Secret match. Proceeding to Completed.",
	}
	RotationCompleted = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "RotationCompleted",
		Message: "Rotation completed (rotationID: %s, rotationGeneration: %d, discardGeneration: %d)",
	}
	RotationBlocked = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "RotationBlocked",
		Message: "Cannot proceed with RETAIN: cluster has 0 replicas. Scale the cluster up first.",
	}
	DualPasswordExists = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "DualPasswordExists",
		Message: "Cannot proceed with RETAIN: instance %d user %s already has a dual password. See MOCO documentation for recovery procedures.",
	}
	RetainApplied = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "RetainApplied",
		Message: "Applied ALTER USER RETAIN for all %d instances (rotationID: %s)",
	}
	DiscardBlocked = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "DiscardBlocked",
		Message: "Cannot proceed with DISCARD: cluster has 0 replicas. Scale the cluster up first.",
	}
	DiscardApplied = MOCOEvent{
		Type:    corev1.EventTypeNormal,
		Reason:  "DiscardApplied",
		Message: "Applied DISCARD OLD PASSWORD and migrated auth plugin to %s for all %d instances (rotationID: %s)",
	}
	PasswordRotationError = MOCOEvent{
		Type:    corev1.EventTypeWarning,
		Reason:  "PasswordRotationError",
		Message: "Password rotation encountered an error: %v",
	}
)
