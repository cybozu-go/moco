package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/cybozu-go/moco/pkg/constants"
)

// dumpMetadata is stored as a small JSON object next to a dump file.
// It allows users to identify the MySQL version of a backup without
// downloading the dump tarball.
type dumpMetadata struct {
	MySQLVersion string `json:"mysqlVersion"`
}

// backupMetadata uploads the metadata of the dump file.
func (bm *BackupManager) backupMetadata(ctx context.Context) error {
	data, err := json.Marshal(dumpMetadata{MySQLVersion: bm.status.Version})
	if err != nil {
		return fmt.Errorf("failed to marshal dump metadata: %w", err)
	}
	data = append(data, '\n')

	key := calcKey(bm.cluster.Namespace, bm.cluster.Name, constants.DumpMetadataFilename, bm.startTime)
	if err := bm.bucket.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
		return fmt.Errorf("failed to put %s: %w", constants.DumpMetadataFilename, err)
	}

	bm.log.Info("uploaded dump metadata", "key", key, "mysqlVersion", bm.status.Version)
	return nil
}
