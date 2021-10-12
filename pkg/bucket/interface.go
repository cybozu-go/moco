package bucket

import (
	"context"
	"io"
)

// Bucket represents the interface to access an object storage bucket.
type Bucket interface {
	// Put puts an object with `key`.  The data is read from `data`.
	Put(ctx context.Context, key string, data io.Reader, partSize int64) error

	// Get gets an object by `key`.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// List lists the matching object keys that have `prefix`.
	List(ctx context.Context, prefix string) ([]string, error)
}
