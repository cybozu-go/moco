package bucket

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func s3KeyHeaders(k *CustomerKey) map[string]string {
	return map[string]string{
		"X-Amz-Server-Side-Encryption-Customer-Algorithm": "AES256",
		"X-Amz-Server-Side-Encryption-Customer-Key":       k.base64(),
		"X-Amz-Server-Side-Encryption-Customer-Key-Md5":   k.md5Base64(),
	}
}

// fakeS3Handler serves PUT and GET.  If rejectKeyOnGet is true, a GET with
// SSE-C headers fails with 400, as S3 does for objects not encrypted with SSE-C.
func fakeS3Handler(rejectKeyOnGet bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.Method {
		case http.MethodPut:
			w.Header().Set("ETag", `"etag"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if rejectKeyOnGet && r.Header.Get("X-Amz-Server-Side-Encryption-Customer-Algorithm") != "" {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>InvalidRequest</Code><Message>The encryption parameters are not applicable to this object.</Message></Error>`)
				return
			}
			_, _ = io.WriteString(w, "payload")
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func TestS3BucketCustomerKey(t *testing.T) {
	key := testCustomerKey(t, 0x42)

	testCases := []struct {
		name           string
		key            *CustomerKey
		rejectKeyOnGet bool
		wantMethods    []string
		// wantKey tells, per request, whether the SSE-C headers must be present.
		wantKey []bool
	}{
		{
			name:        "without key",
			wantMethods: []string{http.MethodPut, http.MethodGet},
			wantKey:     []bool{false, false},
		},
		{
			name:        "with key",
			key:         key,
			wantMethods: []string{http.MethodPut, http.MethodGet},
			wantKey:     []bool{true, true},
		},
		{
			name:           "with key on an unencrypted object",
			key:            key,
			rejectKeyOnGet: true,
			wantMethods:    []string{http.MethodPut, http.MethodGet, http.MethodGet},
			wantKey:        []bool{true, true, false},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			f, url := newFakeServer(t, fakeS3Handler(tt.rejectKeyOnGet))
			b, err := NewS3Bucket("test", tt.key,
				WithEndpointURL(url),
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

			if err := b.Put(t.Context(), "key.tar", strings.NewReader("payload"), 7); err != nil {
				t.Fatalf("Put failed: %v", err)
			}
			rc, err := b.Get(t.Context(), "key.tar")
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
			if got := readAll(t, rc); got != "payload" {
				t.Errorf("want payload, got %q", got)
			}

			reqs := f.recorded()
			if len(reqs) != len(tt.wantMethods) {
				t.Fatalf("want %d requests, got %d", len(tt.wantMethods), len(reqs))
			}
			for i, req := range reqs {
				if req.method != tt.wantMethods[i] {
					t.Errorf("request %d: want %s, got %s", i, tt.wantMethods[i], req.method)
				}
				if tt.wantKey[i] {
					assertKeyHeaders(t, req, s3KeyHeaders(tt.key))
				} else {
					assertKeyHeaders(t, req, nil)
				}
			}
		})
	}
}
