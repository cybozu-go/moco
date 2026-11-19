package bucket

import (
	"bytes"
	"crypto/md5" //nolint:gosec // MD5 is required by the S3 SSE-C protocol.
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

const (
	sseAlgorithmHeader = "X-Amz-Server-Side-Encryption-Customer-Algorithm"
	sseKeyHeader       = "X-Amz-Server-Side-Encryption-Customer-Key"
	sseKeyMD5Header    = "X-Amz-Server-Side-Encryption-Customer-Key-Md5"
)

func TestParseSSECustomerKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x42}, 32)
	valid := base64.StdEncoding.EncodeToString(raw)
	sum := md5.Sum(raw) //nolint:gosec

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
			k, err := ParseSSECustomerKey(tt.input)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if k.keyB64 != valid {
				t.Errorf("key: want %q, got %q", valid, k.keyB64)
			}
			if want := base64.StdEncoding.EncodeToString(sum[:]); k.md5B64 != want {
				t.Errorf("key MD5: want %q, got %q", want, k.md5B64)
			}
		})
	}
}

// fakeS3 is a minimal S3 server that records the SSE-C headers of each request.
type fakeS3 struct {
	mu sync.Mutex
	// requests and methods hold the headers and the method of each request.
	requests []http.Header
	methods  []string
	// rejectSSEOnGet makes GET requests with SSE-C headers fail with 400,
	// as S3 does for objects that are not encrypted with SSE-C.
	rejectSSEOnGet bool
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)

	f.mu.Lock()
	f.requests = append(f.requests, r.Header.Clone())
	f.methods = append(f.methods, r.Method)
	f.mu.Unlock()

	switch r.Method {
	case http.MethodPut:
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if f.rejectSSEOnGet && r.Header.Get(sseAlgorithmHeader) != "" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>InvalidRequest</Code><Message>The encryption parameters are not applicable to this object.</Message></Error>`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "payload")
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newFakeS3Bucket(t *testing.T, f *fakeS3, sse *SSECustomerKey) Bucket {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	b, err := NewS3Bucket("test", sse,
		WithEndpointURL(srv.URL),
		WithPathStyle(),
		WithRegion("us-east-1"),
		WithCredentials(credentials.StaticCredentialsProvider{Value: aws.Credentials{
			AccessKeyID:     "test",
			SecretAccessKey: "test",
		}}),
	)
	if err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}
	return b
}

func TestS3BucketSSECustomerKey(t *testing.T) {
	sse, err := ParseSSECustomerKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)))
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name           string
		sse            *SSECustomerKey
		rejectSSEOnGet bool
		// wantSSE lists, per request, whether SSE-C headers must be present.
		wantMethods []string
		wantSSE     []bool
	}{
		{
			name:        "without key",
			wantMethods: []string{http.MethodPut, http.MethodGet},
			wantSSE:     []bool{false, false},
		},
		{
			name:        "with key",
			sse:         sse,
			wantMethods: []string{http.MethodPut, http.MethodGet},
			wantSSE:     []bool{true, true},
		},
		{
			name:           "with key on an unencrypted object",
			sse:            sse,
			rejectSSEOnGet: true,
			wantMethods:    []string{http.MethodPut, http.MethodGet, http.MethodGet},
			wantSSE:        []bool{true, true, false},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeS3{rejectSSEOnGet: tt.rejectSSEOnGet}
			b := newFakeS3Bucket(t, f, tt.sse)

			if err := b.Put(t.Context(), "key.tar", strings.NewReader("payload"), 7); err != nil {
				t.Fatalf("Put failed: %v", err)
			}
			rc, err := b.Get(t.Context(), "key.tar")
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
			data, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("failed to read object: %v", err)
			}
			if string(data) != "payload" {
				t.Errorf("want payload, got %q", data)
			}

			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.methods) != len(tt.wantMethods) {
				t.Fatalf("want requests %v, got %v", tt.wantMethods, f.methods)
			}
			for i, h := range f.requests {
				if f.methods[i] != tt.wantMethods[i] {
					t.Errorf("request %d: want %s, got %s", i, tt.wantMethods[i], f.methods[i])
				}
				if !tt.wantSSE[i] {
					for _, name := range []string{sseAlgorithmHeader, sseKeyHeader, sseKeyMD5Header} {
						if v := h.Get(name); v != "" {
							t.Errorf("request %d: unexpected header %s: %q", i, name, v)
						}
					}
					continue
				}
				if v := h.Get(sseAlgorithmHeader); v != "AES256" {
					t.Errorf("request %d: algorithm: want AES256, got %q", i, v)
				}
				if v := h.Get(sseKeyHeader); v != tt.sse.keyB64 {
					t.Errorf("request %d: key: want %q, got %q", i, tt.sse.keyB64, v)
				}
				if v := h.Get(sseKeyMD5Header); v != tt.sse.md5B64 {
					t.Errorf("request %d: key MD5: want %q, got %q", i, tt.sse.md5B64, v)
				}
			}
		})
	}
}
