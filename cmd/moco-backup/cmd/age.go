package cmd

import (
	"fmt"
	"os"
	"strings"

	"filippo.io/age"
	"github.com/cybozu-go/moco/backup"
	"github.com/cybozu-go/moco/pkg/constants"
)

// backupOptions returns the options for backups from the environment.
func backupOptions() ([]backup.BackupOption, error) {
	v := os.Getenv(constants.AgeRecipientsEnv)
	if v == "" {
		return nil, nil
	}
	recipients, err := age.ParseRecipients(strings.NewReader(v))
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", constants.AgeRecipientsEnv, err)
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("invalid %s: no recipients", constants.AgeRecipientsEnv)
	}
	return []backup.BackupOption{backup.WithAgeRecipients(recipients...)}, nil
}

// restoreOptions returns the options for restoration from the environment.
func restoreOptions() ([]backup.RestoreOption, error) {
	v := os.Getenv(constants.AgeIdentityEnv)
	if v == "" {
		return nil, nil
	}
	identities, err := age.ParseIdentities(strings.NewReader(v))
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", constants.AgeIdentityEnv, err)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("invalid %s: no identities", constants.AgeIdentityEnv)
	}
	return []backup.RestoreOption{backup.WithAgeIdentities(identities...)}, nil
}
