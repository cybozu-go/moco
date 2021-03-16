package operators

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cybozu-go/moco/accessor"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type setRoleLabelsOp struct{}

// SetRoleLabelsOp returns the SetRoleLabelsOp Operator
func SetRoleLabelsOp() Operator {
	return &setRoleLabelsOp{}
}

func (o setRoleLabelsOp) Name() string {
	return OperatorSetRoleLabels
}

func (o setRoleLabelsOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1beta1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	pods := corev1.PodList{}
	err := infra.GetClient().List(ctx, &pods, &client.ListOptions{
		Namespace:     cluster.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{constants.LabelAppInstance: cluster.PrefixedName()}),
	})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		if strings.HasSuffix(pod.Name, strconv.Itoa(*cluster.Status.CurrentPrimaryIndex)) {
			pod.Labels[constants.LabelMocoRole] = constants.RolePrimary
		} else {
			pod.Labels[constants.LabelMocoRole] = constants.RoleReplica
		}

		if err := infra.GetClient().Update(ctx, &pod); err != nil {
			return err
		}
	}

	return nil
}

func (o setRoleLabelsOp) Describe() string {
	return fmt.Sprintf("%#v", o)
}
