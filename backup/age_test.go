package backup

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestAgeEncryption(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	otherIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name         string
		recipients   []age.Recipient
		identities   []age.Identity
		wantFilename string
		wantErr      string
	}{
		{
			name:         "without encryption",
			wantFilename: "dump.tar",
		},
		{
			name:         "without encryption, with identities",
			identities:   []age.Identity{identity},
			wantFilename: "dump.tar",
		},
		{
			name:         "with encryption",
			recipients:   []age.Recipient{identity.Recipient()},
			identities:   []age.Identity{otherIdentity, identity},
			wantFilename: "dump.tar.age",
		},
		{
			name:         "with encryption to several recipients",
			recipients:   []age.Recipient{otherIdentity.Recipient(), identity.Recipient()},
			identities:   []age.Identity{identity},
			wantFilename: "dump.tar.age",
		},
		{
			name:         "with encryption, without identities",
			recipients:   []age.Recipient{identity.Recipient()},
			wantFilename: "dump.tar.age",
			wantErr:      "no age identity is configured",
		},
		{
			name:         "with encryption, with another identity",
			recipients:   []age.Recipient{identity.Recipient()},
			identities:   []age.Identity{otherIdentity},
			wantFilename: "dump.tar.age",
			wantErr:      "none of the configured age identities can decrypt",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			bm := &BackupManager{}
			WithAgeRecipients(tt.recipients...)(bm)
			rm := &RestoreManager{}
			WithAgeIdentities(tt.identities...)(rm)

			filename := bm.filename("dump.tar")
			if filename != tt.wantFilename {
				t.Errorf("want filename %s, got %s", tt.wantFilename, filename)
			}

			plaintext := strings.Repeat("backup data ", 10000)
			r, err := bm.encrypt(strings.NewReader(plaintext))
			if err != nil {
				t.Fatalf("encrypt failed: %v", err)
			}
			stored, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("failed to read encrypted data: %v", err)
			}
			encrypted := len(tt.recipients) > 0
			if got := bytes.HasPrefix(stored, []byte("age-encryption.org/v1\n")); got != encrypted {
				t.Errorf("want encrypted %v, got %v", encrypted, got)
			}

			dr, err := rm.decrypt("moco/ns/name/20260101-000000/"+filename, bytes.NewReader(stored))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decrypt failed: %v", err)
			}
			got, err := io.ReadAll(dr)
			if err != nil {
				t.Fatalf("failed to read decrypted data: %v", err)
			}
			if string(got) != plaintext {
				t.Error("decrypted data differs from the plaintext")
			}
		})
	}
}

func TestAgeDecryptCorrupted(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	bm := &BackupManager{ageRecipients: []age.Recipient{identity.Recipient()}}
	rm := &RestoreManager{ageIdentities: []age.Identity{identity}}

	r, err := bm.encrypt(strings.NewReader(strings.Repeat("x", 100000)))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	stored[len(stored)-1] ^= 0xff

	dr, err := rm.decrypt("dump.tar.age", bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("decrypt failed before reading: %v", err)
	}
	if _, err := io.ReadAll(dr); err == nil {
		t.Error("want an error when reading corrupted data")
	}
}
