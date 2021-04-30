package dbop

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/go-sql-driver/mysql"
)

type logger struct {
	log logr.Logger
}

var _ mysql.Logger = logger{}

func (l logger) Print(v ...interface{}) {
	l.log.Info(fmt.Sprint(v...))
}

// SetLogger configures MySQL driver logging to use `log`.
func SetLogger(log logr.Logger) {
	mysql.SetLogger(logger{log: log})
}

func init() {
	SetLogger(logr.Discard())
}
