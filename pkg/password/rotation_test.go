package password

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHasPendingPasswords(t *testing.T) {
	t.Run("empty secret returns false", func(t *testing.T) {
		secret := &corev1.Secret{Data: map[string][]byte{}}
		has, err := HasPendingPasswords(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if has {
			t.Error("expected false")
		}
	})

	t.Run("nil data returns false", func(t *testing.T) {
		secret := &corev1.Secret{}
		has, err := HasPendingPasswords(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if has {
			t.Error("expected false")
		}
	})

	t.Run("all pending with matching ID returns true", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		has, err := HasPendingPasswords(secret, "test-id")
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Error("expected true")
		}
	})

	t.Run("ROTATION_ID mismatch returns error", func(t *testing.T) {
		secret := makeSecretWithPending("old-id")
		_, err := HasPendingPasswords(secret, "new-id")
		if err == nil {
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
		_, err := HasPendingPasswords(secret, "test-id")
		if err == nil {
			t.Fatal("expected error for partial pending")
		}
	})

	t.Run("ROTATION_ID without pending returns error", func(t *testing.T) {
		secret := &corev1.Secret{
			Data: map[string][]byte{
				RotationIDKey: []byte("test-id"),
			},
		}
		_, err := HasPendingPasswords(secret, "test-id")
		if err == nil {
			t.Fatal("expected error for ROTATION_ID without pending")
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
					"moco.cybozu.com/secret-version": "1",
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
		_, err := SetPendingPasswords(secret, "new-id")
		if err == nil {
			t.Fatal("expected error for stale pending")
		}
	})
}

func TestNewMySQLPasswordFromPending(t *testing.T) {
	secret := makeSecretWithPending("test-id")
	pwd, err := NewMySQLPasswordFromPending(secret)
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

func TestConfirmPendingPasswords(t *testing.T) {
	t.Run("copies pending to current and removes pending", func(t *testing.T) {
		secret := makeSecretWithPending("test-id")
		secret.Data[AdminPasswordKey] = []byte("old-admin")

		pendingAdmin := string(secret.Data[AdminPasswordPendingKey])

		err := ConfirmPendingPasswords(secret)
		if err != nil {
			t.Fatal(err)
		}
		if string(secret.Data[AdminPasswordKey]) != pendingAdmin {
			t.Errorf("expected admin password to be updated to %s", pendingAdmin)
		}
		if _, ok := secret.Data[AdminPasswordPendingKey]; ok {
			t.Error("pending key should be removed")
		}
		if _, ok := secret.Data[RotationIDKey]; ok {
			t.Error("ROTATION_ID should be removed")
		}
	})

	t.Run("idempotent when no pending exists", func(t *testing.T) {
		secret := &corev1.Secret{
			Data: map[string][]byte{
				AdminPasswordKey: []byte("current"),
			},
		}
		err := ConfirmPendingPasswords(secret)
		if err != nil {
			t.Fatal(err)
		}
		if string(secret.Data[AdminPasswordKey]) != "current" {
			t.Error("current password should not be changed")
		}
	})

	t.Run("nil data is no-op", func(t *testing.T) {
		secret := &corev1.Secret{}
		err := ConfirmPendingPasswords(secret)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("partial pending returns error", func(t *testing.T) {
		secret := &corev1.Secret{
			Data: map[string][]byte{
				AdminPasswordPendingKey: []byte("pwd"),
			},
		}
		err := ConfirmPendingPasswords(secret)
		if err == nil {
			t.Fatal("expected error for partial pending")
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
		_, err := PendingKeyMap(secret)
		if err == nil {
			t.Fatal("expected error for missing pending keys")
		}
	})
}

func makeSecretWithPending(rotationID string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "ns",
		},
		Data: map[string][]byte{
			RotationIDKey:                 []byte(rotationID),
			AdminPasswordPendingKey:       []byte("pending-admin"),
			AgentPasswordPendingKey:       []byte("pending-agent"),
			ReplicationPasswordPendingKey: []byte("pending-repl"),
			CloneDonorPasswordPendingKey:  []byte("pending-donor"),
			ExporterPasswordPendingKey:    []byte("pending-exporter"),
			BackupPasswordPendingKey:      []byte("pending-backup"),
			ReadOnlyPasswordPendingKey:    []byte("pending-readonly"),
			WritablePasswordPendingKey:    []byte("pending-writable"),
		},
	}
	return secret
}
