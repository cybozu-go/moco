package accessor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/jmoiron/sqlx"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MySQLClusterStatus defines the observed state of MySQLCluster
type MySQLClusterStatus struct {
	InstanceStatus []MySQLInstanceStatus
}

// MySQLInstanceStatus defines the observed state of a MySQL instance
type MySQLInstanceStatus struct {
	Available             bool
	PrimaryStatus         *MySQLPrimaryStatus
	ReplicaStatus         *MySQLReplicaStatus
	GlobalVariablesStatus *MySQLGlobalVariablesStatus
	CloneStateStatus      *MySQLCloneStateStatus
	Role                  string
}

// MySQLPrimaryStatus defines the observed state of a primary
type MySQLPrimaryStatus struct {
	ExecutedGtidSet string `db:"Executed_Gtid_Set"`

	// All of variables from here are NOT used in MOCO's reconcile
	File           string `db:"File"`
	Position       string `db:"Position"`
	BinlogDoDB     string `db:"Binlog_Do_DB"`
	BinlogIgnoreDB string `db:"Binlog_Ignore_DB"`
}

// MySQLReplicaStatus defines the observed state of a replica
type MySQLReplicaStatus struct {
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

// MySQLGlobalVariablesStatus defines the observed global variable state of a MySQL instance
type MySQLGlobalVariablesStatus struct {
	ReadOnly                           bool           `db:"@@read_only"`
	SuperReadOnly                      bool           `db:"@@super_read_only"`
	RplSemiSyncMasterWaitForSlaveCount int            `db:"@@rpl_semi_sync_master_wait_for_slave_count"`
	CloneValidDonorList                sql.NullString `db:"@@clone_valid_donor_list"`
}

// MySQLCloneStateStatus defines the observed clone state of a MySQL instance
type MySQLCloneStateStatus struct {
	State sql.NullString `db:"state"`
}

func GetMySQLClusterStatus(ctx context.Context, log logr.Logger, infra Infrastructure, cluster *mocov1alpha1.MySQLCluster) *MySQLClusterStatus {
	status := &MySQLClusterStatus{
		InstanceStatus: make([]MySQLInstanceStatus, int(cluster.Spec.Replicas)),
	}
	for instanceIdx := 0; instanceIdx < int(cluster.Spec.Replicas); instanceIdx++ {
		status.InstanceStatus[instanceIdx].Available = false

		podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), instanceIdx)

		db, err := infra.GetDB(ctx, cluster, instanceIdx)
		if err != nil {
			log.Info("instance not available", "err", err, "podName", podName)
			continue
		}

		primaryStatus, err := GetMySQLPrimaryStatus(ctx, db)
		if err != nil {
			log.Info("get primary status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].PrimaryStatus = primaryStatus

		replicaStatus, err := GetMySQLReplicaStatus(ctx, db)
		if err != nil {
			log.Info("get replica status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].ReplicaStatus = replicaStatus

		globalVariablesStatus, err := GetMySQLGlobalVariablesStatus(ctx, db)
		if err != nil {
			log.Info("get globalVariables status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].GlobalVariablesStatus = globalVariablesStatus

		cloneStatus, err := GetMySQLCloneStateStatus(ctx, db)
		if err != nil {
			log.Info("get clone status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].CloneStateStatus = cloneStatus

		pod := corev1.Pod{}
		err = infra.GetClient().Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: podName}, &pod)
		if err != nil {
			log.Info("get pod label failed", "err", err, "podName", podName)
			continue
		}
		if len(pod.Labels) != 0 {
			status.InstanceStatus[instanceIdx].Role = pod.Labels[moco.RoleKey]
		}

		status.InstanceStatus[instanceIdx].Available = true
	}
	return status
}

func GetMySQLPrimaryStatus(ctx context.Context, db *sqlx.DB) (*MySQLPrimaryStatus, error) {
	rows, err := db.QueryxContext(ctx, `SHOW MASTER STATUS`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLPrimaryStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, errors.New("primary status is empty")
}

func GetMySQLReplicaStatus(ctx context.Context, db *sqlx.DB) (*MySQLReplicaStatus, error) {
	rows, err := db.QueryxContext(ctx, `SHOW SLAVE STATUS`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLReplicaStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, nil
}

func GetMySQLGlobalVariablesStatus(ctx context.Context, db *sqlx.DB) (*MySQLGlobalVariablesStatus, error) {
	rows, err := db.QueryxContext(ctx, `SELECT @@read_only, @@super_read_only, @@rpl_semi_sync_master_wait_for_slave_count, @@clone_valid_donor_list`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLGlobalVariablesStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, errors.New("globalVariables status is empty")
}

func GetMySQLCloneStateStatus(ctx context.Context, db *sqlx.DB) (*MySQLCloneStateStatus, error) {
	rows, err := db.QueryxContext(ctx, `SELECT state FROM performance_schema.clone_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var status MySQLCloneStateStatus
	if rows.Next() {
		err = rows.StructScan(&status)
		if err != nil {
			return nil, err
		}
		return &status, nil
	}

	return nil, nil
}
