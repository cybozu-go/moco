package moco

import (
	"context"
	"fmt"
	"os"

	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// GetPassword gets a password from secret
func GetPassword(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, c client.Client, passwordKey string) (string, error) {
	secret := &corev1.Secret{}
	myNS, mySecretName := GetSecretNameForController(cluster)
	err := c.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err != nil {
		return "", err
	}
	return string(secret.Data[passwordKey]), nil
}
