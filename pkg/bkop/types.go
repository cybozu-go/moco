package bkop

// ServerStatus defines a struct to retrieve the backup source server status.
// These information will be used in the next backup to retrieve binary logs
// since the last backup.
type ServerStatus struct {
	SuperReadOnly bool   `db:"@@super_read_only"`
	UUID          string `db:"@@server_uuid"`
	CurrentBinlog string
}

type showMasterStatus struct {
	File            string `db:"File"`
	Position        int64  `db:"Position"`
	BinlogDoDB      string `db:"Binlog_Do_DB"`
	BinlogIgnoreDB  string `db:"Binlog_Ignore_DB"`
	ExecutedGTIDSet string `db:"Executed_Gtid_Set"`
}

type showBinaryLogs struct {
	LogName   string `db:"Log_name"`
	FileSize  int64  `db:"File_size"`
	Encrypted string `db:"Encrypted"`
}
