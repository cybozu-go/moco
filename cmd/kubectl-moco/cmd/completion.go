package cmd

import (
	"context"
	"strings"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func mysqlClusterCandidates(ctx context.Context, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var namespace string

	if cmd.Flags().Changed("namespace") {
		ns, err := cmd.Flags().GetString("namespace")
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		namespace = ns
	} else {
		ns, _, err := kubeConfigFlags.ToRawKubeConfigLoader().Namespace()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		namespace = ns
	}

	clusters := &mocov1beta2.MySQLClusterList{}
	if err := kubeClient.List(ctx, clusters, &client.ListOptions{
		Namespace: namespace,
	}); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var candidates []string
	for _, c := range clusters.Items {
		if !strings.HasPrefix(c.Name, toComplete) {
			continue
		}
		candidates = append(candidates, c.Name)
	}

	return candidates, cobra.ShellCompDirectiveNoFileComp
}
