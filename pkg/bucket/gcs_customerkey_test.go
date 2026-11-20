package bucket

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/option"
)

func gcsKeyHeaders(k *CustomerKey) map[string]string {
	return map[string]string{
		"X-Goog-Encryption-Algorithm":  "AES256",
		"X-Goog-Encryption-Key":        k.base64(),
		"X-Goog-Encryption-Key-Sha256": k.sha256Base64(),
	}
}

// fakeGCSHandler serves uploads, object metadata (JSON API) and object reads
// (XML API) for the bucket "test".  The metadata reports objectKeySHA256 as the
// customer-supplied key of every object; an empty string means not encrypted.
func fakeGCSHandler(t *testing.T, objectKeySHA256 string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		object := map[string]any{"bucket": "test", "name": "key.tar", "size": "7"}
		if objectKeySHA256 != "" {
			object["customerEncryption"] = map[string]string{
				"encryptionAlgorithm": "AES256",
				"keySha256":           objectKeySHA256,
			}
		}

		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/upload/storage/v1/b/test/o"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(object)
		case r.Method == http.MethodGet && r.URL.Path == "/storage/v1/b/test/o/key.tar":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(object)
		case r.Method == http.MethodGet && r.URL.Path == "/test/key.tar":
			w.Header().Set("Content-Length", "7")
			_, _ = io.WriteString(w, "payload")
		default:
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestGCSBucketCustomerKey(t *testing.T) {
	key := testCustomerKey(t, 0x42)
	otherKey := testCustomerKey(t, 0x43)

	type wantRequest struct {
		method string
		path   string
		// withKey tells whether the CSEK headers must be present.
		withKey bool
	}
	upload := func(withKey bool) wantRequest {
		return wantRequest{http.MethodPost, "/upload/storage/v1/b/test/o", withKey}
	}
	attrs := wantRequest{http.MethodGet, "/storage/v1/b/test/o/key.tar", false}
	read := func(withKey bool) wantRequest {
		return wantRequest{http.MethodGet, "/test/key.tar", withKey}
	}

	testCases := []struct {
		name            string
		key             *CustomerKey
		objectKeySHA256 string
		wantGetErr      string
		wantRequests    []wantRequest
	}{
		{
			name:         "without key",
			wantRequests: []wantRequest{upload(false), read(false)},
		},
		{
			name:            "with key on an object encrypted with it",
			key:             key,
			objectKeySHA256: key.sha256Base64(),
			wantRequests:    []wantRequest{upload(true), attrs, read(true)},
		},
		{
			name:         "with key on an unencrypted object",
			key:          key,
			wantRequests: []wantRequest{upload(true), attrs, read(false)},
		},
		{
			name:            "with key on an object encrypted with another key",
			key:             key,
			objectKeySHA256: otherKey.sha256Base64(),
			wantGetErr:      "encrypted with a different customer-supplied key",
			wantRequests:    []wantRequest{upload(true), attrs},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			f, url := newFakeServer(t, fakeGCSHandler(t, tt.objectKeySHA256))
			b, err := NewGCSBucket(t.Context(), "test", tt.key,
				option.WithEndpoint(url+"/storage/v1/"),
				option.WithoutAuthentication(),
			)
			if err != nil {
				t.Fatalf("failed to create bucket: %v", err)
			}

			if err := b.Put(t.Context(), "key.tar", strings.NewReader("payload"), 7); err != nil {
				t.Fatalf("Put failed: %v", err)
			}
			rc, err := b.Get(t.Context(), "key.tar")
			if tt.wantGetErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantGetErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantGetErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("Get failed: %v", err)
				}
				if got := readAll(t, rc); got != "payload" {
					t.Errorf("want payload, got %q", got)
				}
			}

			reqs := f.recorded()
			if len(reqs) != len(tt.wantRequests) {
				var got []string
				for _, r := range reqs {
					got = append(got, fmt.Sprintf("%s %s", r.method, r.path))
				}
				t.Fatalf("want %d requests, got %v", len(tt.wantRequests), got)
			}
			for i, req := range reqs {
				want := tt.wantRequests[i]
				if req.method != want.method || req.path != want.path {
					t.Errorf("request %d: want %s %s, got %s %s", i, want.method, want.path, req.method, req.path)
				}
				if want.withKey {
					assertKeyHeaders(t, req, gcsKeyHeaders(tt.key))
				} else {
					assertKeyHeaders(t, req, nil)
				}
			}
		})
	}
}
