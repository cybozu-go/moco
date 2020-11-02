package cmd

import (
	"context"
	"errors"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func getPassword(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, user string) (string, error) {
	secret := &corev1.Secret{}
	err := kubeClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      "root-password-" + moco.UniqueName(cluster),
	}, secret)
	if err != nil {
		return "", err
	}

	userPassKeys := map[string]string{
		"root":                       moco.RootPasswordEnvName,
		moco.WritablePasswordEnvName: moco.WritablePasswordEnvName,
		moco.ReadOnlyPasswordEnvName: moco.ReadOnlyPasswordEnvName,
	}
	key, ok := userPassKeys[user]
	if !ok {
		return "", errors.New("unknown user: " + user)
	}
	password, ok := secret.Data[key]
	if !ok {
		return "", errors.New("unknown user: " + user)
	}
	return string(password), nil
}
