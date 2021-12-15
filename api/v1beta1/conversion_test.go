package v1beta1_test

import (
	"testing"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/api/apitesting/roundtrip"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestCompatibility(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = mocov1beta1.AddToScheme(scheme)
	_ = mocov1beta2.AddToScheme(scheme)

	f := roundtrip.CompatibilityTestFuzzer(scheme, nil)
	f.NilChance(0.5).NumElements(0, 3)

	t.Run("v1beta1 => v1beta2 => v1beta1", func(t *testing.T) {
		for i := 0; i < 10000; i++ {
			var oldCluster1, oldCluster2 mocov1beta1.MySQLCluster
			var cluster mocov1beta2.MySQLCluster
			f.Fuzz(&oldCluster1)

			var tmp1, tmp2 mocov1beta2.MySQLCluster

			if err := scheme.Convert(oldCluster1.DeepCopy(), &tmp1, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp1, &cluster, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&cluster, &tmp2, nil); err != nil {
				t.Fatal(err)
			}
			if err := scheme.Convert(&tmp2, &oldCluster2, nil); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(oldCluster1, oldCluster2, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("compatibility error case #%d (-want +got):\n%s", i, diff)
			}
		}
	})
}
