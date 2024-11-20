package constants

// label keys and values
const (
	LabelAppInstance  = "app.kubernetes.io/instance"
	LabelAppNamespace = "app.kubernetes.io/instance-namespace"
	LabelAppName      = "app.kubernetes.io/name"
	AppNameMySQL      = "mysql"
	AppNameBackup     = "mysql-backup"
	LabelAppCreatedBy = "app.kubernetes.io/created-by"
	AppCreator        = "moco"

	LabelMocoRole = "moco.cybozu.com/role"
	RolePrimary   = "primary"
	RoleReplica   = "replica"
)

// annotation keys and values
const (
	AnnDemote                = "moco.cybozu.com/demote"
	AnnSecretVersion         = "moco.cybozu.com/secret-version"
	AnnClusteringStopped     = "moco.cybozu.com/clustering-stopped"
	AnnReconciliationStopped = "moco.cybozu.com/reconciliation-stopped"
	AnnForceRollingUpdate    = "moco.cybozu.com/force-rolling-update"
	AnnPreventDelete         = "moco.cybozu.com/prevent-delete"
)

// MySQLClusterFinalizer is the finalizer specifier for MySQLCluster.
const MySQLClusterFinalizer = "moco.cybozu.com/mysqlcluster"
