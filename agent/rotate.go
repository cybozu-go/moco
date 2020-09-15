package agent

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
	"github.com/go-sql-driver/mysql"
)

func RotateLog(w http.ResponseWriter, r *http.Request) {
	errFile := filepath.Join(moco.VarLogPath, moco.MySQLErrorLogName)
	_, err := os.Stat(errFile)
	if err == nil {
		err := os.Rename(errFile, errFile+".0")
		if err != nil {
			log.Error("failed to rotate err log file", map[string]interface{}{
				log.FnError: err,
			})
			internalServerError(w, fmt.Errorf("failed to rotate err log file: %w", err))
			return
		}
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat err log file", map[string]interface{}{
			log.FnError: err,
		})
		internalServerError(w, fmt.Errorf("failed to stat err log file: %w", err))
		return
	}

	slowFile := filepath.Join(moco.VarLogPath, moco.MySQLSlowLogName)
	_, err = os.Stat(slowFile)
	if err == nil {
		err := os.Rename(slowFile, slowFile+".0")
		if err != nil {
			log.Error("failed to rotate slow query log file", map[string]interface{}{
				log.FnError: err,
			})
			internalServerError(w, fmt.Errorf("failed to rotate slow query log file: %w", err))
			return
		}
	} else if !os.IsNotExist(err) {
		log.Error("failed to stat slow query log file", map[string]interface{}{
			log.FnError: err,
		})
		internalServerError(w, fmt.Errorf("failed to stat slow query log file: %w", err))
		return
	}

	buf, err := ioutil.ReadFile(moco.MiscPasswordPath)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to read misc passsword file: %w", err))
		return
	}
	password := strings.TrimSpace(string(buf))

	conf := mysql.NewConfig()
	conf.User = user
	conf.Passwd = password
	conf.Addr = ""
	conf.Timeout = 3 * time.Second
	conf.ReadTimeout = 30 * time.Second

	cmd := well.CommandContext(r.Context(), "mysql", "--defaults-extra-file="+filepath.Join(moco.MySQLDataPath, "misc.cnf"))
	cmd.Stdin = strings.NewReader("FLUSH LOCAL ERROR LOGS;\nFLUSH LOCAL SLOW LOGS;\n")
	err = cmd.Run()
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to exec mysql FLUSH: %w", err))
		return
	}
}
