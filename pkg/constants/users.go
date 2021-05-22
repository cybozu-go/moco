package constants

// MySQL user names for MOCO
const (
	AdminUser       = "moco-admin"
	AgentUser       = "moco-agent"
	ReplicationUser = "moco-repl"
	CloneDonorUser  = "moco-clone-donor"
	ExporterUser    = "moco-exporter"
	BackupUser      = "moco-backup"
	ReadOnlyUser    = "moco-readonly"
	WritableUser    = "moco-writable"
)

// MocoSystemUsers is a map to hold system users.
// This does not include moco-readonly and moco-writable as they are for end users.
var MocoSystemUsers = map[string]bool{
	AdminUser:       true,
	AgentUser:       true,
	ReplicationUser: true,
	CloneDonorUser:  true,
	ExporterUser:    true,
	BackupUser:      true,
}

// MocoUsers is a list of all users created for MOCO.
var MocoUsers = []string{
	AdminUser,
	AgentUser,
	ReplicationUser,
	CloneDonorUser,
	ExporterUser,
	BackupUser,
	ReadOnlyUser,
	WritableUser,
}

// my.cnf filenames for different kind of users.
const (
	AdminMyCnf    = AdminUser + "-my.cnf"
	ExporterMyCnf = ExporterUser + "-my.cnf"
	BackupMyCnf   = BackupUser + "-my.cnf"
	ReadOnlyMyCnf = ReadOnlyUser + "-my.cnf"
	WritableMyCnf = WritableUser + "-my.cnf"
)
