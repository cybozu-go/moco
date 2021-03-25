package constants

// container names
const (
	AgentContainerName             = "agent"
	InitContainerName              = "moco-init"
	MysqldContainerName            = "mysqld"
	ErrorLogAgentContainerName     = "error-log"
	SlowQueryLogAgentContainerName = "slow-log"
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
	ErrorLogAgentConfigVolumeName     = "error-fluent-bit-config"
	SlowQueryLogAgentConfigVolumeName = "slow-fluent-bit-config"
	MOCOBinVolumeName                 = "moco-bin"
)

// command names
const (
	InitCommand = "moco-init"
)

// PreStop sleep duration
const PreStopSeconds = "20"
