package password

import (
	"testing"

	"github.com/cybozu-go/moco/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRotationState(t *testing.T) {
	t.Run("empty secret is clean", func(t *testing.T) {
		secret := &corev1.Secret{Data: map[string][]byte{}}
		state, err := RotationState(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if state != RotationSecretClean {
			t.Errorf("expected clean, got %v", state)
		}
	})

	t.Run("nil data is clean", func(t *testing.T) {
		secret := &corev1.Secret{}
		state, err := RotationState(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if state != RotationSecretClean {
			t.Errorf("expected clean, got %v", state)
		}
	})

	t.Run("current keys only is clean", func(t *testing.T) {
		secret := makeSecretWithCurrent()
		state, err := RotationState(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if state != RotationSecretClean {
			t.Errorf("expected clean, got %v", state)
		}
	})

	t.Run("all pending with matching ID is pending", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		state, err := RotationState(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if state != RotationSecretPending {
			t.Errorf("expected pending, got %v", state)
		}
	})

	t.Run("all old with matching ID is promoted", func(t *testing.T) {
		secret := makeSecretWithOld("test-id")
		state, err := RotationState(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if state != RotationSecretPromoted {
			t.Errorf("expected promoted, got %v", state)
		}
	})

	t.Run("ROTATION_ID mismatch on pending returns error", func(t *testing.T) {
		secret := makeSecretWithPending("old-id")
		if _, err := RotationState(secret, "new-id"); err == nil {
			t.Fatal("expected error for mismatched ROTATION_ID")
		}
	})

	t.Run("ROTATION_ID mismatch on old returns error", func(t *testing.T) {
		secret := makeSecretWithOld("old-id")
		if _, err := RotationState(secret, "new-id"); err == nil {
			t.Fatal("expected error for mismatched ROTATION_ID")
		}
	})

	t.Run("partial pending returns error", func(t *testing.T) {
		secret := &corev1.Secret{
			Data: map[string][]byte{
				AdminPasswordPendingKey: []byte("pwd"),
				RotationIDKey:           []byte("test-id"),
			},
		}
		if _, err := RotationState(secret, "test-id"); err == nil {
			t.Fatal("expected error for partial pending")
		}
	})

	t.Run("partial old returns error", func(t *testing.T) {
		secret := &corev1.Secret{
			Data: map[string][]byte{
				AdminPasswordOldKey: []byte("pwd"),
				RotationIDKey:       []byte("test-id"),
			},
		}
		if _, err := RotationState(secret, "test-id"); err == nil {
			t.Fatal("expected error for partial old")
		}
	})

	t.Run("ROTATION_ID without key group returns error", func(t *testing.T) {
		secret := &corev1.Secret{
			Data: map[string][]byte{
				RotationIDKey: []byte("test-id"),
			},
		}
		if _, err := RotationState(secret, "test-id"); err == nil {
			t.Fatal("expected error for ROTATION_ID without a key group")
		}
	})

	t.Run("pending group without ROTATION_ID returns error", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		delete(secret.Data, RotationIDKey)
		if _, err := RotationState(secret, "test-id"); err == nil {
			t.Fatal("expected error for pending group without ROTATION_ID")
		}
	})

	t.Run("both pending and old groups returns error", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		for _, key := range allOldKeys {
			secret.Data[key] = []byte("old")
		}
		if _, err := RotationState(secret, "test-id"); err == nil {
			t.Fatal("expected error for coexisting pending and old groups")
		}
	})
}

func TestSetPendingPasswords(t *testing.T) {
	t.Run("generates pending passwords", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "ns",
				Annotations: map[string]string{
					constants.AnnSecretVersion: "1",
				},
			},
			Data: map[string][]byte{
				AdminPasswordKey: []byte("old-admin"),
			},
		}
		pwd, err := SetPendingPasswords(secret, "rot-1")
		if err != nil {
			t.Fatal(err)
		}
		if pwd.Admin() == "" {
			t.Error("expected non-empty admin password")
		}
		if string(secret.Data[RotationIDKey]) != "rot-1" {
			t.Errorf("expected ROTATION_ID=rot-1, got %s", secret.Data[RotationIDKey])
		}
		for _, key := range allPendingKeys {
			if _, ok := secret.Data[key]; !ok {
				t.Errorf("expected pending key %s", key)
			}
		}
	})

	t.Run("idempotent when matching ID exists", func(t *testing.T) {
		secret := makeSecretWithPending("rot-1")
		origAdmin := string(secret.Data[AdminPasswordPendingKey])

		pwd, err := SetPendingPasswords(secret, "rot-1")
		if err != nil {
			t.Fatal(err)
		}
		if pwd.Admin() != origAdmin {
			t.Error("expected existing pending password to be reused")
		}
	})

	t.Run("error when stale pending exists", func(t *testing.T) {
		secret := makeSecretWithPending("old-id")
		if _, err := SetPendingPasswords(secret, "new-id"); err == nil {
			t.Fatal("expected error for stale pending")
		}
	})

	t.Run("error when promoted state exists", func(t *testing.T) {
		secret := makeSecretWithOld("rot-1")
		if _, err := SetPendingPasswords(secret, "rot-1"); err == nil {
			t.Fatal("expected error for leftover promoted state")
		}
	})
}

func TestMySQLPasswordFromPending(t *testing.T) {
	secret := makeSecretWithPending("test-id")
	pwd, err := MySQLPasswordFromPending(secret)
	if err != nil {
		t.Fatal(err)
	}
	if pwd.Admin() != string(secret.Data[AdminPasswordPendingKey]) {
		t.Error("admin password mismatch")
	}
	if pwd.Agent() != string(secret.Data[AgentPasswordPendingKey]) {
		t.Error("agent password mismatch")
	}
}

func TestPromotePendingPasswords(t *testing.T) {
	t.Run("promotes pending to current and archives current as old", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		secret.Data[RetainStartedKey] = []byte("test-id")

		pendingAdmin := string(secret.Data[AdminPasswordPendingKey])

		if err := PromotePendingPasswords(secret, "test-id"); err != nil {
			t.Fatal(err)
		}
		if string(secret.Data[AdminPasswordKey]) != pendingAdmin {
			t.Errorf("expected admin password to be updated to %s", pendingAdmin)
		}
		if string(secret.Data[AdminPasswordOldKey]) != "current-admin" {
			t.Errorf("expected old admin password to be archived, got %s", secret.Data[AdminPasswordOldKey])
		}
		if _, ok := secret.Data[AdminPasswordPendingKey]; ok {
			t.Error("pending key should be removed")
		}
		if _, ok := secret.Data[RetainStartedKey]; ok {
			t.Error("RETAIN_STARTED should be removed")
		}
		// ROTATION_ID stays until CleanupRotationKeys.
		if string(secret.Data[RotationIDKey]) != "test-id" {
			t.Error("ROTATION_ID should be kept until cycle completion")
		}
		// The result must be a valid promoted state.
		state, err := RotationState(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if state != RotationSecretPromoted {
			t.Errorf("expected promoted state after promotion, got %v", state)
		}
	})

	t.Run("idempotent when already promoted", func(t *testing.T) {
		secret := makeSecretWithOld("test-id")
		currentAdmin := string(secret.Data[AdminPasswordKey])
		if err := PromotePendingPasswords(secret, "test-id"); err != nil {
			t.Fatal(err)
		}
		if string(secret.Data[AdminPasswordKey]) != currentAdmin {
			t.Error("current password should not be changed on re-promotion")
		}
	})

	t.Run("error when nothing to promote", func(t *testing.T) {
		secret := makeSecretWithCurrent()
		if err := PromotePendingPasswords(secret, "test-id"); err == nil {
			t.Fatal("expected error for clean state")
		}
	})

	t.Run("error on partial pending", func(t *testing.T) {
		secret := &corev1.Secret{
			Data: map[string][]byte{
				AdminPasswordPendingKey: []byte("pwd"),
				RotationIDKey:           []byte("test-id"),
			},
		}
		if err := PromotePendingPasswords(secret, "test-id"); err == nil {
			t.Fatal("expected error for partial pending")
		}
	})

	t.Run("error when a current key is missing", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		delete(secret.Data, AdminPasswordKey)
		if err := PromotePendingPasswords(secret, "test-id"); err == nil {
			t.Fatal("expected error for missing current key")
		}
	})
}

func TestCleanupRotationKeys(t *testing.T) {
	t.Run("removes old keys and bookkeeping", func(t *testing.T) {
		secret := makeSecretWithOld("test-id")
		currentAdmin := string(secret.Data[AdminPasswordKey])

		if err := CleanupRotationKeys(secret, "test-id"); err != nil {
			t.Fatal(err)
		}
		for _, key := range allOldKeys {
			if _, ok := secret.Data[key]; ok {
				t.Errorf("old key %s should be removed", key)
			}
		}
		if _, ok := secret.Data[RotationIDKey]; ok {
			t.Error("ROTATION_ID should be removed")
		}
		if string(secret.Data[AdminPasswordKey]) != currentAdmin {
			t.Error("current password should not be changed")
		}
	})

	t.Run("idempotent on clean secret", func(t *testing.T) {
		secret := makeSecretWithCurrent()
		if err := CleanupRotationKeys(secret, "test-id"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("removes a leftover RETAIN_STARTED marker alone", func(t *testing.T) {
		secret := makeSecretWithCurrent()
		secret.Data[RetainStartedKey] = []byte("stale-id")
		if err := CleanupRotationKeys(secret, "test-id"); err != nil {
			t.Fatal(err)
		}
		if _, ok := secret.Data[RetainStartedKey]; ok {
			t.Error("RETAIN_STARTED should be removed")
		}
	})

	t.Run("nil data is no-op", func(t *testing.T) {
		secret := &corev1.Secret{}
		if err := CleanupRotationKeys(secret, "test-id"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("error when pending keys are present", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		if err := CleanupRotationKeys(secret, "test-id"); err == nil {
			t.Fatal("expected error for unpromoted pending keys")
		}
	})

	t.Run("error on ROTATION_ID from a different cycle", func(t *testing.T) {
		secret := makeSecretWithOld("other-id")
		if err := CleanupRotationKeys(secret, "test-id"); err == nil {
			t.Fatal("expected error for mismatched ROTATION_ID")
		}
		if _, ok := secret.Data[AdminPasswordOldKey]; !ok {
			t.Error("old keys must be preserved on error")
		}
	})

	t.Run("error on partial old group", func(t *testing.T) {
		secret := makeSecretWithCurrent()
		secret.Data[AdminPasswordOldKey] = []byte("old-admin")
		secret.Data[RotationIDKey] = []byte("test-id")
		if err := CleanupRotationKeys(secret, "test-id"); err == nil {
			t.Fatal("expected error for partial old group")
		}
	})
}

func TestCurrentPasswordsMatch(t *testing.T) {
	t.Run("matches identical current keys", func(t *testing.T) {
		a := makeSecretWithCurrent()
		b := makeSecretWithCurrent()
		if !CurrentPasswordsMatch(a, b) {
			t.Error("expected match")
		}
	})

	t.Run("does not match different values", func(t *testing.T) {
		a := makeSecretWithCurrent()
		b := makeSecretWithCurrent()
		b.Data[AdminPasswordKey] = []byte("different")
		if CurrentPasswordsMatch(a, b) {
			t.Error("expected mismatch")
		}
	})

	t.Run("does not match missing keys", func(t *testing.T) {
		a := makeSecretWithCurrent()
		b := makeSecretWithCurrent()
		delete(b.Data, AdminPasswordKey)
		if CurrentPasswordsMatch(a, b) {
			t.Error("expected mismatch for missing key")
		}
	})

	t.Run("nil data does not match", func(t *testing.T) {
		a := makeSecretWithCurrent()
		b := &corev1.Secret{}
		if CurrentPasswordsMatch(a, b) {
			t.Error("expected mismatch for nil data")
		}
	})
}

func TestPendingKeyMap(t *testing.T) {
	t.Run("returns map when all pending keys present", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		m, err := PendingKeyMap(secret)
		if err != nil {
			t.Fatal(err)
		}
		if len(m) != len(allPendingKeys) {
			t.Errorf("expected %d entries, got %d", len(allPendingKeys), len(m))
		}
		if m["moco-admin"] != string(secret.Data[AdminPasswordPendingKey]) {
			t.Error("admin password mismatch in map")
		}
	})

	t.Run("error when pending keys missing", func(t *testing.T) {
		secret := &corev1.Secret{Data: map[string][]byte{}}
		if _, err := PendingKeyMap(secret); err == nil {
			t.Fatal("expected error for missing pending keys")
		}
	})
}

func TestCurrentKeyMap(t *testing.T) {
	t.Run("returns map when all current keys present", func(t *testing.T) {
		secret := makeSecretWithCurrent()
		m, err := CurrentKeyMap(secret)
		if err != nil {
			t.Fatal(err)
		}
		if len(m) != len(userToCurrentKey) {
			t.Errorf("expected %d entries, got %d", len(userToCurrentKey), len(m))
		}
		if m["moco-admin"] != "current-admin" {
			t.Error("admin password mismatch in map")
		}
	})

	t.Run("error when a current key is missing", func(t *testing.T) {
		secret := makeSecretWithCurrent()
		delete(secret.Data, AdminPasswordKey)
		if _, err := CurrentKeyMap(secret); err == nil {
			t.Fatal("expected error for missing current key")
		}
	})
}

// makeSecretWithCurrent returns a secret holding only the 8 current keys —
// the steady state of a controller Secret.
func makeSecretWithCurrent() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "ns",
		},
		Data: map[string][]byte{
			AdminPasswordKey:       []byte("current-admin"),
			agentPasswordKey:       []byte("current-agent"),
			replicationPasswordKey: []byte("current-repl"),
			cloneDonorPasswordKey:  []byte("current-donor"),
			exporterPasswordKey:    []byte("current-exporter"),
			BackupPasswordKey:      []byte("current-backup"),
			readOnlyPasswordKey:    []byte("current-readonly"),
			writablePasswordKey:    []byte("current-writable"),
		},
	}
}

// makeSecretWithPending returns a secret in the pending state: current keys
// plus the full *_PENDING group and ROTATION_ID.
func makeSecretWithPending(rotationID string) *corev1.Secret {
	secret := makeSecretWithCurrent()
	secret.Data[RotationIDKey] = []byte(rotationID)
	secret.Data[AdminPasswordPendingKey] = []byte("pending-admin")
	secret.Data[AgentPasswordPendingKey] = []byte("pending-agent")
	secret.Data[ReplicationPasswordPendingKey] = []byte("pending-repl")
	secret.Data[CloneDonorPasswordPendingKey] = []byte("pending-donor")
	secret.Data[ExporterPasswordPendingKey] = []byte("pending-exporter")
	secret.Data[BackupPasswordPendingKey] = []byte("pending-backup")
	secret.Data[ReadOnlyPasswordPendingKey] = []byte("pending-readonly")
	secret.Data[WritablePasswordPendingKey] = []byte("pending-writable")
	return secret
}

// makeSecretWithOld returns a secret in the promoted state: current keys
// plus the full *_OLD group and ROTATION_ID.
func makeSecretWithOld(rotationID string) *corev1.Secret {
	secret := makeSecretWithCurrent()
	secret.Data[RotationIDKey] = []byte(rotationID)
	secret.Data[AdminPasswordOldKey] = []byte("old-admin")
	secret.Data[AgentPasswordOldKey] = []byte("old-agent")
	secret.Data[ReplicationPasswordOldKey] = []byte("old-repl")
	secret.Data[CloneDonorPasswordOldKey] = []byte("old-donor")
	secret.Data[ExporterPasswordOldKey] = []byte("old-exporter")
	secret.Data[BackupPasswordOldKey] = []byte("old-backup")
	secret.Data[ReadOnlyPasswordOldKey] = []byte("old-readonly")
	secret.Data[WritablePasswordOldKey] = []byte("old-writable")
	return secret
}
