package bucket

import (
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type gcsBucket struct {
	name   string
	client *storage.Client
	key    *CustomerKey
}

// NewGCSBucket creates a Bucket that manages objects in Google Cloud Storage.
// If key is not nil, objects are stored and read with customer-supplied
// encryption keys (CSEK) using that key.
func NewGCSBucket(ctx context.Context, name string, key *CustomerKey, opts ...option.ClientOption) (Bucket, error) {
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	b := &gcsBucket{
		name:   name,
		client: client,
		key:    key,
	}

	return b, nil
}

func (b *gcsBucket) Put(ctx context.Context, key string, data io.Reader, objectSize int64) error {
	bucket := b.client.Bucket(b.name)

	obj := bucket.Object(key)
	if b.key != nil {
		obj = obj.Key(b.key.raw)
	}
	w := obj.NewWriter(ctx)

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
	obj := b.client.Bucket(b.name).Object(key)
	if b.key != nil {
		// The metadata can be read without the key.  It tells whether the
		// object is encrypted with a customer-supplied key, and with which one.
		// Objects uploaded before the key was configured are not encrypted.
		attrs, err := obj.Attrs(ctx)
		if err != nil {
			return nil, err
		}
		switch attrs.CustomerKeySHA256 {
		case "":
		case b.key.sha256Base64():
			obj = obj.Key(b.key.raw)
		default:
			return nil, fmt.Errorf("object %s is encrypted with a different customer-supplied key", key)
		}
	}

	rc, err := obj.NewReader(ctx)
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
