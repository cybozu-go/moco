package clustering

import (
	"context"

	"github.com/go-logr/logr"
)

var defaultLog logr.Logger

// SetDefaultLogger sets the default logger used by the clustering package.
// The default logger is not normally used. It is used when another
// logger is not set in context due to testing or programming errors.
func SetDefaultLogger(log logr.Logger) {
	defaultLog = log
}

func logFromContext(ctx context.Context) logr.Logger {
	if log, err := logr.FromContext(ctx); err == nil {
		return log
	}
	return defaultLog
}
