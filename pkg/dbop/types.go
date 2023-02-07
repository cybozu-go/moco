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
	ReplicaHosts    []ReplicaHost
	ReplicaStatus   *ReplicaStatus // may not be available
	CloneStatus     *CloneStatus   // may not be available
}

var statusGlobalVars = []string{
	"@@server_uuid",
	"@@gtid_executed",
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
	ReadOnly              bool   `db:"@@read_only"`
	SuperReadOnly         bool   `db:"@@super_read_only"`
	WaitForSlaveCount     int    `db:"@@rpl_semi_sync_master_wait_for_slave_count"`
	SemiSyncMasterEnabled bool   `db:"@@rpl_semi_sync_master_enabled"`
	SemiSyncSlaveEnabled  bool   `db:"@@rpl_semi_sync_slave_enabled"`
}

// ReplicaHost defines the columns from `SHOW SLAVE HOSTS`
type ReplicaHost struct {
	ServerID    int32  `db:"Server_id"`
	Host        string `db:"Host"`
	Port        int    `db:"Port"`
	SourceID    int32  `db:"Master_id"`
	ReplicaUUID string `db:"Slave_UUID"`

	// the following fields don't appear normally
	User     string `db:"User"`
	Password string `db:"Password"`
}

// ReplicaStatus defines the observed state of a replica
type ReplicaStatus struct {
	LastIoErrno      int    `db:"Last_IO_Errno"`
	LastIoError      string `db:"Last_IO_Error"`
	LastSQLErrno     int    `db:"Last_SQL_Errno"`
	LastSQLError     string `db:"Last_SQL_Error"`
	MasterHost       string `db:"Master_Host"`
	RetrievedGtidSet string `db:"Retrieved_Gtid_Set"`
	ExecutedGtidSet  string `db:"Executed_Gtid_Set"`
	SlaveIORunning   string `db:"Slave_IO_Running"`
	SlaveSQLRunning  string `db:"Slave_SQL_Running"`

	// All of variables from here are NOT used in MOCO's reconcile
	SlaveIOState              string        `db:"Slave_IO_State"`
	MasterUser                string        `db:"Master_User"`
	MasterPort                int           `db:"Master_Port"`
	ConnectRetry              int           `db:"Connect_Retry"`
	MasterLogFile             string        `db:"Master_Log_File"`
	ReadMasterLogPos          int           `db:"Read_Master_Log_Pos"`
	RelayLogFile              string        `db:"Relay_Log_File"`
	RelayLogPos               int           `db:"Relay_Log_Pos"`
	RelayMasterLogFile        string        `db:"Relay_Master_Log_File"`
	ReplicateDoDB             string        `db:"Replicate_Do_DB"`
	ReplicateIgnoreDB         string        `db:"Replicate_Ignore_DB"`
	ReplicateDoTable          string        `db:"Replicate_Do_Table"`
	ReplicateIgnoreTable      string        `db:"Replicate_Ignore_Table"`
	ReplicateWildDoTable      string        `db:"Replicate_Wild_Do_Table"`
	ReplicateWildIgnoreTable  string        `db:"Replicate_Wild_Ignore_Table"`
	LastErrno                 int           `db:"Last_Errno"`
	LastError                 string        `db:"Last_Error"`
	SkipCounter               int           `db:"Skip_Counter"`
	ExecMasterLogPos          int           `db:"Exec_Master_Log_Pos"`
	RelayLogSpace             int           `db:"Relay_Log_Space"`
	UntilCondition            string        `db:"Until_Condition"`
	UntilLogFile              string        `db:"Until_Log_File"`
	UntilLogPos               int           `db:"Until_Log_Pos"`
	MasterSSLAllowed          string        `db:"Master_SSL_Allowed"`
	MasterSSLCAFile           string        `db:"Master_SSL_CA_File"`
	MasterSSLCAPath           string        `db:"Master_SSL_CA_Path"`
	MasterSSLCert             string        `db:"Master_SSL_Cert"`
	MasterSSLCipher           string        `db:"Master_SSL_Cipher"`
	MasterSSLKey              string        `db:"Master_SSL_Key"`
	SecondsBehindMaster       sql.NullInt64 `db:"Seconds_Behind_Master"`
	MasterSSLVerifyServerCert string        `db:"Master_SSL_Verify_Server_Cert"`
	ReplicateIgnoreServerIds  string        `db:"Replicate_Ignore_Server_Ids"`
	MasterServerID            int           `db:"Master_Server_Id"`
	MasterUUID                string        `db:"Master_UUID"`
	MasterInfoFile            string        `db:"Master_Info_File"`
	SQLDelay                  int           `db:"SQL_Delay"`
	SQLRemainingDelay         sql.NullInt64 `db:"SQL_Remaining_Delay"`
	SlaveSQLRunningState      string        `db:"Slave_SQL_Running_State"`
	MasterRetryCount          int           `db:"Master_Retry_Count"`
	MasterBind                string        `db:"Master_Bind"`
	LastIOErrorTimestamp      string        `db:"Last_IO_Error_Timestamp"`
	LastSQLErrorTimestamp     string        `db:"Last_SQL_Error_Timestamp"`
	MasterSSLCrl              string        `db:"Master_SSL_Crl"`
	MasterSSLCrlpath          string        `db:"Master_SSL_Crlpath"`
	AutoPosition              string        `db:"Auto_Position"`
	ReplicateRewriteDB        string        `db:"Replicate_Rewrite_DB"`
	ChannelName               string        `db:"Channel_Name"`
	MasterTLSVersion          string        `db:"Master_TLS_Version"`
	Masterpublickeypath       string        `db:"Master_public_key_path"`
	Getmasterpublickey        string        `db:"Get_master_public_key"`
	NetworkNamespace          string        `db:"Network_Namespace"`
}

func (rs *ReplicaStatus) IsRunning() bool {
	if rs == nil {
		return false
	}
	return rs.SlaveIORunning == "Yes" && rs.SlaveSQLRunning == "Yes"
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
