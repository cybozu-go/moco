package backup

import (
	"io"
	"strings"
	"testing"
)

func TestByteCountWriter(t *testing.T) {
	r := strings.NewReader("abcdefg")

	bcw := &ByteCountWriter{}
	tr := io.TeeReader(r, bcw)

	n, err := io.Copy(io.Discard, tr)
	if err != nil {
		t.Fatal(err)
	}

	written := bcw.Written()
	if written != int64(n) {
		t.Errorf("unexpected written bytes: %d (n=%d)", written, n)
	}
}
