package v1beta2

import (
	"context"
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func SetupStatefulSetWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&appsv1.StatefulSet{}).
		WithDefaulter(&StatefulSetDefaulter{}).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-apps-v1-statefulset,mutating=true,failurePolicy=fail,sideEffects=None,groups=apps,resources=statefulsets,verbs=update,versions=v1,name=statefulset.kb.io,admissionReviewVersions=v1

type StatefulSetDefaulter struct{}

var _ admission.CustomDefaulter = &StatefulSetDefaulter{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (*StatefulSetDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	sts, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		return fmt.Errorf("unknown obj type %T", obj)
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get admission request from context: %w", err)
	}

	if req.Operation != admissionv1.Update {
		return nil
	}

	if len(sts.OwnerReferences) != 1 {
		return nil
	}

	if sts.OwnerReferences[0].Kind != "MySQLCluster" && sts.OwnerReferences[0].APIVersion != GroupVersion.String() {
		return nil
	}

	if sts.Annotations[constants.AnnForceRollingUpdate] == "true" {
		sts.Spec.UpdateStrategy.RollingUpdate = nil
		return nil
	}

	if sts.Spec.UpdateStrategy.RollingUpdate == nil || sts.Spec.UpdateStrategy.RollingUpdate.Partition == nil {
		sts.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{
			Partition: ptr.To[int32](*sts.Spec.Replicas),
		}
		return nil
	}

	oldSts, err := readStatefulSet(req.OldObject.Raw)
	if err != nil {
		return fmt.Errorf("failed to read old statefulset: %w", err)
	}

	partition := *sts.Spec.UpdateStrategy.RollingUpdate.Partition
	oldPartition := *oldSts.Spec.UpdateStrategy.RollingUpdate.Partition

	newSts := sts.DeepCopy()
	newSts.Spec.UpdateStrategy = oldSts.Spec.UpdateStrategy

	if partition != oldPartition && equality.Semantic.DeepEqual(newSts.Spec, oldSts.Spec) {
		return nil
	}

	sts.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{
		Partition: ptr.To[int32](*sts.Spec.Replicas),
	}

	return nil
}

func readStatefulSet(raw []byte) (*appsv1.StatefulSet, error) {
	var sts appsv1.StatefulSet

	if _, _, err := unstructured.UnstructuredJSONScheme.Decode(raw, nil, &sts); err != nil {
		return nil, err
	}

	sts.TypeMeta.APIVersion = appsv1.SchemeGroupVersion.Group + "/" + appsv1.SchemeGroupVersion.Version

	return &sts, nil
}
