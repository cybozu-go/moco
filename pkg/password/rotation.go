package password

import (
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
)

// Pending password key constants.
//
// During rotation, the source Secret holds both current and pending passwords.
// The pending state must be all-or-nothing: either all 8 *_PENDING keys plus
// ROTATION_ID are present (and ROTATION_ID matches the expected value), or none
// are present. Any partial state is treated as an unrecoverable inconsistency
// and reported as an error. The controller never attempts automatic repair of
// partial state; explicit manual cleanup is required (documented in Event messages).
const (
	AdminPasswordPendingKey       = "ADMIN_PASSWORD_PENDING"
	AgentPasswordPendingKey       = "AGENT_PASSWORD_PENDING"
	ReplicationPasswordPendingKey = "REPLICATION_PASSWORD_PENDING"
	CloneDonorPasswordPendingKey  = "CLONE_DONOR_PASSWORD_PENDING"
	ExporterPasswordPendingKey    = "EXPORTER_PASSWORD_PENDING"
	BackupPasswordPendingKey      = "BACKUP_PASSWORD_PENDING"
	ReadOnlyPasswordPendingKey    = "READONLY_PASSWORD_PENDING"
	WritablePasswordPendingKey    = "WRITABLE_PASSWORD_PENDING"

	RotationIDKey = "ROTATION_ID"
)

var allPendingKeys = []string{
	AdminPasswordPendingKey,
	AgentPasswordPendingKey,
	ReplicationPasswordPendingKey,
	CloneDonorPasswordPendingKey,
	ExporterPasswordPendingKey,
	BackupPasswordPendingKey,
	ReadOnlyPasswordPendingKey,
	WritablePasswordPendingKey,
}

// pendingToCurrentKey maps pending key → current key for ConfirmPendingPasswords.
var pendingToCurrentKey = map[string]string{
	AdminPasswordPendingKey:       AdminPasswordKey,
	AgentPasswordPendingKey:       agentPasswordKey,
	ReplicationPasswordPendingKey: replicationPasswordKey,
	CloneDonorPasswordPendingKey:  cloneDonorPasswordKey,
	ExporterPasswordPendingKey:    exporterPasswordKey,
	BackupPasswordPendingKey:      BackupPasswordKey,
	ReadOnlyPasswordPendingKey:    readOnlyPasswordKey,
	WritablePasswordPendingKey:    writablePasswordKey,
}

// userToPendingKey maps MySQL user name → pending key for PendingKeyMap.
var userToPendingKey = map[string]string{
	constants.AdminUser:       AdminPasswordPendingKey,
	constants.AgentUser:       AgentPasswordPendingKey,
	constants.ReplicationUser: ReplicationPasswordPendingKey,
	constants.CloneDonorUser:  CloneDonorPasswordPendingKey,
	constants.ExporterUser:    ExporterPasswordPendingKey,
	constants.BackupUser:      BackupPasswordPendingKey,
	constants.ReadOnlyUser:    ReadOnlyPasswordPendingKey,
	constants.WritableUser:    WritablePasswordPendingKey,
}

// HasPendingPasswords validates the pending state of a source secret.
//
// Returns:
//   - (false, nil): no pending keys and no ROTATION_ID — clean state
//   - (true, nil):  all 8 *_PENDING keys + ROTATION_ID present, ID matches
//   - (*, error):   inconsistent state that requires manual cleanup:
//   - partial pending keys (some present, some missing)
//   - ROTATION_ID present without pending keys (or vice versa)
//   - ROTATION_ID mismatch (stale pending from a previous rotation)
func HasPendingPasswords(secret *corev1.Secret, expectedRotationID string) (bool, error) {
	if secret.Data == nil {
		return false, nil
	}

	pendingCount := 0
	for _, key := range allPendingKeys {
		if _, ok := secret.Data[key]; ok {
			pendingCount++
		}
	}
	_, hasRotationID := secret.Data[RotationIDKey]

	// Neither pending keys nor ROTATION_ID
	if pendingCount == 0 && !hasRotationID {
		return false, nil
	}

	// Partial state: some pending keys but not all, or ROTATION_ID without
	// pending keys, or vice versa. This is always an error — no automatic
	// repair is attempted. The caller should surface this as a Warning Event
	// with manual cleanup instructions.
	if pendingCount != len(allPendingKeys) || !hasRotationID {
		return false, fmt.Errorf("inconsistent pending state: %d/%d pending keys present, ROTATION_ID present=%v",
			pendingCount, len(allPendingKeys), hasRotationID)
	}

	// All present — check ROTATION_ID match
	storedID := string(secret.Data[RotationIDKey])
	if storedID != expectedRotationID {
		return false, fmt.Errorf("ROTATION_ID mismatch: stored=%q expected=%q", storedID, expectedRotationID)
	}

	return true, nil
}

// SetPendingPasswords generates new random passwords and stores them as *_PENDING keys
// in the secret's Data. ROTATION_ID is also stored.
// Idempotent: if pending passwords already exist with a matching rotationID, returns
// the existing pending passwords without regeneration.
func SetPendingPasswords(secret *corev1.Secret, rotationID string) (*MySQLPassword, error) {
	has, err := HasPendingPasswords(secret, rotationID)
	if err != nil {
		return nil, fmt.Errorf("cannot set pending passwords: %w", err)
	}
	if has {
		return NewMySQLPasswordFromPending(secret)
	}

	pwd, err := NewMySQLPassword()
	if err != nil {
		return nil, err
	}

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[AdminPasswordPendingKey] = []byte(pwd.admin)
	secret.Data[AgentPasswordPendingKey] = []byte(pwd.agent)
	secret.Data[ReplicationPasswordPendingKey] = []byte(pwd.replicator)
	secret.Data[CloneDonorPasswordPendingKey] = []byte(pwd.donor)
	secret.Data[ExporterPasswordPendingKey] = []byte(pwd.exporter)
	secret.Data[BackupPasswordPendingKey] = []byte(pwd.backup)
	secret.Data[ReadOnlyPasswordPendingKey] = []byte(pwd.readOnly)
	secret.Data[WritablePasswordPendingKey] = []byte(pwd.writable)
	secret.Data[RotationIDKey] = []byte(rotationID)

	return pwd, nil
}

// NewMySQLPasswordFromPending constructs MySQLPassword from *_PENDING keys in the secret.
func NewMySQLPasswordFromPending(secret *corev1.Secret) (*MySQLPassword, error) {
	if secret.Data == nil {
		return nil, fmt.Errorf("secret %s/%s has no data", secret.Namespace, secret.Name)
	}
	for _, key := range allPendingKeys {
		if _, ok := secret.Data[key]; !ok {
			return nil, fmt.Errorf("secret %s/%s is missing pending key %s", secret.Namespace, secret.Name, key)
		}
	}

	return &MySQLPassword{
		admin:      string(secret.Data[AdminPasswordPendingKey]),
		agent:      string(secret.Data[AgentPasswordPendingKey]),
		replicator: string(secret.Data[ReplicationPasswordPendingKey]),
		donor:      string(secret.Data[CloneDonorPasswordPendingKey]),
		exporter:   string(secret.Data[ExporterPasswordPendingKey]),
		backup:     string(secret.Data[BackupPasswordPendingKey]),
		readOnly:   string(secret.Data[ReadOnlyPasswordPendingKey]),
		writable:   string(secret.Data[WritablePasswordPendingKey]),
	}, nil
}

// ConfirmPendingPasswords copies pending passwords to current keys and removes
// pending keys and ROTATION_ID from the secret.
// Idempotent: if no pending keys and no ROTATION_ID exist, returns nil (no-op).
// Returns error only on inconsistent state (partial pending keys).
func ConfirmPendingPasswords(secret *corev1.Secret) error {
	if secret.Data == nil {
		return nil
	}

	pendingCount := 0
	for _, key := range allPendingKeys {
		if _, ok := secret.Data[key]; ok {
			pendingCount++
		}
	}
	_, hasRotationID := secret.Data[RotationIDKey]

	// No pending state at all — idempotent no-op
	if pendingCount == 0 && !hasRotationID {
		return nil
	}

	// Partial state: some pending keys remain but not all, or pending keys
	// exist without ROTATION_ID. This indicates an inconsistency that should
	// not be auto-repaired. Return an error so the caller can surface it for
	// manual investigation.
	if pendingCount != len(allPendingKeys) {
		return fmt.Errorf("inconsistent pending state during confirm: %d/%d pending keys present",
			pendingCount, len(allPendingKeys))
	}
	if !hasRotationID {
		return fmt.Errorf("inconsistent pending state during confirm: all pending keys present but ROTATION_ID is missing")
	}

	// Copy pending → current
	for pendingKey, currentKey := range pendingToCurrentKey {
		secret.Data[currentKey] = secret.Data[pendingKey]
	}

	// Delete pending keys and ROTATION_ID
	for _, key := range allPendingKeys {
		delete(secret.Data, key)
	}
	delete(secret.Data, RotationIDKey)

	return nil
}

// PendingKeyMap returns a map of MySQL user name → pending password.
// Returns error if pending keys are not all present.
func PendingKeyMap(secret *corev1.Secret) (map[string]string, error) {
	if secret.Data == nil {
		return nil, fmt.Errorf("secret has no data")
	}
	for _, key := range allPendingKeys {
		if _, ok := secret.Data[key]; !ok {
			return nil, fmt.Errorf("pending key %s not found in secret", key)
		}
	}

	result := make(map[string]string, len(userToPendingKey))
	for user, pendingKey := range userToPendingKey {
		result[user] = string(secret.Data[pendingKey])
	}
	return result, nil
}
