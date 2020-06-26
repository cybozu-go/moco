package controllers

import (
	"fmt"
	"testing"
)

func TestMySQLConfGenerator(t *testing.T) {

	gen := mysqlConfGenerator{}
	gen.mergeSection("mysqld", map[string]string{
		"datadir":  "/invalid/path",
		"pid-file": "/invalid/pid",
		"debug":    "+P",
	})
	gen.mergeSection("mysqld", map[string]string{
		"binlog-format": "ROW",
	})
	gen.merge(map[string]map[string]string{
		"client": {
			"port": "3306",
		},
		"mysqld": {
			"datadir":  "/var/lib/mysql",
			"pid_file": "/var/run/mysqld/mysqld.pid",
		},
	})

	actual, err := gen.generate()
	if err != nil {
		t.Fatal(err)
	}
	expect := `[client]
port = 3306
[mysqld]
binlog_format = ROW
datadir = /var/lib/mysql
debug = +P
pid_file = /var/run/mysqld/mysqld.pid
`

	if actual != expect {
		t.Error(fmt.Sprintf("actual: %s, expect: %s", actual, expect))
	}
}
