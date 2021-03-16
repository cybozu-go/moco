package constants

// label keys and values
const (
	LabelAppInstance  = "app.kubernetes.io/instance"
	LabelAppNamespace = "app.kubernetes.io/instance-namespace"
	LabelAppName      = "app.kubernetes.io/name"
	AppName           = "mysql"
	LabelAppComponent = "app.kubernetes.io/component"
	ComponentMySQLD   = "mysqld"
	ComponentBackup   = "backup"

	LabelMocoRole = "moco.cybozu.com/role"
	RolePrimary   = "primary"
	RoleReplica   = "replica"
)

// annotation keys and values
const (
	SecretVersionAnnKey = "moco.cybozu.com/secret-version"
)
