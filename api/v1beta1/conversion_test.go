package v1beta1

import (
	"reflect"
	"testing"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/api/apitesting/roundtrip"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestCompatibility(t *testing.T) {
	t.Run("MySQLCluster v1beta1 => v1beta2 => v1beta1", func(t *testing.T) {
		t.Parallel()

		scheme := newScheme(t)

		var v1beta1Cluster1, v1beta1Cluster2 MySQLCluster
		var v1beta2Cluster mocov1beta2.MySQLCluster

		obj, err := roundtrip.CompatibilityTestObject(scheme, GroupVersion.WithKind("MySQLCluster"), fillFuncs())
		if err != nil {
			t.Fatal(err)
		}
		v1beta1Cluster1 = *obj.(*MySQLCluster)

		if err := scheme.Convert(v1beta1Cluster1.DeepCopy(), &v1beta2Cluster, nil); err != nil {
			t.Fatal(err)
		}
		if err := scheme.Convert(&v1beta2Cluster, &v1beta1Cluster2, nil); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(v1beta1Cluster1, v1beta1Cluster2, cmpopts.EquateEmpty(), cmpopts.IgnoreTypes(metav1.TypeMeta{})); diff != "" {
			t.Fatalf("compatibility error (-want +got):\n%s", diff)
		}
	})

	t.Run("MySQLCluster v1beta2 => v1beta1 => v1beta2", func(t *testing.T) {
		t.Parallel()

		scheme := newScheme(t)

		var v1beta2Cluster1, v1beta2Cluster2 mocov1beta2.MySQLCluster
		var v1beta1Cluster MySQLCluster

		obj, err := roundtrip.CompatibilityTestObject(scheme, mocov1beta2.GroupVersion.WithKind("MySQLCluster"), fillFuncs())
		if err != nil {
			t.Fatal(err)
		}
		v1beta2Cluster1 = *obj.(*mocov1beta2.MySQLCluster)

		if err := scheme.Convert(v1beta2Cluster1.DeepCopy(), &v1beta1Cluster, nil); err != nil {
			t.Fatal(err)
		}
		if err := scheme.Convert(&v1beta1Cluster, &v1beta2Cluster2, nil); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(v1beta2Cluster1, v1beta2Cluster2, cmpopts.EquateEmpty(), cmpopts.IgnoreTypes(metav1.TypeMeta{})); diff != "" {
			t.Fatalf("compatibility error (-want +got):\n%s", diff)
		}
	})

	t.Run("MySQLCluster v1beta1 => v1beta2 ServiceTemplate will be copied", func(t *testing.T) {
		t.Parallel()

		scheme := newScheme(t)

		var v1beta2Cluster mocov1beta2.MySQLCluster
		var v1beta1Cluster MySQLCluster

		obj, err := roundtrip.CompatibilityTestObject(scheme, GroupVersion.WithKind("MySQLCluster"), fillFuncs())
		if err != nil {
			t.Fatal(err)
		}
		v1beta1Cluster = *obj.(*MySQLCluster)

		v1beta1Cluster.Spec.ServiceTemplate = &ServiceTemplate{
			ObjectMeta: ObjectMeta{},
			Spec: (*ServiceSpecApplyConfiguration)(
				corev1ac.ServiceSpec().
					WithPorts(corev1ac.ServicePort().
						WithName("dummy").
						WithPort(8080),
					),
			),
		}

		if err := scheme.Convert(v1beta1Cluster.DeepCopy(), &v1beta2Cluster, nil); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(v1beta2Cluster.Spec.PrimaryServiceTemplate, v1beta2Cluster.Spec.ReplicaServiceTemplate, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("compatibility error case (-want +got):\n%s", diff)
		}
	})

	t.Run("BackupPolicy v1beta1 => v1beta2 => v1beta1", func(t *testing.T) {
		t.Parallel()

		scheme := newScheme(t)

		var oldPolicy1, oldPolicy2 BackupPolicy
		var policy mocov1beta2.BackupPolicy

		obj, err := roundtrip.CompatibilityTestObject(scheme, GroupVersion.WithKind("BackupPolicy"), fillFuncs())
		if err != nil {
			t.Fatal(err)
		}
		oldPolicy1 = *obj.(*BackupPolicy)

		if err := scheme.Convert(oldPolicy1.DeepCopy(), &policy, nil); err != nil {
			t.Fatal(err)
		}
		if err := scheme.Convert(&policy, &oldPolicy2, nil); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(oldPolicy1, oldPolicy2, cmpopts.EquateEmpty(), cmpopts.IgnoreTypes(metav1.TypeMeta{})); diff != "" {
			t.Fatalf("compatibility error (-want +got):\n%s", diff)
		}
	})
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	_ = AddToScheme(scheme)
	_ = mocov1beta2.AddToScheme(scheme)

	return scheme
}

func fillFuncs() map[reflect.Type]roundtrip.FillFunc {
	funcs := map[reflect.Type]roundtrip.FillFunc{}

	// TODO: This is necessary because there is an int field in MySQLCluster.
	// It is not a pointer type, so the value cannot be set.
	var i int
	funcs[reflect.TypeOf(i)] = func(s string, i int, obj interface{}) {}

	// refs: https://github.com/kubernetes/apimachinery/blob/v0.24.3/pkg/api/apitesting/roundtrip/construct.go#L33
	funcs[reflect.TypeOf(&runtime.RawExtension{})] = func(s string, i int, obj interface{}) {
		// generate a raw object in normalized form
		// TODO: test non-normalized round-tripping... YAMLToJSON normalizes and makes exact comparisons fail
		obj.(*runtime.RawExtension).Raw = []byte(`{"apiVersion":"example.com/v1","kind":"CustomType","spec":{"replicas":1},"status":{"available":1}}`)
	}
	funcs[reflect.TypeOf(&metav1.TypeMeta{})] = func(s string, i int, obj interface{}) {
		// APIVersion and Kind are not serialized in all formats (notably protobuf), so clear by default for cross-format checking.
		obj.(*metav1.TypeMeta).APIVersion = ""
		obj.(*metav1.TypeMeta).Kind = ""
	}
	funcs[reflect.TypeOf(&metav1.FieldsV1{})] = func(s string, i int, obj interface{}) {
		obj.(*metav1.FieldsV1).Raw = []byte(`{}`)
	}
	funcs[reflect.TypeOf(&metav1.Time{})] = func(s string, i int, obj interface{}) {
		// use the integer as an offset from the year
		obj.(*metav1.Time).Time = time.Date(2000+i, 1, 1, 1, 1, 1, 0, time.UTC)
	}
	funcs[reflect.TypeOf(&metav1.MicroTime{})] = func(s string, i int, obj interface{}) {
		// use the integer as an offset from the year, and as a microsecond
		obj.(*metav1.MicroTime).Time = time.Date(2000+i, 1, 1, 1, 1, 1, i*int(time.Microsecond), time.UTC)
	}
	funcs[reflect.TypeOf(&intstr.IntOrString{})] = func(s string, i int, obj interface{}) {
		// use the string as a string value
		obj.(*intstr.IntOrString).Type = intstr.String
		obj.(*intstr.IntOrString).StrVal = s + "Value"
	}
	return funcs
}
