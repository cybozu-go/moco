package backup

import (
	"io"
	"sync/atomic"
)

// ByteCountWriter counts the written data in bytes
type ByteCountWriter struct {
	written int64
}

var _ io.Writer = &ByteCountWriter{}

// Write implements io.Writer interface.
func (w *ByteCountWriter) Write(data []byte) (int, error) {
	atomic.AddInt64(&w.written, int64(len(data)))
	return len(data), nil
}

// Written returns the number of written bytes.
func (w *ByteCountWriter) Written() int64 {
	return atomic.LoadInt64(&w.written)
}
