package controllers

import (
	"testing"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

func TestHasStaleClusterOwnerRef(t *testing.T) {
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
		name string
		refs []metav1.OwnerReference
		want bool
	}{
		{
			name: "no owner references",
			refs: nil,
			want: false,
		},
		{
			name: "matching UID is not stale",
			refs: []metav1.OwnerReference{
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "test", UID: "new-uid"},
			},
			want: false,
		},
		{
			name: "only different UID is stale",
			refs: []metav1.OwnerReference{
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "test", UID: "old-uid"},
			},
			want: true,
		},
		{
			name: "matching ref alongside stale ref is not stale",
			refs: []metav1.OwnerReference{
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "test", UID: "old-uid"},
				{APIVersion: "moco.cybozu.com/v1beta2", Kind: "MySQLCluster", Name: "test", UID: "new-uid"},
			},
			want: false,
		},
		{
			name: "non-MySQLCluster ref is ignored",
			refs: []metav1.OwnerReference{
				{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "test", UID: "sts-uid"},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cr := &mocov1beta2.CredentialRotation{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: append([]metav1.OwnerReference(nil), tc.refs...),
				},
			}
			got := hasStaleClusterOwnerRef(cr, cluster, sch)
			if got != tc.want {
				t.Errorf("hasStaleClusterOwnerRef = %v, want %v", got, tc.want)
			}
		})
	}
}
