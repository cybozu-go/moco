package bucket

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type gcsBucket struct {
	name       string
	client     *storage.Client
	httpClient *http.Client
}

func NewGCSBucket(ctx context.Context, name string, httpClient *http.Client, opts ...option.ClientOption) (Bucket, error) {
	// If an httpClient exists, add it to the beginning of the options.
	// If the user specifies option.WithHTTPClient, that will take priority.
	if httpClient != nil {
		opts = append([]option.ClientOption{option.WithHTTPClient(httpClient)}, opts...)
	}

	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	b := &gcsBucket{
		name:       name,
		client:     client,
		httpClient: httpClient,
	}

	if b.httpClient == nil {
		b.httpClient = http.DefaultClient
	}

	return b, nil
}

func (b *gcsBucket) Put(ctx context.Context, key string, data io.Reader, _ int64) error {
	pv4, err := b.client.Bucket(b.name).GenerateSignedPostPolicyV4(key, &storage.PostPolicyV4Options{
		Fields: &storage.PolicyV4Fields{
			// It MUST only be a text file.
			ContentType: "text/plain",
		},
	})
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	for fieldName, value := range pv4.Fields {
		if err := mw.WriteField(fieldName, value); err != nil {
			return err
		}
	}

	go func() {
		defer pw.Close()
		defer mw.Close()

		fw, err := mw.CreateFormFile("file", key)
		if err != nil {
			return
		}
		if _, err := io.Copy(fw, data); err != nil {
			return
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pv4.URL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (b *gcsBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := b.client.Bucket(b.name).Object(key).NewReader(ctx)
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
