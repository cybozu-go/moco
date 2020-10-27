package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/jmoiron/sqlx"
	corev1 "k8s.io/api/core/v1"
)

func execAtLocal(cmd string, input []byte, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	command := exec.Command(cmd, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr

	if len(input) != 0 {
		command.Stdin = bytes.NewReader(input)
	}

	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func kubectl(args ...string) ([]byte, []byte, error) {
	return execAtLocal("./bin/kubectl", nil, args...)
}

//lint:ignore U1000 This func may be used in the future.
func kubectlWithInput(input []byte, args ...string) ([]byte, []byte, error) {
	return execAtLocal("./bin/kubectl", input, args...)
}

func getMySQLCluster() (*mocov1alpha1.MySQLCluster, error) {
	stdout, stderr, err := kubectl("get", "-n", "e2e-test", "mysqlcluster", "mysqlcluster", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get MySQLCluster. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
	}

	var mysqlCluster mocov1alpha1.MySQLCluster
	err = json.Unmarshal(stdout, &mysqlCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal MySQLCluster. stdout: %s, err: %v", stdout, err)
	}
	return &mysqlCluster, nil
}

func getRootPassword(mysqlCluster *mocov1alpha1.MySQLCluster) (*corev1.Secret, error) {
	stdout, stderr, err := kubectl("get", "-n", "e2e-test", "secret", "root-password-"+moco.UniqueName(mysqlCluster), "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get Secret. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
	}

	var secret corev1.Secret
	err = json.Unmarshal(stdout, &secret)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Secret stdout: %s, err: %v", stdout, err)
	}
	return &secret, nil
}

type mysqlConnector struct {
	cluster             *mocov1alpha1.MySQLCluster
	portForwardCommands []*exec.Cmd
	accessor            *accessor.MySQLAccessor
	basePort            int
}

func newMySQLConnector(cluster *mocov1alpha1.MySQLCluster) *mysqlConnector {
	acc := accessor.NewMySQLAccessor(&accessor.MySQLAccessorConfig{
		ConnMaxLifeTime:   30 * time.Minute,
		ConnectionTimeout: 3 * time.Second,
		ReadTimeout:       30 * time.Second,
	})
	return &mysqlConnector{
		cluster:  cluster,
		accessor: acc,
		basePort: 13306,
	}
}

func (c *mysqlConnector) startPortForward() error {
	for i := 0; i < int(c.cluster.Spec.Replicas); i++ {
		podName := fmt.Sprintf("%s-%d", moco.UniqueName(c.cluster), i)
		port := c.basePort + i
		command := exec.Command("./bin/kubectl", "-n", "e2e-test", "port-forward", "pod/"+podName, fmt.Sprintf("%d:%d", port, moco.MySQLPort))
		err := command.Start()
		if err != nil {
			c.stopPortForward()
			return err
		}
		c.portForwardCommands = append(c.portForwardCommands, command)
	}
	return nil
}

func (c *mysqlConnector) stopPortForward() {
	for _, command := range c.portForwardCommands {
		_ = command.Process.Kill()
	}
	c.portForwardCommands = []*exec.Cmd{}
}

func (c *mysqlConnector) connectToPrimary() (*sqlx.DB, error) {
	if c.cluster.Status.CurrentPrimaryIndex == nil {
		return nil, errors.New("CurrentPrimaryIndex is nil")
	}
	return c.connect(*c.cluster.Status.CurrentPrimaryIndex)
}

func (c *mysqlConnector) connect(index int) (*sqlx.DB, error) {
	port := c.basePort + index
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	secret, err := getRootPassword(c.cluster)
	if err != nil {
		return nil, err
	}
	password := string(secret.Data[moco.OperatorPasswordEnvName])
	return c.accessor.Get(addr, moco.OperatorAdminUser, password)
}

func findCondition(conditions []mocov1alpha1.MySQLClusterCondition, conditionType mocov1alpha1.MySQLClusterConditionType) *mocov1alpha1.MySQLClusterCondition {
	for i, c := range conditions {
		if c.Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func replica0(cluster *mocov1alpha1.MySQLCluster) (int, error) {
	if cluster.Status.CurrentPrimaryIndex == nil {
		return 0, errors.New("CurrentPrimaryIndex is nil")
	}
	for i := 0; i < int(cluster.Spec.Replicas); i++ {
		if i == *cluster.Status.CurrentPrimaryIndex {
			continue
		}
		return i, nil
	}
	return 0, errors.New("replica not found")
}

func insertData(db *sqlx.DB, count int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec("SET SESSION cte_max_recursion_depth = ?", count)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO moco_e2e.replication_test (val0, val1, val2, val3, val4) 
			WITH RECURSIVE t AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM t WHERE n < ?) 
			SELECT MD5(RAND()),MD5(RAND()),MD5(RAND()),MD5(RAND()),MD5(RAND()) FROM t
		`, count)
	if err != nil {
		return err
	}
	return tx.Commit()
}
