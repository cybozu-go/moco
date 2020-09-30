package moco

import (
	"errors"
)

const (
	// OperatorUser is a name of MOCO operator user in the MySQL context.
	OperatorUser = "moco"

	// OperatorAdminUser is a name of MOCO operator-admin user in the MySQL context.
	// This user is a super user especially for creating and granting privileges to other users.
	OperatorAdminUser = "moco-admin"

	// ReplicatorUser is a name of MOCO replicator user in the MySQL context.
	ReplicatorUser = "moco-repl"

	// DonorUser is a name of MOCO clone-donor user in the MySQL context.
	DonorUser = "moco-clone-donor"

	// MiscUser is a name of MOCO misc user in the MySQL context.
	MiscUser = "misc"
)

const (
	// MySQLDataPath is a path for MySQL data dir.
	MySQLDataPath = "/var/lib/mysql"

	// MySQLConfPath is a path for MySQL conf dir.
	MySQLConfPath = "/etc/mysql"

	// MySQLConfName is a filename for MySQL conf.
	MySQLConfName = "my.cnf"

	// VarRunPath is a path for variable files which concerns MySQLd.
	VarRunPath = "/var/run/mysqld"

	// VarLogPath is a path for /var/log/mysql.
	VarLogPath = "/var/log/mysql"

	// MySQLErrorLogName is a filename of error log for MySQL.
	MySQLErrorLogName = "mysql.err"

	// MySQLSlowLogName is a filename of slow query log for MySQL.
	MySQLSlowLogName = "mysql.slow"

	// TmpPath is a path for /tmp.
	TmpPath = "/tmp"

	// MySQLConfTemplatePath is
	MySQLConfTemplatePath = "/etc/mysql_template"

	// DonorPasswordPath is the path to donor user passsword file
	DonorPasswordPath = MySQLDataPath + "/donor-password"

	// MiscPasswordPath is the path to misc user passsword file
	MiscPasswordPath = MySQLDataPath + "/misc-password"
)

const (
	// MySQLPort is a port number for MySQL
	MySQLPort = 3306

	// MySQLAdminPort is a port number for MySQL Admin
	MySQLAdminPort = 33062

	// MySQLXPort is a port number for MySQL XProtocol
	MySQLXPort = 33060
)

const (
	// AgentPort is a port number for agent container
	AgentPort = 9080

	// AgentTokenEnvName is a name of the environment variable of agent token.
	AgentTokenEnvName = "MOCO_AGENT_TOKEN"

	// AgentTokenParam is a name of the param of agent token.
	AgentTokenParam = "token"
)

// env names must correspond to options in entrypoint/init.go
const (
	// PodNameEnvName is a name of the environment variable of a pod name.
	PodNameEnvName = "POD_NAME"

	// PodNameFlag is a name of the flag of a pod name.
	PodNameFlag = "pod-name"

	// PodNamespaceEnvName is a name of the environment variable of a pod namespace.
	PodNamespaceEnvName = "POD_NAMESPACE"

	// PodNamespaceFlag is a name of the flag of a pod namespace.
	PodNamespaceFlag = "pod-namespace"

	// PodIPEnvName is a name of the environment variable of a pod IP.
	PodIPEnvName = "POD_IP"

	// PodNameFlag is a name of the flag of a pod IP.
	PodIPFlag = "pod-ip"

	// NodeNameEnvName is a name of the environment variable of a node name where the pod runs.
	NodeNameEnvName = "NODE_NAME"

	// NodeNameFlag is a name of the flag of a node name where the pod runs.
	NodeNameFlag = "node-name"

	// RootPasswordEnvName is a name of the environment variable of a root password.
	RootPasswordEnvName = "ROOT_PASSWORD"

	// OperatorPasswordEnvName is a name of the environment variable of a password for both operator and operator-admin.
	OperatorPasswordEnvName = "OPERATOR_PASSWORD"

	// ReplicationPasswordEnvName is a name of the environment variable of a password for replication user.
	ReplicationPasswordEnvName = "REPLICATION_PASSWORD"

	// ClonePasswordEnvName is a name of the environment variable of a password for donor user.
	ClonePasswordEnvName = "CLONE_DONOR_PASSWORD"

	// MiscPasswordEnvName is a name of the environment variable of a password for the misc user.
	MiscPasswordEnvName = "MISC_PASSWORD"
)

const (
	// RootPasswordKey is a Secret key for root password.
	RootPasswordKey = "ROOT_PASSWORD"

	// OperatorPasswordKey is a Secret key for operator password.
	OperatorPasswordKey = "OPERATOR_PASSWORD"

	// ReplicationPasswordKey is a Secret key for operator replication password.
	ReplicationPasswordKey = "REPLICATION_PASSWORD"

	// DonorPasswordKey is a Secret key for operator donor password.
	DonorPasswordKey = "CLONE_DONOR_PASSWORD"

	// MiscPasswordKey is a Secret key for misc user password.
	MiscPasswordKey = "MISC_PASSWORD"
)

const (
	// InitializedClusterIndexField is an index name for Initialized MySQL Clusters
	InitializedClusterIndexField = ".status.conditions.type.initialized"
)

const (
	MyName          = "moco"
	AppNameKey      = "app.kubernetes.io/name"
	AppManagedByKey = "app.kubernetes.io/managed-by"

	RoleKey     = "moco.cybozu.com/role"
	PrimaryRole = "primary"
	ReplicaRole = "replica"

	MysqldContainerName = "mysqld"
)

const (
	CloneParamDonorHostName = "donor_hostname"
	CloneParamDonorPort     = "donor_port"
)

const (
	ReplicaRunConnect     = "Yes"
	ReplicaNotRun         = "No"
	ReplicaRunNotConnect  = "Connecting"
	CloneStatusNotStarted = "Not Started"
	CloneStatusInProgress = "In Progress"
	CloneStatusCompleted  = "Completed"
	CloneStatusFailed     = "Failed"
)

var (
	// ErrConstraintsViolation is returned when the constraints violation occurs
	ErrConstraintsViolation = errors.New("constraints violation occurs")
	// ErrConstraintsRecovered is returned when the constrains recovered but once violated
	ErrConstraintsRecovered = errors.New("constrains recovered but once violated")
	// ErrUnavailableHost is returned at least one MySQL instance is unavailable
	ErrUnavailableHost = errors.New("unavailable host exists")
)
