package moco

import (
	"fmt"
	"os"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
)

// UniqueName returns unique name of the cluster
func UniqueName(cluster *mocov1alpha1.MySQLCluster) string {
	return fmt.Sprintf("%s-%s", cluster.GetName(), cluster.GetUID())
}

// GetHost returns host url of the given cluster and instance
func GetHost(cluster *mocov1alpha1.MySQLCluster, index int) string {
	podName := fmt.Sprintf("%s-%d", UniqueName(cluster), index)
	return fmt.Sprintf("%s.%s.%s.svc", podName, UniqueName(cluster), cluster.Namespace)
}

// GetSecretNameForController returns the namespace and Secret name of the cluster
func GetSecretNameForController(cluster *mocov1alpha1.MySQLCluster) (string, string) {
	myNS := os.Getenv("POD_NAMESPACE")
	mySecretName := cluster.Namespace + "." + cluster.Name // TODO: clarify assumptions for length and charset
	return myNS, mySecretName
}
