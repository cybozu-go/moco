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
	return fmt.Sprintf("moco-%s", cluster.Name)
}

// GetPodName returns the pod name in the cluster.
func GetPodName(clusterName string, index int) string {
	return fmt.Sprintf("moco-%s-%d", clusterName, index)
}

// GetRootPasswordSecretName returns the name of the root password secret.
func GetRootPasswordSecretName(clusterName string) string {
	return fmt.Sprintf("moco-root-password-%s", clusterName)
}

// GetMyCnfSecretName returns the name of the myCnf secret.
func GetMyCnfSecretName(clusterName string) string {
	return fmt.Sprintf("moco-my-cnf-%s", clusterName)
}

// GetServiceAccountName returns the name of service account for mysql pod.
func GetServiceAccountName(clusterName string) string {
	return fmt.Sprintf("moco-mysqld-sa-%s", clusterName)
}

// GetHost returns host url of the given cluster and instance
func GetHost(cluster *mocov1alpha1.MySQLCluster, index int) string {
	podName := GetPodName(cluster.Name, index)
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
