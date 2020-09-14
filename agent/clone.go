package agent

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/well"
)

func Clone(w http.ResponseWriter, r *http.Request) {
	buf, err := ioutil.ReadFile(moco.DonorPasswordPath)
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to read password file: %w", err))
		return
	}
	password := strings.TrimSpace(string(buf))

	cmd := well.CommandContext(r.Context(), "mysql", "--defaults-extra-file="+filepath.Join(moco.MySQLDataPath, "misc.cnf"))
	query := fmt.Sprintf("CLONE INSTANCE FROM '%s'@'10.244.1.9':%d IDENTIFIED BY '%s';\n", moco.DonorUser, moco.MySQLPort, password)
	cmd.Stdin = strings.NewReader(query)
	err = cmd.Run()
	if err != nil {
		internalServerError(w, fmt.Errorf("failed to exec mysql FLUSH: %w", err))
		return
	}
}
