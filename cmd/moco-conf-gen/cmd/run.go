package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cybozu-go/moco"
)

var mycnf = `
[mysqld]
datadir         = /var/lib/mysql
pid-file        = /var/run/mysqld/mysqld.pid
socket          = /var/run/mysqld/mysqld.sock
secure-file-priv= NULL

# Disabling symbolic-links to prevent assorted security risks
symbolic-links=0
`

func subMain() error {
	// log.Info("create config file for admin interface", nil)
	// err = confAdminInterface(ctx, viper.GetString(moco.PodIPFlag))
	// if err != nil {
	// 	return err
	// }

	// log.Info("create config file for server-id", nil)
	// err = confServerID(ctx, viper.GetString(moco.PodNameFlag))
	// if err != nil {
	// 	return err
	// }

	return ioutil.WriteFile(filepath.Join(moco.MySQLConfPath, moco.MySQLConfName), []byte(mycnf), 0644)
}

func confAdminInterface(ctx context.Context, podIP string) error {
	conf := `
[mysqld]
admin-address=%s
`
	return ioutil.WriteFile(filepath.Join(moco.MySQLConfPath, "admin-interface.cnf"), []byte(fmt.Sprintf(conf, podIP)), 0400)
}

func confServerID(ctx context.Context, podNameWithOrdinal string) error {
	// ordinal should be increased by 1000 because the case server-id is 0 is not suitable for the replication purpose
	const ordinalOffset = 1000

	s := strings.Split(podNameWithOrdinal, "-")
	if len(s) < 2 {
		return errors.New("podName should contain an ordinal with dash, like 'podname-0', at the end: " + podNameWithOrdinal)
	}

	ordinal, err := strconv.Atoi(s[len(s)-1])
	if err != nil {
		return err
	}

	conf := `
[mysqld]
server-id=%d
`
	return ioutil.WriteFile(filepath.Join(moco.MySQLConfPath, "server-id.cnf"), []byte(fmt.Sprintf(conf, ordinal+ordinalOffset)), 0400)
}
