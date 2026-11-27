package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
)

func TestSyncPVCValuesWithNilMaps(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	selected := map[string]string{"app": "mysql"}
	// An existing PVC without annotations.
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: "data-moco-test-0", Namespace: "default", Labels: selected,
	}}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "moco-test", Namespace: "default"},
		Spec:       appsv1.StatefulSetSpec{Selector: &metav1.LabelSelector{MatchLabels: selected}},
	}
	template := mocov1beta2.PersistentVolumeClaim{}
	template.Name = "data"
	template.Annotations = map[string]string{"example.com/key": "value"}
	cluster := &mocov1beta2.MySQLCluster{}
	cluster.Spec.VolumeClaimTemplates = []mocov1beta2.PersistentVolumeClaim{template}

	r := &MySQLClusterReconciler{
		Client:                fake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc).Build(),
		PVCSyncAnnotationKeys: []string{"example.com/key"},
	}
	if err := r.syncPVCLabelAndAnnotationValues(context.Background(), cluster, sts); err != nil {
		t.Fatal(err)
	}

	got := &corev1.PersistentVolumeClaim{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(pvc), got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations["example.com/key"] != "value" {
		t.Errorf("annotations = %v, want the synced value", got.Annotations)
	}
}
