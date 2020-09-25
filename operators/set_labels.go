package operators

import (
	"context"
	"strconv"
	"strings"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type setLabelsOp struct{}

// SetLabelsOp returns the SetLabelsOp Operator
func SetLabelsOp() Operator {
	return &setLabelsOp{}
}

func (setLabelsOp) Name() string {
	return moco.OperatorSetLabels
}

func (setLabelsOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	pods := corev1.PodList{}
	err := infra.GetClient().List(ctx, &pods, &client.ListOptions{
		Namespace:     cluster.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{moco.AppNameKey: moco.UniqueName(cluster)}),
	})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		if strings.HasSuffix(pod.Name, strconv.Itoa(*cluster.Status.CurrentPrimaryIndex)) {
			pod.Labels[moco.RoleKey] = moco.PrimaryRole
		} else {
			pod.Labels[moco.RoleKey] = moco.ReplicaRole
		}

		if err := infra.GetClient().Update(ctx, &pod); err != nil {
			return err
		}
	}

	return nil
}
