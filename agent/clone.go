package agent

import (
	"net/http"
)

func Clone(w http.ResponseWriter, r *http.Request) {
	// cmd := well.CommandContext(r.Context(), "mysql", "--defaults-extra-file="+filepath.Join(moco.MySQLDataPath, "misc.cnf"))
	// cmd.Stdin = strings.NewReader("FLUSH LOCAL ERROR LOGS;\nFLUSH LOCAL SLOW LOGS;\n")
	// err = cmd.Run()
	// if err != nil {
	// 	internalServerError(w, fmt.Errorf("failed to exec mysql FLUSH: %w", err))
	// 	return
	// }
}
