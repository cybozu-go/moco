package bucket

import (
	"context"
	"errors"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type gcsBucket struct {
	name   string
	client *storage.Client
}

func NewGCSBucket(ctx context.Context, name string, opts ...option.ClientOption) (Bucket, error) {
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	b := &gcsBucket{
		name:   name,
		client: client,
	}

	return b, nil
}

func (b *gcsBucket) Put(ctx context.Context, key string, data io.Reader, objectSize int64) error {
	bucket := b.client.Bucket(b.name)

	w := bucket.Object(key).NewWriter(ctx)

	// Chunk size is set to 16 MiB by default.
	// There is a trade-off between upload speed and memory space for the chunk size.
	// The default value is respected here.
	// https://cloud.google.com/storage/docs/resumable-uploads#go
	//	w.ChunkSize = int(decidePartSize(objectSize))

	if _, err := io.Copy(w, data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return nil
}

func (b *gcsBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := b.client.Bucket(b.name).Object(key).NewReader(ctx)
	if err != nil {
		return nil, err
	}
	return rc, nil
}

func (b *gcsBucket) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	it := b.client.Bucket(b.name).Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		obj, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		keys = append(keys, obj.Name)
	}
	return keys, nil
}
