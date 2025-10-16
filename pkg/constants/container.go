package constants

// container names
const (
	AgentContainerName             = "agent"
	InitContainerName              = "moco-init"
	CopyInitContainerName          = "copy-moco-init"
	MysqldContainerName            = "mysqld"
	SlowQueryLogAgentContainerName = "slow-log"
	ExporterContainerName          = "mysqld-exporter"
)

// container resources
const (
	AgentContainerCPURequest = "100m"
	AgentContainerCPULimit   = "100m"
	AgentContainerMemRequest = "100Mi"
	AgentContainerMemLimit   = "100Mi"

	InitContainerCPURequest = "100m"
	InitContainerCPULimit   = "1000m" // mysql data directory process is CPU intensive, with 100m it takes ~60 seconds, with 1000m it takes ~6 seconds
	InitContainerMemRequest = "512Mi"
	InitContainerMemLimit   = "512Mi"

	SlowQueryLogAgentCPURequest = "100m"
	SlowQueryLogAgentCPULimit   = "100m"
	SlowQueryLogAgentMemRequest = "20Mi"
	SlowQueryLogAgentMemLimit   = "20Mi"

	ExporterContainerCPURequest = "200m"
	ExporterContainerCPULimit   = "200m"
	ExporterContainerMemRequest = "100Mi"
	ExporterContainerMemLimit   = "100Mi"
)

// volume names
const (
	MySQLDataVolumeName               = "mysql-data"
	MySQLConfVolumeName               = "mysql-conf"
	MySQLInitConfVolumeName           = "mysql-conf-d"
	MySQLConfSecretVolumeName         = "my-cnf-secret"
	GRPCSecretVolumeName              = "grpc-cert"
	RunVolumeName                     = "run"
	VarLogVolumeName                  = "var-log"
	TmpVolumeName                     = "tmp"
	SlowQueryLogAgentConfigVolumeName = "slow-fluent-bit-config"
	SharedVolumeName                  = "shared"
)

// UID/GID
const (
	ContainerUID = 10000
	ContainerGID = 10000
)

// command names
const (
	InitCommand = "moco-init"
)

// PreStop sleep duration
const PreStopSeconds = "20"
