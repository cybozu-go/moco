package cmd

import (
	"context"
	"errors"
	"fmt"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func getPassword(ctx context.Context, clusterName, user string) (string, error) {
	cluster := &mocov1beta2.MySQLCluster{}
	cluster.Name = clusterName
	cluster.Namespace = namespace
	name := cluster.UserSecretName()
	secret := &corev1.Secret{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, secret); err != nil {
		return "", err
	}

	passwd, err := password.NewMySQLPasswordFromSecret(secret)
	if err != nil {
		return "", err
	}

	switch user {
	case constants.AdminUser:
		return passwd.Admin(), nil
	case constants.ReadOnlyUser:
		return passwd.ReadOnly(), nil
	case constants.WritableUser:
		return passwd.Writable(), nil
	}

	return "", fmt.Errorf("invalid user: %s", user)
}

func getPodName(ctx context.Context, cluster *mocov1beta2.MySQLCluster, index int) (string, error) {
	if index >= int(cluster.Spec.Replicas) {
		return "", errors.New("index should be smaller than replicas")
	}
	if index < 0 {
		index = cluster.Status.CurrentPrimaryIndex
	}

	return cluster.PodName(index), nil
}
