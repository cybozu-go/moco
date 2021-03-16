package event

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

type MOCOEvent struct {
	Type    string
	Reason  string
	Message string
}

func (e MOCOEvent) FillVariables(val ...interface{}) *MOCOEvent {
	e.Message = fmt.Sprintf(e.Message, val...)
	return &e
}

var (
	EventInitializationSucceeded = MOCOEvent{
		corev1.EventTypeNormal,
		"Initialization Succeeded",
		"Initialization phase finished successfully.",
	}
	EventInitializationFailed = MOCOEvent{
		corev1.EventTypeWarning,
		"Initialization Failed",
		"Initialization phase failed. err=%s",
	}
	EventWaitingAllInstancesAvailable = MOCOEvent{
		corev1.EventTypeNormal,
		"Waiting All Instances Available",
		"Waiting for all instances to become connected from MOCO. unavailable=%v",
	}
	EventViolationOccurred = MOCOEvent{
		corev1.EventTypeWarning,
		"Violation Occurred",
		"Constraint violation occurred. Please resolve via manual operation. err=%v",
	}
	EventWatingRelayLogExecution = MOCOEvent{
		corev1.EventTypeNormal,
		"Waiting Relay Log Execution",
		"Waiting relay log execution on replica instance(s).",
	}
	EventWaitingCloneFromExternal = MOCOEvent{
		corev1.EventTypeNormal,
		"Waiting External Clone",
		"Waiting for the intermediate primary to clone from the external primary",
	}
	EventRestoringReplicaInstances = MOCOEvent{
		corev1.EventTypeNormal,
		"Restoring Replica Instance(s)",
		"Restoring replica instance(s) by cloning with primary instance.",
	}
	EventPrimaryChanged = MOCOEvent{
		corev1.EventTypeNormal,
		"Primary Changed",
		"Primary instance was changed from %s to %s because of failover or switchover.",
	}
	EventIntermediatePrimaryConfigured = MOCOEvent{
		corev1.EventTypeNormal, "Intermediate Primary Configured",
		"Intermediate primary instance was configured with host=%s",
	}
	EventIntermediatePrimaryUnset = MOCOEvent{
		corev1.EventTypeNormal, "Intermediate Primary Unset",
		"Intermediate primary instance was unset.",
	}
	EventClusteringCompletedSynced = MOCOEvent{
		corev1.EventTypeNormal,
		"Clustering Completed and Synced",
		"Clustering are completed. All instances are synced.",
	}
	EventClusteringCompletedNotSynced = MOCOEvent{
		corev1.EventTypeWarning,
		"Clustering Completed but Not Synced",
		"Clustering are completed. Some instance(s) are not synced. out_of_sync=%v",
	}
)
