package bucket

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func azureKeyHeaders(k *CustomerKey) map[string]string {
	return map[string]string{
		"X-Ms-Encryption-Algorithm":  "AES256",
		"X-Ms-Encryption-Key":        k.base64(),
		"X-Ms-Encryption-Key-Sha256": k.sha256Base64(),
	}
}

// fakeAzureHandler serves block uploads, downloads and blob properties for
// the blob "moco/key.tar".  The blob is encrypted with the customer-provided
// key whose SHA-256 is blobKeySHA256; an empty string means not encrypted.
// Requests whose key does not match the blob fail with 409 Conflict.
func fakeAzureHandler(t *testing.T, blobKeySHA256 string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("x-ms-request-id", "req")
		w.Header().Set("x-ms-version", r.Header.Get("x-ms-version"))

		if r.URL.Path != "/devstoreaccount1/moco/key.tar" {
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		comp := r.URL.Query().Get("comp")
		if r.Method == http.MethodPut {
			// Uploads set the key of the blob; they are always accepted.
			w.Header().Set("ETag", `"etag"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusCreated)
			return
		}
		if comp != "" {
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		requestKeySHA256 := r.Header.Get("x-ms-encryption-key-sha256")
		if requestKeySHA256 != blobKeySHA256 {
			code := "BlobUsesCustomerSpecifiedEncryption"
			if blobKeySHA256 == "" {
				code = "BlobDoesNotUseCustomerSpecifiedEncryption"
			}
			w.Header().Set("x-ms-error-code", code)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusConflict)
			if r.Method == http.MethodGet {
				_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><Error><Code>%s</Code><Message>key mismatch</Message></Error>`, code)
			}
			return
		}

		w.Header().Set("Content-Length", "7")
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = io.WriteString(w, "payload")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func TestAzureBucketCustomerKey(t *testing.T) {
	key := testCustomerKey(t, 0x42)
	otherKey := testCustomerKey(t, 0x43)

	type wantRequest struct {
		method string
		comp   string
		// withKey tells whether the CPK headers must be present.
		withKey bool
	}
	// Payloads smaller than one block are uploaded with a single Put Blob.
	upload := func(withKey bool) []wantRequest {
		return []wantRequest{{http.MethodPut, "", withKey}}
	}
	// Larger payloads are uploaded as staged blocks and a block list.
	blockUpload := func(withKey bool) []wantRequest {
		return []wantRequest{
			{http.MethodPut, "block", withKey},
			{http.MethodPut, "block", withKey},
			{http.MethodPut, "blocklist", withKey},
		}
	}
	download := func(withKey bool) wantRequest { return wantRequest{http.MethodGet, "", withKey} }
	properties := wantRequest{http.MethodHead, "", false}

	testCases := []struct {
		name          string
		key           *CustomerKey
		payloadSize   int
		blobKeySHA256 string
		wantGetErr    string
		wantRequests  []wantRequest
	}{
		{
			name:         "without key",
			wantRequests: append(upload(false), download(false)),
		},
		{
			name:          "with key on a blob encrypted with it",
			key:           key,
			blobKeySHA256: key.sha256Base64(),
			wantRequests:  append(upload(true), download(true)),
		},
		{
			name:          "with key and a multi-block upload",
			key:           key,
			payloadSize:   4<<20 + 1,
			blobKeySHA256: key.sha256Base64(),
			wantRequests:  append(blockUpload(true), download(true)),
		},
		{
			name:         "with key on an unencrypted blob",
			key:          key,
			wantRequests: append(upload(true), download(true), properties, download(false)),
		},
		{
			name:          "with key on a blob encrypted with another key",
			key:           key,
			blobKeySHA256: otherKey.sha256Base64(),
			wantGetErr:    "BlobUsesCustomerSpecifiedEncryption",
			wantRequests:  append(upload(true), download(true), properties),
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			f, endpoint := newFakeServer(t, fakeAzureHandler(t, tt.blobKeySHA256))
			connStr := "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;" +
				"AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;" +
				"BlobEndpoint=" + endpoint + "/devstoreaccount1;"
			b, err := NewAzureBucketFromConnectionString(t.Context(), connStr, "moco", tt.key)
			if err != nil {
				t.Fatalf("failed to create bucket: %v", err)
			}

			payload := "payload"
			if tt.payloadSize > 0 {
				payload = strings.Repeat("x", tt.payloadSize)
			}
			if err := b.Put(t.Context(), "key.tar", strings.NewReader(payload), int64(len(payload))); err != nil {
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
					got = append(got, fmt.Sprintf("%s %s?%s", r.method, r.path, r.query))
				}
				t.Fatalf("want %d requests, got %v", len(tt.wantRequests), got)
			}
			for i, req := range reqs {
				want := tt.wantRequests[i]
				query, err := url.ParseQuery(req.query)
				if err != nil {
					t.Fatalf("request %d: invalid query %q: %v", i, req.query, err)
				}
				comp := query.Get("comp")
				if req.method != want.method || comp != want.comp {
					t.Errorf("request %d: want %s comp=%s, got %s comp=%s", i, want.method, want.comp, req.method, comp)
				}
				if want.withKey {
					assertKeyHeaders(t, req, azureKeyHeaders(tt.key))
				} else {
					assertKeyHeaders(t, req, nil)
				}
			}
		})
	}
}
