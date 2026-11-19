package bucket

import (
	"bytes"
	"crypto/md5" //nolint:gosec // MD5 is required by the S3 SSE-C protocol.
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// testCustomerKey returns a valid customer key and the raw key bytes.
func testCustomerKey(t *testing.T, b byte) *CustomerKey {
	t.Helper()
	k, err := ParseCustomerKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseCustomerKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x42}, 32)
	valid := base64.StdEncoding.EncodeToString(raw)
	md5Sum := md5.Sum(raw) //nolint:gosec
	sha256Sum := sha256.Sum256(raw)

	testCases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "valid key", input: valid},
		{name: "valid key with trailing newline", input: valid + "\n"},
		{name: "invalid base64", input: "not base64!", wantErr: "not valid base64"},
		{name: "too short", input: base64.StdEncoding.EncodeToString(raw[:16]), wantErr: "got 16 bytes"},
		{name: "too long", input: base64.StdEncoding.EncodeToString(append(raw, 0)), wantErr: "got 33 bytes"},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			k, err := ParseCustomerKey(tt.input)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := k.base64(); got != valid {
				t.Errorf("key: want %q, got %q", valid, got)
			}
			if want, got := base64.StdEncoding.EncodeToString(md5Sum[:]), k.md5Base64(); got != want {
				t.Errorf("key MD5: want %q, got %q", want, got)
			}
			if want, got := base64.StdEncoding.EncodeToString(sha256Sum[:]), k.sha256Base64(); got != want {
				t.Errorf("key SHA-256: want %q, got %q", want, got)
			}
		})
	}
}

// recordedRequest is a request received by a fake object storage server.
type recordedRequest struct {
	method string
	path   string
	query  string
	header http.Header
}

// fakeServer is an object storage server for tests.  It records every request
// and passes it to handler.
type fakeServer struct {
	mu       sync.Mutex
	requests []recordedRequest
	handler  http.HandlerFunc
}

func newFakeServer(t *testing.T, handler http.HandlerFunc) (*fakeServer, string) {
	t.Helper()
	f := &fakeServer{handler: handler}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		header: r.Header.Clone(),
	})
	f.mu.Unlock()
	f.handler(w, r)
}

func (f *fakeServer) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

// assertKeyHeaders checks that req carries the customer key headers with the
// given names when want is not nil, and none of them otherwise.
func assertKeyHeaders(t *testing.T, req recordedRequest, want map[string]string) {
	t.Helper()
	if want == nil {
		for name, v := range req.header {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "encryption") && strings.Contains(lower, "key") ||
				strings.Contains(lower, "encryption-algorithm") {
				t.Errorf("%s %s: unexpected header %s: %q", req.method, req.path, name, v)
			}
		}
		return
	}
	for name, v := range want {
		if got := req.header.Get(name); got != v {
			t.Errorf("%s %s: header %s: want %q, got %q", req.method, req.path, name, v, got)
		}
	}
}

// readAll reads and closes rc.
func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read object: %v", err)
	}
	return string(data)
}
