package bucket

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// DefaultPartSize is the default part size used for Bucket.Put method.
const DefaultPartSize = 128 << 20

// WithCredentials specifies a credential provider.
func WithCredentials(cred aws.CredentialsProvider) func(*s3.Options) {
	return func(o *s3.Options) {
		o.Credentials = cred
	}
}

// WithRegion specifies the region of the bucket.
func WithRegion(region string) func(*s3.Options) {
	return func(o *s3.Options) {
		o.Region = region
	}
}

// WithEndpointURL specifies the endpoint of S3 API.
func WithEndpointURL(u string) func(*s3.Options) {
	return func(o *s3.Options) {
		o.EndpointResolver = s3.EndpointResolverFromURL(u)
	}
}

// WithPathStyle specifies to use the path-style API request.
func WithPathStyle() func(*s3.Options) {
	return func(o *s3.Options) {
		o.UsePathStyle = true
	}
}

// WithHTTPClient specifies the http.Client to be used.
func WithHTTPClient(c *http.Client) func(*s3.Options) {
	return func(o *s3.Options) {
		o.HTTPClient = c
	}
}

type s3Bucket struct {
	name     string
	partSize int64
	client   *s3.Client
}

// NewS3Bucket creates a Bucket that manage object in S3.
// PartSize is used to put objects with the upload manager.
// If the size is less than the minimum (5 MiB), DefaultPartSize will be used.
func NewS3Bucket(name string, partSize int64, optFns ...func(*s3.Options)) (Bucket, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, err
	}

	if partSize < (5 << 20) {
		partSize = DefaultPartSize
	}

	return s3Bucket{
		name:     name,
		partSize: partSize,
		client:   s3.NewFromConfig(cfg, optFns...),
	}, nil
}

func (b s3Bucket) Put(ctx context.Context, key string, data io.Reader) error {
	mt := "application/octet-stream"
	switch {
	case strings.HasSuffix(key, ".tar"):
		mt = "application/x-tar"
	case strings.HasSuffix(key, ".zst"):
		mt = "application/zstd"
	}

	uploader := manager.NewUploader(b.client, func(u *manager.Uploader) {
		u.Concurrency = 1
		u.PartSize = b.partSize
		u.LeavePartsOnError = false
	})
	pi := &s3.PutObjectInput{
		Bucket:      &b.name,
		Key:         &key,
		Body:        data,
		ContentType: &mt,
	}
	_, err := uploader.Upload(ctx, pi)
	return err
}

func (b s3Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	gi := &s3.GetObjectInput{
		Bucket: &b.name,
		Key:    &key,
	}
	resp, err := b.client.GetObject(ctx, gi)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (b s3Bucket) List(ctx context.Context, prefix string) ([]string, error) {
	li := &s3.ListObjectsV2Input{
		Bucket: &b.name,
	}
	if len(prefix) > 0 {
		li.Prefix = &prefix
	}

	p := s3.NewListObjectsV2Paginator(b.client, li)

	var keys []string
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, obj := range page.Contents {
			keys = append(keys, *obj.Key)
		}
	}

	return keys, nil
}
