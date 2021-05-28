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

func (e MOCOEvent) Emit(obj runtime.Object, r record.EventRecorder, args ...interface{}) {
	r.Eventf(obj, e.Type, e.Reason, e.Message, args...)
}

func (e MOCOEvent) ToEvent(ref *corev1.ObjectReference, args ...interface{}) *corev1.Event {
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
