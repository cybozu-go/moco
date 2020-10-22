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
	fromExternal bool
}

// CloneOp returns the CloneOp Operator
func CloneOp(replicaIndex int, fromExternal bool) Operator {
	return &cloneOp{
		replicaIndex: replicaIndex,
		fromExternal: fromExternal,
	}
}

func (o cloneOp) Name() string {
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
		moco.AgentTokenParam: []string{cluster.Status.AgentToken},
	}
	if o.fromExternal {
		queries[moco.CloneParamExternal] = []string{"true"}
	} else {
		queries[moco.CloneParamDonorHostName] = []string{primaryHost}
		queries[moco.CloneParamDonorPort] = []string{strconv.Itoa(moco.MySQLAdminPort)}
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

func (o cloneOp) Describe() string {
	return fmt.Sprintf("%#v", o)
}
