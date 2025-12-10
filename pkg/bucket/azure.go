package bucket

import (
	"context"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

type azureBucket struct {
	name string
	client *azblob.Client
}

// AzureClientOption is a function type for configuring Azure Blob Storage client options.
type AzureClientOption func(*azblob.ClientOptions)

// WithAzureClientOptions allows passing custom Azure client options.
func WithAzureClientOptions(opts *azblob.ClientOptions) AzureClientOption {
	return func(o *azblob.ClientOptions) {
		*o = *opts
	}
}

// NewAzureBucket creates a Bucket that manages objects in Azure Blob Storage.
// It uses DefaultAzureCredential for authentication, which supports:
// - Environment variables (AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET)
// - Managed Identity
// - Azure CLI credentials
// - And other Azure authentication methods
//
// The serviceURL should be in the format: https://<account-name>.blob.core.windows.net/
func NewAzureBucket(ctx context.Context, serviceURL, name string, credential azcore.TokenCredential, optFns ...AzureClientOption) (Bucket, error) {
	clientOpts := &azblob.ClientOptions{}
	for _, fn := range optFns {
		fn(clientOpts)
	}

	client, err := azblob.NewClient(serviceURL, credential, clientOpts)
	if err != nil {
		return nil, err
	}

	return &azureBucket{
		name: name,
		client:        client,
	}, nil
}

func (b *azureBucket) Put(ctx context.Context, key string, data io.Reader, objectSize int64) error {
	// Determine content type based on file extension
	contentType := "application/octet-stream"
	switch {
	case strings.HasSuffix(key, ".tar"):
		contentType = "application/x-tar"
	case strings.HasSuffix(key, ".zst"):
		contentType = "application/zstd"
	}

	// Upload the blob
	_, err := b.client.UploadStream(ctx, b.name, key, data, &azblob.UploadStreamOptions{
		BlockSize: 4 * 1024 * 1024, // 4 MiB blocks
		Metadata:  map[string]*string{},
		HTTPHeaders: &blob.HTTPHeaders{
			BlobContentType: &contentType,
		},
	})

	return err
}

func (b *azureBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := b.client.DownloadStream(ctx, b.name, key, nil)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

func (b *azureBucket) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string

	pager := b.client.NewListBlobsFlatPager(b.name, &container.ListBlobsFlatOptions{
		Prefix: &prefix,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, blob := range page.Segment.BlobItems {
			if blob.Name != nil {
				keys = append(keys, *blob.Name)
			}
		}
	}

	return keys, nil
}