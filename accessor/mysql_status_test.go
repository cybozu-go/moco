package accessor

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/cybozu-go/moco"
	mocov1alpha1 "github.com/cybozu-go/moco/api/v1alpha1"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	host     = "localhost"
	password = "test-password"
	port     = 3306
)

func TestGetMySQLClusterStatus(t *testing.T) {
	err := initializeOperatorAdminUser()
	if err != nil {
		t.Fatalf("cannot create moco-admin user: err=%v", err)
	}

	acc := NewMySQLAccessor(&MySQLAccessorConfig{
		ConnMaxLifeTime:   30 * time.Minute,
		ConnectionTimeout: 3 * time.Second,
		ReadTimeout:       30 * time.Second,
	})

	inf := NewInfrastructure(nil, acc, password, []string{host}, 3306)
	cluster := mocov1alpha1.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			ClusterName: "test-cluster",
			Namespace:   "test-namespace",
			UID:         "test-uid",
		},
		Spec: mocov1alpha1.MySQLClusterSpec{
			Replicas: 1,
		},
	}
	logger := ctrl.Log.WithName("controllers").WithName("MySQLCluster")

	sts := GetMySQLClusterStatus(context.Background(), logger, inf, &cluster)
	if len(sts.InstanceStatus) != 1 {
		t.Fatal("cannot get the MySQLClusterState")
	}

	if sts.InstanceStatus[0].PrimaryStatus == nil {
		t.Error("cannot get or parse PrimaryStatus")
	}
	if sts.InstanceStatus[0].ReplicaStatus == nil {
		t.Error("cannot get or parse ReplicaStatus")
	}
	if sts.InstanceStatus[0].GlobalVariablesStatus == nil {
		t.Error("cannot get or parse GlobalVariablesStatus")
	}
	if sts.InstanceStatus[0].CloneStateStatus == nil {
		t.Error("cannot get or parse CloneStateStatus")
	}
}

func initializeOperatorAdminUser() error {
	conf := mysql.NewConfig()
	conf.User = "root"
	conf.Passwd = password
	conf.Addr = host + ":" + strconv.Itoa(port)
	conf.InterpolateParams = true

	var db *sqlx.DB
	var err error
	for i := 0; i < 10; i++ {
		db, err = sqlx.Connect("mysql", conf.FormatDSN())
		if err == nil {
			break
		}
		time.Sleep(time.Second * 3)
	}
	if err != nil {
		return err
	}

	_, err = db.Exec("CREATE USER IF NOT EXISTS ?@'%' IDENTIFIED BY ?", moco.OperatorAdminUser, password)
	if err != nil {
		return err
	}
	_, err = db.Exec("GRANT ALL ON *.* TO ?@'%' WITH GRANT OPTION", moco.OperatorAdminUser)
	if err != nil {
		return err
	}

	_, err = db.Exec("INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'rpl_semi_sync_master' already exists" {
			return err
		}
	}
	_, err = db.Exec("INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'rpl_semi_sync_slave' already exists" {
			return err
		}
	}
	_, err = db.Exec("INSTALL PLUGIN clone SONAME 'mysql_clone.so'")
	if err != nil {
		if err.Error() != "Error 1125: Function 'clone' already exists" {
			return err
		}
	}

	_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?`,
		"dummy", 3306, "dummy", "dummy")
	if err != nil {
		return err
	}
	_, err = db.Exec(`CLONE LOCAL DATA DIRECTORY = ?`, "/tmp/"+uuid.NewUUID())
	if err != nil {
		return err
	}

	return nil
}