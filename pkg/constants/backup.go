package constants

// moco-backup related constants
const (
	BackupSubcommand  = "backup"
	RestoreSubcommand = "restore"

	BackupTimeFormat = "20060102-150405"
	DumpFilename     = "dump.tar"
	BinlogFilename   = "binlog.tar.zst"
)

const (
	BackendTypeS3    = "s3"
	BackendTypeGCS   = "gcs"
	BackendTypeAzure = "azure"
)

// CustomerKeyEnv is the name of the environment variable that passes
// the base64-encoded customer-provided encryption key to moco-backup.
const CustomerKeyEnv = "MOCO_CUSTOMER_KEY"

// Constants for client-side encryption of backup files with age.
const (
	// AgeSuffix is appended to the names of backup files encrypted with age.
	AgeSuffix = ".age"

	// AgeRecipientsEnv is the name of the environment variable that passes
	// the age recipients (public keys), one per line, to moco-backup.
	AgeRecipientsEnv = "MOCO_AGE_RECIPIENTS"

	// AgeIdentityEnv is the name of the environment variable that passes
	// the age identities (private keys), one per line, to moco-backup.
	AgeIdentityEnv = "MOCO_AGE_IDENTITY"
)
