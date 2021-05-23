package backup

import (
	"path"
	"time"

	"github.com/cybozu-go/moco/pkg/constants"
)

const prefix = "moco"

func calcKey(clusterNS, clusterName, filename string, dt time.Time) string {
	return path.Join(prefix, clusterNS, clusterName, dt.Format(constants.BackupTimeFormat), filename)
}

func calcPrefix(clusterNS, clusterName string) string {
	return path.Join(prefix, clusterNS, clusterName)
}
