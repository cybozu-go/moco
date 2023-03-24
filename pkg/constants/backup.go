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
	BackendTypeS3  = "s3"
	BackendTypeGCS = "gcs"
)
