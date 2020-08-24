package controllers

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileMySQLCluster recoclies MySQL cluster
func (r *MySQLClusterReconciler) reconcileClustering(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (bool, error) {
	_, err := r.getMySQLClusterStatus(ctx, log, cluster)
	return true, err
}

// MySQLClusterStatus contains MySQLCluster status
type MySQLClusterStatus struct {
	InstanceStatus []MySQLInstanceStatus
}

type MySQLPrimaryStatus struct {
	ExecutedGtidSet string
}

type MySQLReplicaStatus struct {
	PrimaryHost       string
	ReplicaIORunning  string
	ReplicaSQLRunning string
	RetrievedGtidSet  string
	ExecutedGtidSet   string
}

type MySQLInstanceStatus struct {
	Available     bool
	PrimaryStatus *MySQLPrimaryStatus
	ReplicaStatus []MySQLReplicaStatus
}

func (r *MySQLClusterReconciler) getMySQLClusterStatus(ctx context.Context, log logr.Logger, cluster *mocov1alpha1.MySQLCluster) (*MySQLClusterStatus, error) {
	secret := &corev1.Secret{}
	myNS, mySecretName := r.getSecretNameForController(cluster)
	err := r.Get(ctx, client.ObjectKey{Namespace: myNS, Name: mySecretName}, secret)
	if err != nil {
		return nil, err
	}
	operatorPassword := string(secret.Data[moco.OperatorPasswordKey])

	status := &MySQLClusterStatus{
		InstanceStatus: make([]MySQLInstanceStatus, int(cluster.Spec.Replicas)),
	}
	for instanceIdx := 0; instanceIdx < int(cluster.Spec.Replicas); instanceIdx++ {
		status.InstanceStatus[instanceIdx].Available = false

		podName := fmt.Sprintf("%s-%d", uniqueName(cluster), instanceIdx)
		host := fmt.Sprintf("%s.%s.%s", podName, uniqueName(cluster), cluster.Namespace)

		db, err := r.MySQLAccessor.Get(host, moco.OperatorAdminUser, operatorPassword)
		if err != nil {
			log.Info("instance not available", "err", err, "podName", podName)
			continue
		}

		primaryStatus, err := r.getMySQLPrimaryStatus(ctx, log, db)
		if err != nil {
			log.Info("get primary status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].PrimaryStatus = primaryStatus

		replicaStatus, err := r.getMySQLReplicaStatus(ctx, log, db)
		if err != nil {
			log.Info("get replica status failed", "err", err, "podName", podName)
			continue
		}
		status.InstanceStatus[instanceIdx].ReplicaStatus = replicaStatus

		status.InstanceStatus[instanceIdx].Available = true
	}
	return status, nil
}

func (r *MySQLClusterReconciler) getMySQLPrimaryStatus(ctx context.Context, log logr.Logger, db *sql.DB) (*MySQLPrimaryStatus, error) {
	rows, err := r.getColumns(ctx, log, db, "SHOW MASTER STATUS")
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, nil
	}

	if len(rows) != 1 {
		return nil, fmt.Errorf("unsupported topology")
	}

	status := &MySQLPrimaryStatus{
		ExecutedGtidSet: rows[0]["Executed_Gtid_Set"],
	}

	return status, nil
}

func (r *MySQLClusterReconciler) getMySQLReplicaStatus(ctx context.Context, log logr.Logger, db *sql.DB) ([]MySQLReplicaStatus, error) {
	rows, err := r.getColumns(ctx, log, db, "SHOW SLAVE STATUS")
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, nil
	}

	status := make([]MySQLReplicaStatus, len(rows))
	for idx, row := range rows {
		status[idx] = MySQLReplicaStatus{
			PrimaryHost:       row["Slave_IO_State"],
			ReplicaIORunning:  row["Slave_IO_Running"],
			ReplicaSQLRunning: row["Slave_SQL_Running"],
			RetrievedGtidSet:  row["Retrieved_Gtid_Set"],
			ExecutedGtidSet:   row["Executed_Gtid_Set"],
		}
	}
	return status, nil
}

func (r *MySQLClusterReconciler) getColumns(ctx context.Context, log logr.Logger, db *sql.DB, query string) ([]map[string]string, error) {
	rows, err := db.Query(query)
	if rows != nil {
		defer rows.Close()
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	// Make a slice for the values
	values := make([]sql.RawBytes, len(columns))

	// rows.Scan wants '[]interface{}' as an argument, so we must copy the
	// references into such a slice
	// See http://code.google.com/p/go-wiki/wiki/InterfaceSlice for details
	scanArgs := make([]interface{}, len(values))
	for i := range values {
		scanArgs[i] = &values[i]
	}

	result := make([]map[string]string, 0)

	// Fetch rows
	for rows.Next() {
		row := make(map[string]string)
		result = append(result, row)
		// get RawBytes from data
		err = rows.Scan(scanArgs...)
		if err != nil {
			return nil, err
		}

		// Now do something with the data.
		// Here we just print each column as a string.
		var value string
		for i, col := range values {
			// Here we can check if the value is nil (NULL value)
			if col == nil {
				value = "NULL"
			} else {
				value = string(col)
			}
			row[columns[i]] = value
		}
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
