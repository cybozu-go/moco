package bucket

import (
	"context"
	"io"
)

// Bucket represents the interface to access an object storage bucket.
type Bucket interface {
	// Put puts an object with `key`.  The data is read from `data`.
	Put(ctx context.Context, key string, data io.Reader, objectSize int64) error

	// Get gets an object by `key`.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// List lists the matching object keys that have `prefix`.
	// The prefix argument should end with /. (e.g. "foo/bar/").
	// If / is not at the end, both ojbects xx-1/bar and xx-11/bar are taken.
	List(ctx context.Context, prefix string) ([]string, error)
}
