package dbop

import (
	"database/sql"
)

type AccessInfo struct {
	Host     string `db:"Host"`
	Port     int    `db:"Port"`
	User     string `db:"User"`
	Password string `db:"Password"`
}

// MySQLInstanceStatus defines the observed state of a MySQL instance
type MySQLInstanceStatus struct {
	IsErrant        bool
	GlobalVariables GlobalVariables
	GlobalStatus    *GlobalStatus
	ReplicaHosts    []ReplicaHost
	ReplicaStatus   *ReplicaStatus // may not be available
	CloneStatus     *CloneStatus   // may not be available
}

var statusGlobalVars = []string{
	"@@server_uuid",
	"@@gtid_executed",
	"@@gtid_purged",
	"@@read_only",
	"@@super_read_only",
	"@@rpl_semi_sync_master_wait_for_slave_count",
	"@@rpl_semi_sync_master_enabled",
	"@@rpl_semi_sync_slave_enabled",
}

// GlobalVariables defines the observed global variable values of a MySQL instance
type GlobalVariables struct {
	UUID                  string `db:"@@server_uuid"`
	ExecutedGTID          string `db:"@@gtid_executed"`
	PurgedGTID            string `db:"@@gtid_purged"`
	ReadOnly              bool   `db:"@@read_only"`
	SuperReadOnly         bool   `db:"@@super_read_only"`
	WaitForSlaveCount     int    `db:"@@rpl_semi_sync_master_wait_for_slave_count"`
	SemiSyncMasterEnabled bool   `db:"@@rpl_semi_sync_master_enabled"`
	SemiSyncSlaveEnabled  bool   `db:"@@rpl_semi_sync_slave_enabled"`
}

type GlobalStatus struct {
	SemiSyncMasterWaitSessions int
}

// ReplicaHost defines the columns from `SHOW REPLICAS`
type ReplicaHost struct {
	ServerID    int32  `db:"Server_Id"`
	Host        string `db:"Host"`
	Port        int    `db:"Port"`
	SourceID    int32  `db:"Source_Id"`
	ReplicaUUID string `db:"Replica_UUID"`

	// the following fields don't appear normally
	User     string `db:"User"`
	Password string `db:"Password"`
}

// ReplicaStatus defines the observed state of a replica
type ReplicaStatus struct {
	LastIoErrno       int    `db:"Last_IO_Errno"`
	LastIoError       string `db:"Last_IO_Error"`
	LastSQLErrno      int    `db:"Last_SQL_Errno"`
	LastSQLError      string `db:"Last_SQL_Error"`
	SourceHost        string `db:"Source_Host"`
	RetrievedGtidSet  string `db:"Retrieved_Gtid_Set"`
	ExecutedGtidSet   string `db:"Executed_Gtid_Set"`
	ReplicaIORunning  string `db:"Replica_IO_Running"`
	ReplicaSQLRunning string `db:"Replica_SQL_Running"`

	// All of variables from here are NOT used in MOCO's reconcile
	ReplicaIOState            string        `db:"Replica_IO_State"`
	SourceUser                string        `db:"Source_User"`
	SourcePort                int           `db:"Source_Port"`
	ConnectRetry              int           `db:"Connect_Retry"`
	SourceLogFile             string        `db:"Source_Log_File"`
	ReadSourceLogPos          int           `db:"Read_Source_Log_Pos"`
	RelayLogFile              string        `db:"Relay_Log_File"`
	RelayLogPos               int           `db:"Relay_Log_Pos"`
	RelaySourceLogFile        string        `db:"Relay_Source_Log_File"`
	ReplicateDoDB             string        `db:"Replicate_Do_DB"`
	ReplicateIgnoreDB         string        `db:"Replicate_Ignore_DB"`
	ReplicateDoTable          string        `db:"Replicate_Do_Table"`
	ReplicateIgnoreTable      string        `db:"Replicate_Ignore_Table"`
	ReplicateWildDoTable      string        `db:"Replicate_Wild_Do_Table"`
	ReplicateWildIgnoreTable  string        `db:"Replicate_Wild_Ignore_Table"`
	LastErrno                 int           `db:"Last_Errno"`
	LastError                 string        `db:"Last_Error"`
	SkipCounter               int           `db:"Skip_Counter"`
	ExecSourceLogPos          int           `db:"Exec_Source_Log_Pos"`
	RelayLogSpace             int           `db:"Relay_Log_Space"`
	UntilCondition            string        `db:"Until_Condition"`
	UntilLogFile              string        `db:"Until_Log_File"`
	UntilLogPos               int           `db:"Until_Log_Pos"`
	SourceSSLAllowed          string        `db:"Source_SSL_Allowed"`
	SourceSSLCAFile           string        `db:"Source_SSL_CA_File"`
	SourceSSLCAPath           string        `db:"Source_SSL_CA_Path"`
	SourceSSLCert             string        `db:"Source_SSL_Cert"`
	SourceSSLCipher           string        `db:"Source_SSL_Cipher"`
	SourceSSLKey              string        `db:"Source_SSL_Key"`
	SecondsBehindSource       sql.NullInt64 `db:"Seconds_Behind_Source"`
	SourceSSLVerifyServerCert string        `db:"Source_SSL_Verify_Server_Cert"`
	ReplicateIgnoreServerIds  string        `db:"Replicate_Ignore_Server_Ids"`
	SourceServerID            int           `db:"Source_Server_Id"`
	SourceUUID                string        `db:"Source_UUID"`
	SourceInfoFile            string        `db:"Source_Info_File"`
	SQLDelay                  int           `db:"SQL_Delay"`
	SQLRemainingDelay         sql.NullInt64 `db:"SQL_Remaining_Delay"`
	ReplicaSQLRunningState    string        `db:"Replica_SQL_Running_State"`
	SourceRetryCount          int           `db:"Source_Retry_Count"`
	SourceBind                string        `db:"Source_Bind"`
	LastIOErrorTimestamp      string        `db:"Last_IO_Error_Timestamp"`
	LastSQLErrorTimestamp     string        `db:"Last_SQL_Error_Timestamp"`
	SourceSSLCrl              string        `db:"Source_SSL_Crl"`
	SourceSSLCrlpath          string        `db:"Source_SSL_Crlpath"`
	AutoPosition              string        `db:"Auto_Position"`
	ReplicateRewriteDB        string        `db:"Replicate_Rewrite_DB"`
	ChannelName               string        `db:"Channel_Name"`
	SourceTLSVersion          string        `db:"Source_TLS_Version"`
	Sourcepublickeypath       string        `db:"Source_public_key_path"`
	GetSourcepublickey        string        `db:"Get_Source_public_key"`
	NetworkNamespace          string        `db:"Network_Namespace"`
}

func (rs *ReplicaStatus) IsRunning() bool {
	if rs == nil {
		return false
	}
	return rs.ReplicaIORunning == "Yes" && rs.ReplicaSQLRunning == "Yes"
}

// CloneStatus defines the observed clone status of a MySQL instance
type CloneStatus struct {
	State sql.NullString `db:"state"`
}

// Process represents a process in `information_schema.PROCESSLIST` table.
type Process struct {
	ID   uint64 `db:"ID"`
	User string `db:"USER"`
	Host string `db:"HOST"`
}
