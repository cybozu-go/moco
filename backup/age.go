package backup

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
	"github.com/cybozu-go/moco/pkg/constants"
)

// BackupOption is an option for NewBackupManager.
type BackupOption func(*BackupManager)

// WithAgeRecipients makes the BackupManager encrypt backup files to the
// given age recipients.  Encrypted files get the suffix constants.AgeSuffix.
func WithAgeRecipients(recipients ...age.Recipient) BackupOption {
	return func(bm *BackupManager) {
		bm.ageRecipients = recipients
	}
}

// RestoreOption is an option for NewRestoreManager.
type RestoreOption func(*RestoreManager)

// WithAgeIdentities makes the RestoreManager decrypt backup files that were
// encrypted with age, using the given identities.
func WithAgeIdentities(identities ...age.Identity) RestoreOption {
	return func(rm *RestoreManager) {
		rm.ageIdentities = identities
	}
}

// filename returns the name of a backup file to upload.
func (bm *BackupManager) filename(name string) string {
	if len(bm.ageRecipients) > 0 {
		return name + constants.AgeSuffix
	}
	return name
}

// encrypt returns a reader that encrypts r if age recipients are configured.
func (bm *BackupManager) encrypt(r io.Reader) (io.Reader, error) {
	if len(bm.ageRecipients) == 0 {
		return r, nil
	}
	er, err := age.EncryptReader(r, bm.ageRecipients...)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt with age: %w", err)
	}
	return er, nil
}

// decrypt returns a reader that decrypts r if key names a file encrypted with age.
func (rm *RestoreManager) decrypt(key string, r io.Reader) (io.Reader, error) {
	if !strings.HasSuffix(key, constants.AgeSuffix) {
		return r, nil
	}
	if len(rm.ageIdentities) == 0 {
		return nil, fmt.Errorf("%s is encrypted with age, but no age identity is configured", key)
	}
	dr, err := age.Decrypt(r, rm.ageIdentities...)
	if err != nil {
		if _, ok := errors.AsType[*age.NoIdentityMatchError](err); ok {
			return nil, fmt.Errorf("none of the configured age identities can decrypt %s: %w", key, err)
		}
		return nil, fmt.Errorf("failed to decrypt %s with age: %w", key, err)
	}
	return dr, nil
}
