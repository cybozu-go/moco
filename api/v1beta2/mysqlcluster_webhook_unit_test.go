package v1beta2

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestValidateAgainstActiveRotation covers the freeze-rule branches that the
// envtest suite cannot reach: a live cluster can never hold replicas: 0,
// because the create and update webhooks reject it. The fake reader lets the
// test hand the admission an already-bypassed 0-replica cluster.
func TestValidateAgainstActiveRotation(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(AddToScheme(scheme))

	const clusterUID = types.UID("cluster-uid")
	makeCluster := func(replicas int32, offline bool) *MySQLCluster {
		return &MySQLCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: clusterUID},
			Spec:       MySQLClusterSpec{Replicas: replicas, Offline: offline},
		}
	}
	activeCR := &CredentialRotation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: GroupVersion.String(),
				Kind:       "MySQLCluster",
				Name:       "test",
				UID:        clusterUID,
			}},
		},
		Status: CredentialRotationStatus{Phase: PhaseApplyingRetain},
	}

	tests := []struct {
		name       string
		old, new   *MySQLCluster
		wantDenied bool
	}{
		{
			name: "scale-up from 0 replicas is always allowed",
			old:  makeCluster(0, false), new: makeCluster(3, false),
			wantDenied: false,
		},
		{
			name: "scale-up is denied while a rotation is active",
			old:  makeCluster(3, false), new: makeCluster(5, false),
			wantDenied: true,
		},
		{
			name: "setting offline back to false is always allowed",
			old:  makeCluster(3, true), new: makeCluster(3, false),
			wantDenied: false,
		},
		{
			name: "turning offline on is denied while a rotation is active",
			old:  makeCluster(3, false), new: makeCluster(3, true),
			wantDenied: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(activeCR).Build()
			a := &mySQLClusterAdmission{client: reader}
			errs := a.validateAgainstActiveRotation(context.Background(), tt.old, tt.new)
			if denied := len(errs) > 0; denied != tt.wantDenied {
				t.Errorf("validateAgainstActiveRotation() denied=%v, want %v (errs: %v)", denied, tt.wantDenied, errs)
			}
		})
	}
}
