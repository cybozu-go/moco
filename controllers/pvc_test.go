package controllers

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/metrics"
	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
)

func TestReconcilePVC(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		arrangeFunc func(*testing.T, client.Client)
		assertFunc  func(*testing.T, client.Client)
		wantMetrics string
	}{
		{
			name:        "resize succeeded",
			clusterName: "mysql-cluster",
			arrangeFunc: func(t *testing.T, c client.Client) {
				cluster := newMySQLClusterWithVolumeSize(resource.MustParse("2Gi"))
				sts := newStatefulSetWithVolumeSize(resource.MustParse("1Gi"))
				pvcs := newPVCsForStatefulSet(sts, nil)
				objects := append([]client.Object{cluster, sts}, pvcs...)
				createObjects(t, c, objects...)
			},
			assertFunc: func(t *testing.T, c client.Client) {
				assertPVC(t, c, "mysql-data-moco-mysql-cluster-0", resource.MustParse("2Gi"), mysqlPVCLabels(), nil)
			},
			wantMetrics: `# HELP moco_cluster_volume_resized_total The number of successful volume resizes
# TYPE moco_cluster_volume_resized_total counter
moco_cluster_volume_resized_total{name="mysql-cluster",namespace="default"} 1
`,
		},
		{
			name:        "PVC without storage request",
			clusterName: "mysql-cluster",
			arrangeFunc: func(t *testing.T, c client.Client) {
				cluster := newMySQLClusterWithVolumeSize(resource.MustParse("2Gi"))
				sts := newStatefulSetWithVolumeSize(resource.MustParse("1Gi"))
				pvcs := newPVCsForStatefulSet(sts, nil)
				pvcs[0].(*corev1.PersistentVolumeClaim).Spec.Resources.Requests = make(corev1.ResourceList)
				objects := append([]client.Object{cluster, sts}, pvcs...)
				createObjects(t, c, objects...)
			},
			assertFunc: func(t *testing.T, c client.Client) {
				assertPVC(t, c, "mysql-data-moco-mysql-cluster-0", resource.MustParse("2Gi"), mysqlPVCLabels(), nil)
			},
			wantMetrics: `# HELP moco_cluster_volume_resized_total The number of successful volume resizes
# TYPE moco_cluster_volume_resized_total counter
moco_cluster_volume_resized_total{name="mysql-cluster",namespace="default"} 1
`,
		},
		{
			name:        "non-expandable StorageClass",
			clusterName: "mysql-cluster",
			arrangeFunc: func(t *testing.T, c client.Client) {
				cluster := newMySQLClusterWithVolumeSize(resource.MustParse("2Gi"))
				sts := newStatefulSetWithVolumeSize(resource.MustParse("1Gi"))
				sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName = new("non-expandable")
				pvcs := newPVCsForStatefulSet(sts, nil)
				objects := append([]client.Object{cluster, sts, newNonExpandableStorageClass()}, pvcs...)
				createObjects(t, c, objects...)
			},
			assertFunc: func(t *testing.T, c client.Client) {
				assertPVC(t, c, "mysql-data-moco-mysql-cluster-0", resource.MustParse("1Gi"), mysqlPVCLabels(), nil)
			},
		},
		{
			name:        "actual PVC is larger than requested size",
			clusterName: "mysql-cluster",
			arrangeFunc: func(t *testing.T, c client.Client) {
				cluster := newMySQLClusterWithVolumeSize(resource.MustParse("300Gi"))
				sts := newStatefulSetWithVolumeSize(resource.MustParse("200Gi"))
				pvcs := newPVCsForStatefulSet(sts, map[string]resource.Quantity{
					"mysql-data-moco-mysql-cluster-0": resource.MustParse("500Gi"),
				})
				objects := append([]client.Object{cluster, sts}, pvcs...)
				createObjects(t, c, objects...)
			},
			assertFunc: func(t *testing.T, c client.Client) {
				assertPVC(t, c, "mysql-data-moco-mysql-cluster-0", resource.MustParse("500Gi"), mysqlPVCLabels(), nil)
			},
		},
		{
			name:        "label synced",
			clusterName: "mysql-cluster",
			arrangeFunc: func(t *testing.T, c client.Client) {
				cluster := newMySQLClusterWithVolumeSize(resource.MustParse("2Gi"))
				cluster.Spec.VolumeClaimTemplates[0].Labels = map[string]string{
					"need-update": "updated",
					"foo":         "updated",
				}
				sts := newStatefulSetWithVolumeSize(resource.MustParse("2Gi"))
				sts.Spec.VolumeClaimTemplates[0].Labels = map[string]string{
					"need-update": "not-updated",
					"foo":         "not-updated",
				}
				pvcs := newPVCsForStatefulSet(sts, nil)
				objects := append([]client.Object{cluster, sts}, pvcs...)
				createObjects(t, c, objects...)
			},
			assertFunc: func(t *testing.T, c client.Client) {
				wantLabels := mysqlPVCLabels()
				wantLabels["need-update"] = "updated"
				wantLabels["foo"] = "not-updated"
				assertPVC(t, c, "mysql-data-moco-mysql-cluster-0", resource.MustParse("2Gi"), wantLabels, nil)
			},
		},
		{
			name:        "annotation synced",
			clusterName: "mysql-cluster",
			arrangeFunc: func(t *testing.T, c client.Client) {
				cluster := newMySQLClusterWithVolumeSize(resource.MustParse("2Gi"))
				cluster.Spec.VolumeClaimTemplates[0].Annotations = map[string]string{
					"need-update": "updated",
					"foo":         "updated",
				}
				sts := newStatefulSetWithVolumeSize(resource.MustParse("2Gi"))
				sts.Spec.VolumeClaimTemplates[0].Annotations = map[string]string{
					"need-update": "not-updated",
					"foo":         "not-updated",
				}
				pvcs := newPVCsForStatefulSet(sts, nil)
				objects := append([]client.Object{cluster, sts}, pvcs...)
				createObjects(t, c, objects...)
			},
			assertFunc: func(t *testing.T, c client.Client) {
				assertPVC(t, c, "mysql-data-moco-mysql-cluster-0", resource.MustParse("2Gi"), mysqlPVCLabels(), map[string]string{
					"need-update": "updated",
					"foo":         "not-updated",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := clientgoscheme.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add scheme: %v", err)
			}
			if err := mocov1beta2.AddToScheme(scheme); err != nil {
				t.Fatalf("failed to add scheme: %v", err)
			}

			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			createObjects(t, c, newExpandableStorageClass())
			tt.arrangeFunc(t, c)

			var cluster mocov1beta2.MySQLCluster
			if err := c.Get(t.Context(), types.NamespacedName{Name: tt.clusterName, Namespace: "default"}, &cluster); err != nil {
				t.Fatalf("failed to get MySQLCluster: %v", err)
			}

			registry := prometheus.NewRegistry()
			metrics.Register(registry)

			r := &MySQLClusterReconciler{
				Client:                c,
				PVCSyncAnnotationKeys: []string{"need-update"},
				PVCSyncLabelKeys:      []string{"need-update"},
			}

			if err := r.reconcilePVC(t.Context(), &cluster); err != nil {
				t.Fatalf("reconcilePVC() error = %v", err)
			}

			tt.assertFunc(t, c)

			if err := testutil.GatherAndCompare(registry, strings.NewReader(tt.wantMetrics), "moco_cluster_volume_resized_total"); err != nil {
				t.Errorf("metrics comparison failed: %v", err)
			}
		})
	}
}

func newPVCsForStatefulSet(sts *appsv1.StatefulSet, pvcSizes map[string]resource.Quantity) []client.Object {
	var pvcs []client.Object

	replicas := ptr.Deref(sts.Spec.Replicas, 1)

	for _, template := range sts.Spec.VolumeClaimTemplates {
		for i := range replicas {
			pvc := template.DeepCopy()
			pvc.Name = fmt.Sprintf("%s-%s-%d", template.Name, sts.Name, i)
			pvc.Namespace = "default"

			labels := make(map[string]string)
			maps.Copy(labels, pvc.Labels)
			maps.Copy(labels, sts.Spec.Selector.MatchLabels)
			pvc.Labels = labels

			if size, ok := pvcSizes[pvc.Name]; ok {
				if pvc.Spec.Resources.Requests == nil {
					pvc.Spec.Resources.Requests = corev1.ResourceList{}
				}
				pvc.Spec.Resources.Requests[corev1.ResourceStorage] = size
			}
			pvcs = append(pvcs, pvc)
		}
	}

	return pvcs
}

func newExpandableStorageClass() *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
		Provisioner:          "kubernetes.io/no-provisioner",
		AllowVolumeExpansion: new(true),
	}
}

func newNonExpandableStorageClass() *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "non-expandable",
		},
		Provisioner:          "kubernetes.io/no-provisioner",
		AllowVolumeExpansion: new(false),
	}
}

func createObjects(t *testing.T, c client.Client, objects ...client.Object) {
	t.Helper()

	for _, object := range objects {
		if err := c.Create(t.Context(), object); err != nil {
			t.Fatalf("failed to create %T %s: %v", object, client.ObjectKeyFromObject(object), err)
		}
	}
}

func assertPVC(t *testing.T, c client.Client, name string, wantSize resource.Quantity, wantLabels, wantAnnotations map[string]string) {
	t.Helper()

	var pvc corev1.PersistentVolumeClaim
	if err := c.Get(t.Context(), types.NamespacedName{Name: name, Namespace: "default"}, &pvc); err != nil {
		t.Fatalf("failed to get PVC %q: %v", name, err)
	}
	if got := pvc.Spec.Resources.Requests.Storage(); !got.Equal(wantSize) {
		t.Errorf("unexpected PVC %q size: got: %s, want: %s", name, got.String(), wantSize.String())
	}
	if diff := cmp.Diff(wantLabels, pvc.Labels); len(diff) != 0 {
		t.Errorf("unexpected PVC %q labels: %s", name, diff)
	}
	if diff := cmp.Diff(wantAnnotations, pvc.Annotations); len(diff) != 0 {
		t.Errorf("unexpected PVC %q annotations: %s", name, diff)
	}
}

func mysqlPVCLabels() map[string]string {
	return map[string]string{
		constants.LabelAppName:      constants.AppNameMySQL,
		constants.LabelAppCreatedBy: constants.AppCreator,
		constants.LabelAppInstance:  "mysql-cluster",
	}
}

func newMySQLClusterWithVolumeSize(size resource.Quantity) *mocov1beta2.MySQLCluster {
	return &mocov1beta2.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mysql-cluster",
			Namespace: "default",
		},
		Spec: mocov1beta2.MySQLClusterSpec{
			VolumeClaimTemplates: []mocov1beta2.PersistentVolumeClaim{
				{
					ObjectMeta: mocov1beta2.ObjectMeta{Name: "mysql-data"},
					Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(*corev1ac.PersistentVolumeClaimSpec().
						WithStorageClassName("default").WithResources(corev1ac.VolumeResourceRequirements().
						WithRequests(corev1.ResourceList{corev1.ResourceStorage: size}),
					)),
				},
			},
		},
	}
}

func newStatefulSetWithVolumeSize(size resource.Quantity) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "moco-mysql-cluster",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: new(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					constants.LabelAppName:      constants.AppNameMySQL,
					constants.LabelAppInstance:  "mysql-cluster",
					constants.LabelAppCreatedBy: constants.AppCreator,
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "mysql-data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						StorageClassName: new("default"),
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceStorage: size},
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 1,
			ReadyReplicas:      1,
		},
	}
}
