package password

import (
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
)

// Rotation key constants.
//
// The controller Secret always holds the 8 canonical current password keys.
// During a rotation cycle it additionally holds bookkeeping keys:
//
//   - *_PENDING keys stage the new passwords between seeding and promotion.
//   - *_OLD keys archive the previous passwords between promotion and cycle
//     completion. They exist for recovery only (MySQL stores only hashes) and
//     double as the "promotion done" marker for crash recovery.
//   - ROTATION_ID identifies the cycle from seeding to completion.
//   - RETAIN_STARTED marks that the RETAIN pre-check passed; it is removed
//     at promotion.
//
// Each key group is all-or-nothing: either all 8 keys of a group plus
// ROTATION_ID are present (and ROTATION_ID matches the expected value), or
// none are. The two groups never coexist — promotion replaces the pending
// group with the old group in a single Secret update. Any partial state is
// treated as an unrecoverable inconsistency and reported as an error. The
// controller never attempts automatic repair of partial state; explicit
// manual cleanup is required (documented in Event messages).
const (
	pendingKeySuffix = "_PENDING"
	oldKeySuffix     = "_OLD"

	AdminPasswordPendingKey       = AdminPasswordKey + pendingKeySuffix
	AgentPasswordPendingKey       = agentPasswordKey + pendingKeySuffix
	ReplicationPasswordPendingKey = replicationPasswordKey + pendingKeySuffix
	CloneDonorPasswordPendingKey  = cloneDonorPasswordKey + pendingKeySuffix
	ExporterPasswordPendingKey    = exporterPasswordKey + pendingKeySuffix
	BackupPasswordPendingKey      = BackupPasswordKey + pendingKeySuffix
	ReadOnlyPasswordPendingKey    = readOnlyPasswordKey + pendingKeySuffix
	WritablePasswordPendingKey    = writablePasswordKey + pendingKeySuffix

	AdminPasswordOldKey       = AdminPasswordKey + oldKeySuffix
	AgentPasswordOldKey       = agentPasswordKey + oldKeySuffix
	ReplicationPasswordOldKey = replicationPasswordKey + oldKeySuffix
	CloneDonorPasswordOldKey  = cloneDonorPasswordKey + oldKeySuffix
	ExporterPasswordOldKey    = exporterPasswordKey + oldKeySuffix
	BackupPasswordOldKey      = BackupPasswordKey + oldKeySuffix
	ReadOnlyPasswordOldKey    = readOnlyPasswordKey + oldKeySuffix
	WritablePasswordOldKey    = writablePasswordKey + oldKeySuffix

	RotationIDKey    = "ROTATION_ID"
	RetainStartedKey = "RETAIN_STARTED"
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

var allOldKeys = []string{
	AdminPasswordOldKey,
	AgentPasswordOldKey,
	ReplicationPasswordOldKey,
	CloneDonorPasswordOldKey,
	ExporterPasswordOldKey,
	BackupPasswordOldKey,
	ReadOnlyPasswordOldKey,
	WritablePasswordOldKey,
}

// pendingToCurrentKey maps pending key → current key for PromotePendingPasswords.
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

// currentToOldKey maps current key → old key for PromotePendingPasswords.
var currentToOldKey = map[string]string{
	AdminPasswordKey:       AdminPasswordOldKey,
	agentPasswordKey:       AgentPasswordOldKey,
	replicationPasswordKey: ReplicationPasswordOldKey,
	cloneDonorPasswordKey:  CloneDonorPasswordOldKey,
	exporterPasswordKey:    ExporterPasswordOldKey,
	BackupPasswordKey:      BackupPasswordOldKey,
	readOnlyPasswordKey:    ReadOnlyPasswordOldKey,
	writablePasswordKey:    WritablePasswordOldKey,
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

// userToCurrentKey maps MySQL user name → current key for CurrentKeyMap.
var userToCurrentKey = map[string]string{
	constants.AdminUser:       AdminPasswordKey,
	constants.AgentUser:       agentPasswordKey,
	constants.ReplicationUser: replicationPasswordKey,
	constants.CloneDonorUser:  cloneDonorPasswordKey,
	constants.ExporterUser:    exporterPasswordKey,
	constants.BackupUser:      BackupPasswordKey,
	constants.ReadOnlyUser:    readOnlyPasswordKey,
	constants.WritableUser:    writablePasswordKey,
}

// GetRotationID returns the stored ROTATION_ID from the secret, or "" if absent.
func GetRotationID(secret *corev1.Secret) string {
	if secret.Data == nil {
		return ""
	}
	return string(secret.Data[RotationIDKey])
}

// RotationSecretState describes the rotation bookkeeping state of a
// controller Secret, derived from which key groups are present.
type RotationSecretState int

const (
	// RotationSecretClean means no rotation bookkeeping keys are present.
	// The Secret holds only the canonical current passwords.
	RotationSecretClean RotationSecretState = iota

	// RotationSecretPending means all 8 *_PENDING keys and a matching
	// ROTATION_ID are present: new passwords are staged but not yet
	// promoted to current.
	RotationSecretPending

	// RotationSecretPromoted means all 8 *_OLD keys and a matching
	// ROTATION_ID are present with no *_PENDING keys: the staged
	// passwords have been promoted to current and the previous passwords
	// are archived for recovery.
	RotationSecretPromoted
)

// String implements fmt.Stringer for readable log and error messages.
func (s RotationSecretState) String() string {
	switch s {
	case RotationSecretClean:
		return "Clean"
	case RotationSecretPending:
		return "Pending"
	case RotationSecretPromoted:
		return "Promoted"
	default:
		return fmt.Sprintf("Unknown(%d)", int(s))
	}
}

// RotationState validates the rotation bookkeeping state of a controller
// secret.
//
// Returns an error for any inconsistent state that requires manual cleanup:
//   - partial key groups (some keys of a group present, some missing)
//   - both the pending and the old group present
//   - a key group present without ROTATION_ID, or ROTATION_ID without a group
//   - ROTATION_ID mismatch (stale keys from a different rotation cycle)
func RotationState(secret *corev1.Secret, expectedRotationID string) (RotationSecretState, error) {
	if secret.Data == nil {
		return RotationSecretClean, nil
	}

	pendingCount := 0
	for _, key := range allPendingKeys {
		if _, ok := secret.Data[key]; ok {
			pendingCount++
		}
	}
	oldCount := 0
	for _, key := range allOldKeys {
		if _, ok := secret.Data[key]; ok {
			oldCount++
		}
	}
	_, hasRotationID := secret.Data[RotationIDKey]

	if pendingCount == 0 && oldCount == 0 && !hasRotationID {
		return RotationSecretClean, nil
	}

	// Partial state or an impossible combination. This is always an error —
	// no automatic repair is attempted. The caller should surface this as a
	// Warning Event with manual cleanup instructions.
	if (pendingCount != 0 && pendingCount != len(allPendingKeys)) ||
		(oldCount != 0 && oldCount != len(allOldKeys)) ||
		(pendingCount != 0 && oldCount != 0) ||
		(pendingCount == 0 && oldCount == 0) || // ROTATION_ID without a key group
		!hasRotationID {
		return RotationSecretClean, fmt.Errorf(
			"inconsistent rotation state: %d/%d pending keys, %d/%d old keys, ROTATION_ID present=%v",
			pendingCount, len(allPendingKeys), oldCount, len(allOldKeys), hasRotationID)
	}

	storedID := string(secret.Data[RotationIDKey])
	if storedID != expectedRotationID {
		return RotationSecretClean, fmt.Errorf("ROTATION_ID mismatch: stored=%q expected=%q", storedID, expectedRotationID)
	}

	if pendingCount == len(allPendingKeys) {
		return RotationSecretPending, nil
	}
	return RotationSecretPromoted, nil
}

// SetPendingPasswords generates new random passwords and stores them as *_PENDING keys
// in the secret's Data. ROTATION_ID is also stored.
// Idempotent: if pending passwords already exist with a matching rotationID, returns
// the existing pending passwords without regeneration.
// Returns an error on inconsistent state, or when the Secret already holds a
// promoted (*_OLD) state — leftover old keys from an abandoned cycle must be
// cleaned up manually before a new cycle can be seeded.
func SetPendingPasswords(secret *corev1.Secret, rotationID string) (*MySQLPassword, error) {
	state, err := RotationState(secret, rotationID)
	if err != nil {
		return nil, fmt.Errorf("cannot set pending passwords: %w", err)
	}
	switch state {
	case RotationSecretPending:
		return MySQLPasswordFromPending(secret)
	case RotationSecretPromoted:
		return nil, fmt.Errorf("cannot set pending passwords: leftover promoted state (old password keys) exists for rotationID %s", rotationID)
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

// MySQLPasswordFromPending constructs MySQLPassword from the *_PENDING
// keys of a Secret. Not a constructor of fresh passwords — use
// NewMySQLPassword or SetPendingPasswords for that.
func MySQLPasswordFromPending(secret *corev1.Secret) (*MySQLPassword, error) {
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

// PromotePendingPasswords makes the staged pending passwords the new
// canonical current set, in a single in-memory mutation intended to be
// persisted with one Secret update:
//
//   - the current values are archived under the *_OLD keys,
//   - the *_PENDING values become the current values,
//   - the *_PENDING keys and RETAIN_STARTED are removed,
//   - ROTATION_ID is kept until the cycle completes (CleanupRotationKeys).
//
// The caller must only invoke this after ALTER USER ... RETAIN CURRENT
// PASSWORD succeeded on every instance — that is what makes the new current
// values authenticate everywhere.
//
// Idempotent: if the Secret is already in the promoted state for the given
// rotationID, returns nil without changes. Returns an error on any
// inconsistent state, or when there is nothing to promote (clean state).
func PromotePendingPasswords(secret *corev1.Secret, rotationID string) error {
	state, err := RotationState(secret, rotationID)
	if err != nil {
		return fmt.Errorf("cannot promote pending passwords: %w", err)
	}
	switch state {
	case RotationSecretPromoted:
		return nil
	case RotationSecretClean:
		return fmt.Errorf("cannot promote pending passwords: no pending state for rotationID %s", rotationID)
	}

	// Archive current → old, then copy pending → current.
	for currentKey := range currentToOldKey {
		if _, ok := secret.Data[currentKey]; !ok {
			return fmt.Errorf("cannot promote pending passwords: current key %s is missing", currentKey)
		}
	}
	for currentKey, oldKey := range currentToOldKey {
		secret.Data[oldKey] = secret.Data[currentKey]
	}
	for pendingKey, currentKey := range pendingToCurrentKey {
		secret.Data[currentKey] = secret.Data[pendingKey]
	}

	for _, key := range allPendingKeys {
		delete(secret.Data, key)
	}
	delete(secret.Data, RetainStartedKey)

	return nil
}

// CleanupRotationKeys removes the *_OLD keys, ROTATION_ID, and
// RETAIN_STARTED from the secret at the end of a rotation cycle. The
// canonical current passwords are untouched. Idempotent: a clean secret
// (crash recovery after the keys were already removed) is a no-op.
//
// The state is validated with RotationState first, so an inconsistent
// secret (unpromoted *_PENDING keys, a partial *_OLD group, or a
// ROTATION_ID from a different cycle) is surfaced as an error instead of
// being silently erased — deleting such bookkeeping would hide the very
// inconsistency it records.
func CleanupRotationKeys(secret *corev1.Secret, rotationID string) error {
	state, err := RotationState(secret, rotationID)
	if err != nil {
		return fmt.Errorf("cannot clean up rotation keys: %w", err)
	}
	if state == RotationSecretPending {
		return fmt.Errorf("cannot clean up rotation keys: unpromoted pending passwords are present for rotationID %s", rotationID)
	}
	// Promoted, or Clean (already cleaned; a leftover RETAIN_STARTED
	// marker alone is also reported as Clean and removed here).
	if secret.Data == nil {
		return nil
	}
	for _, key := range allOldKeys {
		delete(secret.Data, key)
	}
	delete(secret.Data, RotationIDKey)
	delete(secret.Data, RetainStartedKey)
	return nil
}

// CurrentPasswordsMatch returns true if all 8 current password keys exist
// and have identical values in both secrets. This is used to verify that a
// distributed per-namespace Secret has caught up with the controller
// Secret's current passwords.
// Returns false if either secret has nil Data or is missing any key.
func CurrentPasswordsMatch(secretA, secretB *corev1.Secret) bool {
	if secretA.Data == nil || secretB.Data == nil {
		return false
	}
	for _, key := range pendingToCurrentKey {
		valA, okA := secretA.Data[key]
		valB, okB := secretB.Data[key]
		if !okA || !okB || string(valA) != string(valB) {
			return false
		}
	}
	return true
}

// PendingKeyMap returns a map of MySQL user name → pending password.
// Returns error if pending keys are not all present.
func PendingKeyMap(secret *corev1.Secret) (map[string]string, error) {
	if secret.Data == nil {
		return nil, fmt.Errorf("secret %s/%s has no data", secret.Namespace, secret.Name)
	}
	for _, key := range allPendingKeys {
		if _, ok := secret.Data[key]; !ok {
			return nil, fmt.Errorf("pending key %s not found in secret %s/%s", key, secret.Namespace, secret.Name)
		}
	}

	result := make(map[string]string, len(userToPendingKey))
	for user, pendingKey := range userToPendingKey {
		result[user] = string(secret.Data[pendingKey])
	}
	return result, nil
}

// CurrentKeyMap returns a map of MySQL user name → current password.
// Returns error if current keys are not all present.
func CurrentKeyMap(secret *corev1.Secret) (map[string]string, error) {
	if secret.Data == nil {
		return nil, fmt.Errorf("secret %s/%s has no data", secret.Namespace, secret.Name)
	}

	result := make(map[string]string, len(userToCurrentKey))
	for user, currentKey := range userToCurrentKey {
		val, ok := secret.Data[currentKey]
		if !ok {
			return nil, fmt.Errorf("current key %s not found in secret %s/%s", currentKey, secret.Namespace, secret.Name)
		}
		result[user] = string(val)
	}
	return result, nil
}
