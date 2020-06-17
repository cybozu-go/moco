package moco

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

	// VarLogPath is a path for /var/log.
	VarLogPath = "/var/log"

	// TmpPath is a path for /tmp.
	TmpPath = "/tmp"
)

// env names must correspond to options in entrypoint/init.go
const (
	// PodNameEnvName is a name of the environment variable of a pod name.
	PodNameEnvName = "MYSQL_POD_NAME"

	// PodNameFlag is a name of the flag of a pod name.
	PodNameFlag = "pod-name"

	// PodIPEnvName is a name of the environment variable of a pod IP.
	PodIPEnvName = "MYSQL_POD_IP"

	// PodNameFlag is a name of the flag of a pod IP.
	PodIPFlag = "pod-ip"

	// RootPasswordEnvName is a name of the environment variable of a root password.
	RootPasswordEnvName = "MYSQL_ROOT_PASSWORD"

	// RootPasswordFlag is a name of the flag of a root password.
	RootPasswordFlag = "root-password"

	// OperatorPasswordEnvName is a name of the environment variable of a password for both operator and operator-admin.
	OperatorPasswordEnvName = "MYSQL_OPERATOR_PASSWORD"

	// OperatorPasswordFlag is a name of the flag of a password for both operator and operator-admin.
	OperatorPasswordFlag = "operator-password"
)

const (
	// RootPasswordKey is a Secret key for root password.
	RootPasswordKey = "MYSQL_ROOT_PASSWORD"

	// OperatorPasswordKey is a Secret key for operator password.
	OperatorPasswordKey = "MYSQL_OPERATOR_PASSWORD"

	// ReplicationPasswordKey is a Secret key for operator replication password.
	ReplicationPasswordKey = "MYSQL_REPLICATION_PASSWORD"

	// DonorPasswordKey is a Secret key for operator donor password.
	DonorPasswordKey = "MYSQL_CLONE_DONOR_PASSWORD"
)
