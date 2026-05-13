package controllers

import (
	"testing"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

func TestRemoveStaleClusterOwnerReferences(t *testing.T) {
	sch := runtime.NewScheme()
	if err := mocov1beta2.AddToScheme(sch); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	cluster := &mocov1beta2.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "ns",
			UID:       types.UID("new-uid"),
		},
	}

	tests := []struct {
		name        string
		refs        []metav1.OwnerReference
		wantRemoved bool
		wantLen     int
	}{
		{
			name:        "no owner references",
			refs:        nil,
			wantRemoved: false,
			wantLen:     0,
		},
		{
			name: "matching UID is preserved",
			refs: []metav1.OwnerReference{
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "test", UID: "new-uid"},
			},
			wantRemoved: false,
			wantLen:     1,
		},
		{
			name: "stale UID with same name is removed",
			refs: []metav1.OwnerReference{
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "test", UID: "old-uid"},
			},
			wantRemoved: true,
			wantLen:     0,
		},
		{
			name: "different name is preserved",
			refs: []metav1.OwnerReference{
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "other", UID: "old-uid"},
			},
			wantRemoved: false,
			wantLen:     1,
		},
		{
			name: "different kind is preserved",
			refs: []metav1.OwnerReference{
				{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "test", UID: "old-uid"},
			},
			wantRemoved: false,
			wantLen:     1,
		},
		{
			name: "stale removed, unrelated preserved",
			refs: []metav1.OwnerReference{
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "test", UID: "old-uid"},
				{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "test", UID: "sts-uid"},
			},
			wantRemoved: true,
			wantLen:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cr := &mocov1beta2.CredentialRotation{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: append([]metav1.OwnerReference(nil), tc.refs...),
				},
			}
			removed, err := removeStaleClusterOwnerReferences(cr, cluster, sch)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if removed != tc.wantRemoved {
				t.Errorf("removed = %v, want %v", removed, tc.wantRemoved)
			}
			if got := len(cr.OwnerReferences); got != tc.wantLen {
				t.Errorf("len(OwnerReferences) = %d, want %d", got, tc.wantLen)
			}
		})
	}
}
