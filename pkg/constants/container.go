package constants

// container names
const (
	AgentContainerName             = "agent"
	InitContainerName              = "moco-init"
	MysqldContainerName            = "mysqld"
	SlowQueryLogAgentContainerName = "slow-log"
	ExporterContainerName          = "mysqld-exporter"
)

// volume names
const (
	MySQLDataVolumeName               = "mysql-data"
	MySQLConfVolumeName               = "mysql-conf"
	MySQLInitConfVolumeName           = "mysql-conf-d"
	MySQLConfSecretVolumeName         = "my-cnf-secret"
	RunVolumeName                     = "run"
	VarLogVolumeName                  = "var-log"
	TmpVolumeName                     = "tmp"
	SlowQueryLogAgentConfigVolumeName = "slow-fluent-bit-config"
	MOCOBinVolumeName                 = "moco-bin"
)

// command names
const (
	InitCommand = "moco-init"
)

// PreStop sleep duration
const PreStopSeconds = "20"
