package password

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"text/template"

	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// passwordVersion is the current secret format version for MySQL passwords.
const passwordVersion = "1"

const (
	passwordBytes = 16

	adminPasswordKey       = "ADMIN_PASSWORD"
	agentPasswordKey       = "AGENT_PASSWORD"
	replicationPasswordKey = "REPLICATION_PASSWORD"
	cloneDonorPasswordKey  = "CLONE_DONOR_PASSWORD"
	ExporterPasswordKey    = "EXPORTER_PASSWORD"
	BackupPasswordKey      = "BACKUP_PASSWORD"
	readOnlyPasswordKey    = "READONLY_PASSWORD"
	writablePasswordKey    = "WRITABLE_PASSWORD"
)

// MySQLPassword represents a set of passwords of MySQL users for MOCO
type MySQLPassword struct {
	admin      string
	agent      string
	replicator string
	donor      string
	exporter   string
	backup     string
	readOnly   string
	writable   string
}

// NewMySQLPassword generates random passwords for NewMySQLPassword and return it.
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

	exporter, err := generateRandomPassword()
	if err != nil {
		return nil, err
	}

	backup, err := generateRandomPassword()
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
		exporter:   exporter,
		backup:     backup,
		readOnly:   readOnly,
		writable:   writable,
	}, nil
}

// NewMySQLPasswordFromSecret constructs MySQLPassword from Secret.
func NewMySQLPasswordFromSecret(secret *corev1.Secret) (*MySQLPassword, error) {
	if secret.Annotations[constants.AnnSecretVersion] != passwordVersion {
		return nil, fmt.Errorf("secret %s/%s does not have valid annotation", secret.Namespace, secret.Name)
	}

	return &MySQLPassword{
		admin:      string(secret.Data[adminPasswordKey]),
		agent:      string(secret.Data[agentPasswordKey]),
		replicator: string(secret.Data[replicationPasswordKey]),
		donor:      string(secret.Data[cloneDonorPasswordKey]),
		exporter:   string(secret.Data[ExporterPasswordKey]),
		backup:     string(secret.Data[BackupPasswordKey]),
		readOnly:   string(secret.Data[readOnlyPasswordKey]),
		writable:   string(secret.Data[writablePasswordKey]),
	}, nil
}

// ToSecret converts MySQLPassword to Secret.
// The caller have to fill Name and Namespace of the returned Secret.
func (p MySQLPassword) ToSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				constants.AnnSecretVersion: passwordVersion,
			},
		},
		Data: map[string][]byte{
			adminPasswordKey:       []byte(p.admin),
			agentPasswordKey:       []byte(p.agent),
			replicationPasswordKey: []byte(p.replicator),
			cloneDonorPasswordKey:  []byte(p.donor),
			ExporterPasswordKey:    []byte(p.exporter),
			BackupPasswordKey:      []byte(p.backup),
			readOnlyPasswordKey:    []byte(p.readOnly),
			writablePasswordKey:    []byte(p.writable),
		},
	}
}

var mycnfTmpl = template.Must(template.New("my.cnf").Parse(`[client]
user={{printf "%q" .User}}
password={{printf "%q" .Password}}
{{if .Socket -}}
socket={{printf "%q" .Socket}}
{{end}}`))

func formatMyCnf(user, pwd, socket string) []byte {
	buf := new(bytes.Buffer)
	err := mycnfTmpl.Execute(buf, struct {
		User     string
		Password string
		Socket   string
	}{
		user,
		pwd,
		socket,
	})
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// ToMyCnfSecret converts MySQLPassword to Secret in my.cnf format.
// The caller have to fill Name and Namespace of the returned Secret.
func (p MySQLPassword) ToMyCnfSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				constants.AnnSecretVersion: passwordVersion,
			},
		},
		Data: map[string][]byte{
			constants.AdminMyCnf:    formatMyCnf(constants.AdminUser, p.admin, ""),
			constants.ExporterMyCnf: formatMyCnf(constants.ExporterUser, p.exporter, filepath.Join(constants.RunPath, "mysqld.sock")),
			constants.BackupMyCnf:   formatMyCnf(constants.BackupUser, p.backup, ""),
			constants.ReadOnlyMyCnf: formatMyCnf(constants.ReadOnlyUser, p.readOnly, ""),
			constants.WritableMyCnf: formatMyCnf(constants.WritableUser, p.writable, ""),
		},
	}
}

// Admin returns the password for moco-admin.
func (p MySQLPassword) Admin() string {
	return p.admin
}

// Agent returns the password for moco-agent.
func (p MySQLPassword) Agent() string {
	return p.agent
}

// Replicator returns the password for moco-repl.
func (p MySQLPassword) Replicator() string {
	return p.replicator
}

// Donor returns the password for moco-clone-donor.
func (p MySQLPassword) Donor() string {
	return p.donor
}

// Exporter returns the password for mysqld_exporter.
func (p MySQLPassword) Exporter() string {
	return p.exporter
}

// Backup returns the password for backup jobs.
func (p MySQLPassword) Backup() string {
	return p.backup
}

// ReadOnly returns the password for moco-readonly.
func (p MySQLPassword) ReadOnly() string {
	return p.readOnly
}

// Writable returns the password for moco-writable.
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
