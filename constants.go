package moco

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	// OperatorUser is a name of MOCO operator user in the MySQL context.
	OperatorUser = "moco"

	// OperatorAdminUser is a name of MOCO operator-admin user in the MySQL context.
	// This user is a super user especially for creating and granting privileges to other users.
	OperatorAdminUser = "moco-admin"

	// ReplicationUser is a name of MOCO replicator user in the MySQL context.
	ReplicationUser = "moco-repl"

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

	// ReplicationSourceSecretPath is the path to replication source secret file
	ReplicationSourceSecretPath = MySQLDataPath + "/replication-source-secret"

	// InnoDBBufferPoolRatioPercent is the ratio of InnoDB buffer pool size to resource.limits.memory or resource.requests.memory
	// Note that the pool size doesn't set to lower than 128MiB, which is the default innodb_buffer_pool_size value
	InnoDBBufferPoolRatioPercent = 70
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

	// CloneDonorPasswordKey is a Secret key for operator donor password.
	CloneDonorPasswordKey = "CLONE_DONOR_PASSWORD"

	// MiscPasswordKey is a Secret key for misc user password.
	MiscPasswordKey = "MISC_PASSWORD"

	// ReplicationSourcePrimaryHostKey etc. are Secret key for replication source secret
	ReplicationSourcePrimaryHostKey            = "PRIMARY_HOST"
	ReplicationSourcePrimaryUserKey            = "PRIMARY_USER"
	ReplicationSourcePrimaryPasswordKey        = "PRIMARY_PASSWORD"
	ReplicationSourcePrimaryPortKey            = "PRIMARY_PORT"
	ReplicationSourceInitAfterCloneUserKey     = "INIT_AFTER_CLONE_USER"
	ReplicationSourceInitAfterClonePasswordKey = "INIT_AFTER_CLONE_PASSWORD"
)

const (
	// InitializedClusterIndexField is an index name for Initialized MySQL Clusters
	InitializedClusterIndexField = ".status.conditions.type.initialized"
)

const (
	MyName       = "moco"
	AppName      = "moco-mysql"
	ClusterKey   = "app.kubernetes.io/instance"
	ManagedByKey = "app.kubernetes.io/managed-by"
	AppNameKey   = "app.kubernetes.io/name"

	RoleKey     = "moco.cybozu.com/role"
	PrimaryRole = "primary"
	ReplicaRole = "replica"

	MysqldContainerName = "mysqld"
)

const (
	CloneParamDonorHostName = "donor_hostname"
	CloneParamDonorPort     = "donor_port"
	CloneParamExternal      = "external"
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

type OperationPhase string

const (
	PhaseInitializing    = OperationPhase("initializing")
	PhaseWaitRelayLog    = OperationPhase("wait-relay-log")
	PhaseRestoreInstance = OperationPhase("restoring-instance")
	PhaseCompleted       = OperationPhase("completed")
)

var AllOperationPhases = []OperationPhase{
	PhaseInitializing,
	PhaseWaitRelayLog,
	PhaseRestoreInstance,
	PhaseCompleted,
}

var (
	// ErrConstraintsViolation is returned when the constraints violation occurs
	ErrConstraintsViolation = errors.New("constraints violation occurs")
	// ErrConstraintsRecovered is returned when the constrains recovered but once violated
	ErrConstraintsRecovered = errors.New("constrains recovered but once violated")
	// ErrCannotCompareGITDs is returned if GTID comparison returns error
	ErrCannotCompareGITDs = errors.New("cannot compare gtids")
)

type MOCOEvent struct {
	Type    string
	Reason  string
	Message string
}

func (e MOCOEvent) FillVariables(val ...interface{}) *MOCOEvent {
	e.Message = fmt.Sprintf(e.Message, val...)
	return &e
}

var (
	EventInitializationSucceeded = MOCOEvent{
		corev1.EventTypeNormal,
		"Initialization Succeeded",
		"Initialization phase finished successfully.",
	}
	EventInitializationFailed = MOCOEvent{
		corev1.EventTypeWarning,
		"Initialization Failed",
		"Initialization phase failed. err=%s",
	}
	EventWaitingAllInstancesAvailable = MOCOEvent{
		corev1.EventTypeNormal,
		"Waiting All Instances Available",
		"Waiting for all instances to become connected from MOCO. unavailable=%v",
	}
	EventViolationOccurred = MOCOEvent{
		corev1.EventTypeWarning,
		"Violation Occurred",
		"Constraint violation occurred. Please resolve via manual operation. err=%v",
	}
	EventWatingRelayLogExecution = MOCOEvent{
		corev1.EventTypeNormal,
		"Waiting Relay Log Execution",
		"Waiting relay log execution on replica instance(s).",
	}
	EventWaitingCloneFromExternal = MOCOEvent{
		corev1.EventTypeNormal,
		"Waiting External Clone",
		"Waiting for the intermediate primary to clone from the external primary",
	}
	EventRestoringReplicaInstances = MOCOEvent{
		corev1.EventTypeNormal,
		"Restoring Replica Instance(s)",
		"Restoring replica instance(s) by cloning with primary instance.",
	}
	EventPrimaryChanged = MOCOEvent{
		corev1.EventTypeNormal,
		"Primary Changed",
		"Primary instance was changed from %s to %s because of failover or switchover.",
	}
	EventIntermediatePrimaryConfigured = MOCOEvent{
		corev1.EventTypeNormal, "Intermediate Primary Configured",
		"Intermediate primary instance was configured with host=%s",
	}
	EventIntermediatePrimaryUnset = MOCOEvent{
		corev1.EventTypeNormal, "Intermediate Primary Unset",
		"Intermediate primary instance was unset.",
	}
	EventClusteringCompletedSynced = MOCOEvent{
		corev1.EventTypeNormal,
		"Clustering Completed and Synced",
		"Clustering are completed. All instances are synced.",
	}
	EventClusteringCompletedNotSynced = MOCOEvent{
		corev1.EventTypeWarning,
		"Clustering Completed but Not Synced",
		"Clustering are completed. Some instance(s) are not synced. out_of_sync=%v",
	}
)
