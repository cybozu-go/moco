package backup

import (
	"context"
	"testing"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/bkop"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/go-logr/logr"
)

func TestBackupMetadata(t *testing.T) {
	startTime := time.Date(2021, time.May, 15, 23, 0, 3, 0, time.UTC)
	cluster := &mocov1beta2.MySQLCluster{}
	cluster.Namespace = "foo"
	cluster.Name = "bar"

	bc := &mockBucket{contents: map[string][]byte{}}
	bm := &BackupManager{
		log:       logr.Discard(),
		cluster:   cluster,
		bucket:    bc,
		status:    bkop.ServerStatus{Version: "8.0.36"},
		startTime: startTime,
	}

	if err := bm.backupMetadata(context.Background()); err != nil {
		t.Fatal("unexpected error", err)
	}

	key := calcKey("foo", "bar", constants.DumpMetadataFilename, startTime)
	if key != "moco/foo/bar/20210515-230003/dump-metadata.json" {
		t.Errorf("unexpected key: %s", key)
	}
	data, ok := bc.contents[key]
	if !ok {
		t.Fatalf("metadata object %s was not uploaded", key)
	}
	expected := "{\"mysqlVersion\":\"8.0.36\"}\n"
	if string(data) != expected {
		t.Errorf("unexpected metadata: %q, expected %q", string(data), expected)
	}
}
