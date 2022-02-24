package v1beta1

import (
	"testing"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	fuzz "github.com/google/gofuzz"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/apitesting/roundtrip"
	"k8s.io/apimachinery/pkg/runtime"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

func TestCompatibility(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = AddToScheme(scheme)
	_ = mocov1beta2.AddToScheme(scheme)

	// Suppress typed nil.
	fn := func(l **corev1.ResourceList, c fuzz.Continue) {
		if l != nil && *l == nil || **l == nil {
			*l = nil
		}
	}

	f := roundtrip.CompatibilityTestFuzzer(scheme, []interface{}{fn})
	f.NilChance(0.5).NumElements(0, 3)

	t.Run("MySQLCluster v1beta1 => v1beta2 => v1beta1", func(t *testing.T) {
		for i := 0; i < 10000; i++ {
			var v1beta1Cluster1, v1beta1Cluster2 MySQLCluster
			var v1beta2Cluster mocov1beta2.MySQLCluster
			f.Fuzz(&v1beta1Cluster1)

			var tmp1, tmp2 mocov1beta2.MySQLCluster

			if err := scheme.Convert(v1beta1Cluster1.DeepCopy(), &tmp1, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp1, &v1beta2Cluster, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&v1beta2Cluster, &tmp2, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp2, &v1beta1Cluster2, nil); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(v1beta1Cluster1, v1beta1Cluster2, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("compatibility error case #%d (-want +got):\n%s", i, diff)
			}
		}
	})

	t.Run("MySQLCluster v1beta2 => v1beta1 => v1beta2", func(t *testing.T) {
		for i := 0; i < 10000; i++ {
			var v1beta2Cluster1, v1beta2Cluster2 mocov1beta2.MySQLCluster
			var v1beta1Cluster MySQLCluster
			f.Fuzz(&v1beta2Cluster1)

			var tmp1, tmp2 MySQLCluster

			if err := scheme.Convert(v1beta2Cluster1.DeepCopy(), &tmp1, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp1, &v1beta1Cluster, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&v1beta1Cluster, &tmp2, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp2, &v1beta2Cluster2, nil); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(v1beta2Cluster1, v1beta2Cluster2, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("compatibility error case #%d (-want +got):\n%s", i, diff)
			}
		}
	})

	t.Run("MySQLCluster v1beta1 => v1beta2 ServiceTemplate will be copied", func(t *testing.T) {
		var v1beta2Cluster mocov1beta2.MySQLCluster
		var v1beta1Cluster MySQLCluster
		f.Fuzz(&v1beta1Cluster)

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

		if err := scheme.Convert(&v1beta1Cluster, &v1beta2Cluster, nil); err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(v1beta2Cluster.Spec.PrimaryServiceTemplate, v1beta2Cluster.Spec.ReplicaServiceTemplate, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("compatibility error case (-want +got):\n%s", diff)
		}
	})

	t.Run("BackupPolicy v1beta1 => v1beta2 => v1beta1", func(t *testing.T) {
		for i := 0; i < 10000; i++ {
			var oldPolicy1, oldPolicy2 BackupPolicy
			var policy mocov1beta2.BackupPolicy
			f.Fuzz(&oldPolicy1)

			var tmp1, tmp2 mocov1beta2.BackupPolicy

			if err := scheme.Convert(oldPolicy1.DeepCopy(), &tmp1, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp1, &policy, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&policy, &tmp2, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp2, &oldPolicy2, nil); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(oldPolicy1, oldPolicy2, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("compatibility error case #%d (-want +got):\n%s", i, diff)
			}
		}
	})
}
