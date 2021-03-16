package operators

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	passwordVersion = "1"

	passwordBytes = 16

	adminPasswordKey       = "ADMIN_PASSWORD"
	agentPasswordKey       = "AGENT_PASSWORD"
	replicationPasswordKey = "REPLICATION_PASSWORD"
	cloneDonorPasswordKey  = "CLONE_DONOR_PASSWORD"
	ReadOnlyPasswordKey    = "READONLY_PASSWORD"
	WritablePasswordKey    = "WRITABLE_PASSWORD"
)

type MySQLPassword struct {
	admin      string
	agent      string
	replicator string
	donor      string
	readOnly   string
	writable   string
}

func NewMySQLPassword() (*MySQLPassword, error) {
	admin, err := generateRandomPassword()
	if err != nil {
		return nil, err
	}

	agent, err := generateRandomPassword()
	if err != nil {
		return nil, err
	}

	replicator, err := generateRandomPassword()
	if err != nil {
		return nil, err
	}

	donor, err := generateRandomPassword()
	if err != nil {
		return nil, err
	}

	readOnly, err := generateRandomPassword()
	if err != nil {
		return nil, err
	}

	writable, err := generateRandomPassword()
	if err != nil {
		return nil, err
	}

	return &MySQLPassword{
		admin:      admin,
		agent:      agent,
		replicator: replicator,
		donor:      donor,
		readOnly:   readOnly,
		writable:   writable,
	}, nil
}

func NewMySQLPasswordFromSecret(secret *corev1.Secret) (*MySQLPassword, error) {
	if secret.Annotations[constants.SecretVersionAnnKey] != passwordVersion {
		return nil, fmt.Errorf("secret %s/%s does not have valid annotation", secret.Namespace, secret.Name)
	}

	return &MySQLPassword{
		admin:      string(secret.Data[adminPasswordKey]),
		agent:      string(secret.Data[agentPasswordKey]),
		replicator: string(secret.Data[replicationPasswordKey]),
		donor:      string(secret.Data[cloneDonorPasswordKey]),
		readOnly:   string(secret.Data[ReadOnlyPasswordKey]),
		writable:   string(secret.Data[WritablePasswordKey]),
	}, nil
}

// Caller must fill Name and Namespace of the returned Secret.
func (p MySQLPassword) ToSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				constants.SecretVersionAnnKey: passwordVersion,
			},
		},
		Data: map[string][]byte{
			adminPasswordKey:       []byte(p.admin),
			agentPasswordKey:       []byte(p.agent),
			replicationPasswordKey: []byte(p.replicator),
			cloneDonorPasswordKey:  []byte(p.donor),
			ReadOnlyPasswordKey:    []byte(p.readOnly),
			WritablePasswordKey:    []byte(p.writable),
		},
	}
}

// Caller must fill Name and Namespace of the returned Secret.
func (p MySQLPassword) ToMyCnfSecret() *corev1.Secret {
	formatMyCnf := func(user, pwd string) []byte {
		return []byte(fmt.Sprintf(`[client]
user="%s"
password="%s"
`, user, pwd))
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				constants.SecretVersionAnnKey: passwordVersion,
			},
		},
		Data: map[string][]byte{
			constants.ReadOnlyMyCnfKey: formatMyCnf(constants.ReadOnlyUser, p.readOnly),
			constants.WritableMyCnfKey: formatMyCnf(constants.WritableUser, p.writable),
		},
	}
}

func (p MySQLPassword) Admin() string {
	return p.admin
}

func (p MySQLPassword) Agent() string {
	return p.agent
}

func (p MySQLPassword) Replicator() string {
	return p.replicator
}

func (p MySQLPassword) Donor() string {
	return p.donor
}

func (p MySQLPassword) ReadOnly() string {
	return p.readOnly
}

func (p MySQLPassword) Writable() string {
	return p.writable
}

func generateRandomPassword() (string, error) {
	password := make([]byte, passwordBytes)
	_, err := rand.Read(password)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(password), nil
}
