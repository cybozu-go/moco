package operators

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/cybozu-go/well"
)

type cloneOp struct {
	replicaIndex int
}

// CloneOp returns the CloneOp Operator
func CloneOp(replicaIndex int) Operator {
	return &cloneOp{
		replicaIndex: replicaIndex,
	}
}

func (cloneOp) Name() string {
	return OperatorClone
}

func (o cloneOp) Run(ctx context.Context, infra accessor.Infrastructure, cluster *mocov1alpha1.MySQLCluster, status *accessor.MySQLClusterStatus) error {
	primaryHost := moco.GetHost(cluster, *cluster.Status.CurrentPrimaryIndex)
	replicaHost := moco.GetHost(cluster, o.replicaIndex)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("http://%s:%d/clone", replicaHost, moco.AgentPort),
		nil,
	)
	if err != nil {
		return err
	}

	queries := url.Values{
		moco.CloneParamDonorHostName: []string{primaryHost},
		moco.CloneParamDonorPort:     []string{strconv.Itoa(moco.MySQLAdminPort)},
		moco.AgentTokenParam:         []string{cluster.Status.AgentToken},
	}
	req.URL.RawQuery = queries.Encode()

	cli := &well.HTTPClient{Client: &http.Client{}}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to clone: %s", resp.Status)
	}

	return nil
}
