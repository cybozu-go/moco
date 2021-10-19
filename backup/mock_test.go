package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cybozu-go/moco/pkg/bkop"
	"github.com/cybozu-go/moco/pkg/bucket"
)

type mockOperator struct {
	binlogs    []string
	uuid       string
	gtid       string
	expectPiTR bool

	// status
	alive    bool
	closed   bool
	writable bool
	prepared bool
	pitr     bool
	finished bool
}

var _ bkop.Operator = &mockOperator{}

func (o *mockOperator) Ping() error {
	if !o.alive {
		o.alive = true
		return errors.New("not alive")
	}
	return nil
}

func (o *mockOperator) Close() {
	o.closed = true
}

func (o *mockOperator) GetServerStatus(_ context.Context, st *bkop.ServerStatus) error {
	st.CurrentBinlog = o.binlogs[len(o.binlogs)-1]
	st.UUID = o.uuid
	st.SuperReadOnly = !o.writable
	return nil
}

func (o *mockOperator) DumpFull(ctx context.Context, dir string) error {
	data, err := json.Marshal(map[string]string{
		"gtidExecuted": o.gtid,
	})
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "@.json"), data, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "dumpdata"), []byte("1234567890"), 0644)
}

func (o *mockOperator) GetBinlogs(_ context.Context) ([]string, error) {
	return o.binlogs, nil
}

func (o *mockOperator) DumpBinlog(ctx context.Context, dir string, binlogName string, filterGTID string) error {
	ok := false
	for _, binlog := range o.binlogs {
		if binlogName == binlog {
			ok = true
		}
		if ok {
			if err := os.WriteFile(filepath.Join(dir, binlog), []byte("binlog"), 0644); err != nil {
				return err
			}
		}
	}
	if !ok {
		return errors.New("binlog was purged")
	}
	return nil
}

func (o *mockOperator) PrepareRestore(_ context.Context) error {
	if !o.alive {
		return errors.New("not alive")
	}
	o.prepared = true
	o.writable = true
	return nil
}

func (o *mockOperator) LoadDump(ctx context.Context, dir string) error {
	if !o.prepared {
		return errors.New("not prepared")
	}
	_, err := os.Stat(filepath.Join(dir, "@.json"))
	return err
}

func (o *mockOperator) LoadBinlog(ctx context.Context, binlogDir, tmpDir string, restorePoint time.Time) error {
	if !o.prepared {
		return errors.New("not prepared")
	}
	entries, err := os.ReadDir(binlogDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("no binlog")
	}

	o.pitr = true
	return nil
}

func (o *mockOperator) FinishRestore(_ context.Context) error {
	if o.expectPiTR && !o.pitr {
		return errors.New("no pitr has performed")
	}
	o.writable = false
	o.finished = true
	return nil
}

type mockBucket struct {
	contents map[string][]byte
}

var _ bucket.Bucket = &mockBucket{}

func (b *mockBucket) Put(ctx context.Context, key string, r io.Reader, objectSize int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	b.contents[key] = data
	return nil
}

func (b *mockBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	data, ok := b.contents[key]
	if !ok {
		return nil, fmt.Errorf("%s is not found", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (b *mockBucket) List(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0, len(b.contents))
	for k := range b.contents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}
