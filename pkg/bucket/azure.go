package bucket

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

type azureBucket struct {
	name   string
	client *azblob.Client
	cpk    *blob.CPKInfo
}

// newCPKInfo returns the CPKInfo for key, or nil if key is nil.
func newCPKInfo(key *CustomerKey) *blob.CPKInfo {
	if key == nil {
		return nil
	}
	alg := blob.EncryptionAlgorithmType(customerKeyAlgorithm)
	k := key.base64()
	sum := key.sha256Base64()
	return &blob.CPKInfo{
		EncryptionAlgorithm: &alg,
		EncryptionKey:       &k,
		EncryptionKeySHA256: &sum,
	}
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
//
// If key is not nil, blobs are stored and read with customer-provided keys (CPK) using that key.
func NewAzureBucket(ctx context.Context, serviceURL, name string, credential azcore.TokenCredential, key *CustomerKey, optFns ...AzureClientOption) (Bucket, error) {
	clientOpts := &azblob.ClientOptions{}
	for _, fn := range optFns {
		fn(clientOpts)
	}

	client, err := azblob.NewClient(serviceURL, credential, clientOpts)
	if err != nil {
		return nil, err
	}

	return &azureBucket{
		name:   name,
		client: client,
		cpk:    newCPKInfo(key),
	}, nil
}

// NewAzureBucketFromConnectionString creates a Bucket using an Azure Storage connection string.
// This is useful for Azurite (local development emulator) and scenarios where connection
// strings are preferred over credential-based authentication.
//
// Connection string format:
// "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=<key>;BlobEndpoint=<url>;"
//
// Example for Azurite:
// "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;"
//
// If key is not nil, blobs are stored and read with customer-provided keys (CPK) using that key.
func NewAzureBucketFromConnectionString(ctx context.Context, connectionString, name string, key *CustomerKey) (Bucket, error) {
	client, err := azblob.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		return nil, err
	}

	return &azureBucket{
		name:   name,
		client: client,
		cpk:    newCPKInfo(key),
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
		// UploadStream passes CPKInfo to StageBlock and CommitBlockList.
		CPKInfo: b.cpk,
	})

	return err
}

func (b *azureBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if b.cpk == nil {
		return b.download(ctx, key, nil)
	}

	body, err := b.download(ctx, key, &azblob.DownloadStreamOptions{CPKInfo: b.cpk})
	if err == nil {
		return body, nil
	}

	// Blobs uploaded before the key was configured are not encrypted with it.
	// Reading the properties of a blob encrypted with a customer-provided key
	// requires the key, so if they can be read without it, the blob is not
	// encrypted with a customer-provided key and is downloaded without one.
	blobClient := b.client.ServiceClient().NewContainerClient(b.name).NewBlobClient(key)
	if _, propErr := blobClient.GetProperties(ctx, nil); propErr != nil {
		return nil, err
	}
	body, err2 := b.download(ctx, key, nil)
	if err2 != nil {
		return nil, fmt.Errorf("failed to download blob with customer-provided key (%w), and without it (%w)", err, err2)
	}
	return body, nil
}

func (b *azureBucket) download(ctx context.Context, key string, opts *azblob.DownloadStreamOptions) (io.ReadCloser, error) {
	resp, err := b.client.DownloadStream(ctx, b.name, key, opts)
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
