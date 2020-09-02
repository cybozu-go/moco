package accessor

import (
	"context"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Infrastructure struct {
	client.Client
	MySQLAccessor *MySQLAccessor
}

func (i Infrastructure) GetClient() client.Client {
	return i.Client
}

func (i Infrastructure) GetDB(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, index int) (*sqlx.DB, error) {
	operatorPassword, err := i.GetPassword(ctx, cluster, moco.OperatorPasswordKey)
	if err != nil {
		return nil, err
	}

	db, err := i.MySQLAccessor.Get(moco.GetHost(cluster, index), moco.OperatorAdminUser, operatorPassword)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (i Infrastructure) GetPassword(ctx context.Context, cluster *mocov1alpha1.MySQLCluster, passwordKey string) (string, error) {
	secret := &corev1.Secret{}
	myNS, mySecretName := moco.GetSecretNameForController(cluster)
	err := i.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err != nil {
		return "", err
	}
	return string(secret.Data[passwordKey]), nil
}
