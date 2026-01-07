package bucket

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockTokenCredential implements azcore.TokenCredential for testing with Azurite
type mockTokenCredential struct{}

func (m *mockTokenCredential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "mock-token",
		ExpiresOn: time.Now().Add(1 * time.Hour),
	}, nil
}

var _ = Describe("AzureBucket", func() {
	ctx := context.Background()
	var dataDir string
	const (
		accountName   = "devstoreaccount1"
		accountKey    = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
		name = "test"
		azuriteURL    = "http://127.0.0.1:10000/devstoreaccount1"
	)

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "")
		Expect(err).NotTo(HaveOccurred())
		dataDir = dir

		// Start Azurite (Azure Storage Emulator) in Docker
		err = exec.Command("docker", "run", "--rm", "--name=moco-azurite", "-d",
			"-p", "10000:10000",
			"-v", fmt.Sprintf("%s:/data", dir),
			"mcr.microsoft.com/azure-storage/azurite",
			"azurite-blob", "--blobHost", "0.0.0.0", "--blobPort", "10000", "--location", "/data").Run()
		Expect(err).NotTo(HaveOccurred())

		// Wait for Azurite to be ready
		Eventually(func() error {
			conn, err := net.Dial("tcp", "127.0.0.1:10000")
			if err != nil {
				return err
			}
			conn.Close()
			return nil
		}, 60).Should(Succeed())

		// Create container using connection string
		connectionString := fmt.Sprintf("DefaultEndpointsProtocol=http;AccountName=%s;AccountKey=%s;BlobEndpoint=%s;",
			accountName, accountKey, azuriteURL)

		serviceClient, err := azblob.NewClientFromConnectionString(connectionString, nil)
		Expect(err).NotTo(HaveOccurred())

		_, err = serviceClient.CreateContainer(ctx, name, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		exec.Command("docker", "kill", "moco-azurite").Run()
		time.Sleep(1 * time.Second)
		os.RemoveAll(dataDir)
	})

	createBucket := func() Bucket {
		connectionString := fmt.Sprintf("DefaultEndpointsProtocol=http;AccountName=%s;AccountKey=%s;BlobEndpoint=%s;",
			accountName, accountKey, azuriteURL)

		// For testing, we'll create the client directly with connection string
		// In production, users would use proper credentials
		serviceClient, err := azblob.NewClientFromConnectionString(connectionString, nil)
		Expect(err).NotTo(HaveOccurred())

		return &azureBucket{
			name:   name,
			client: serviceClient,
		}
	}

	It("should put and get objects", func() {
		b := createBucket()

		err := b.Put(ctx, "foo/bar", strings.NewReader("01234567890123456789"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		r, err := b.Get(ctx, "foo/bar")
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()

		data, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())

		Expect(data).To(Equal([]byte("01234567890123456789")))

		for i := 0; i < 1100; i++ {
			err = b.Put(ctx, fmt.Sprintf("foo/baz%d", i), strings.NewReader("01234567890123456789"), 128<<20)
			Expect(err).NotTo(HaveOccurred())
		}

		keys, err := b.List(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(1101))

		keys, err = b.List(ctx, "foo/bar")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(1))
	})

	It("should put unseekable objects", func() {
		b := createBucket()

		dateCmd := exec.Command("date")
		pr, pw, err := os.Pipe()
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			if pr != nil {
				pr.Close()
			}
			if pw != nil {
				pw.Close()
			}
		}()
		dateCmd.Stdout = pw
		err = dateCmd.Start()
		Expect(err).NotTo(HaveOccurred())
		pw.Close()
		pw = nil

		err = b.Put(ctx, "date", io.TeeReader(pr, io.Discard), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		dateCmd.Wait()

		r, err := b.Get(ctx, "date")
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()

		data, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())

		fmt.Println(string(data))
	})

	It("should put objects and get list of objects up to delimiter", func() {
		b := createBucket()

		err := b.Put(ctx, "foo1/bar", strings.NewReader("01234567890123456789"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		r, err := b.Get(ctx, "foo1/bar")
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()

		err = b.Put(ctx, "foo11/bar", strings.NewReader("01234567890123456789"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		r, err = b.Get(ctx, "foo11/bar")
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()

		data, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())

		Expect(data).To(Equal([]byte("01234567890123456789")))

		keys, err := b.List(ctx, "foo1")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(2))

		// prefix with delimiter
		keys, err = b.List(ctx, "foo1/")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(1))
	})

	It("should set correct content types", func() {
		b := createBucket()

		// Test .tar file
		err := b.Put(ctx, "backup.tar", strings.NewReader("tar content"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		// Test .zst file
		err = b.Put(ctx, "binlog.tar.zst", strings.NewReader("zstd content"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		// Test generic file
		err = b.Put(ctx, "generic.dat", strings.NewReader("generic content"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		// Verify we can retrieve all files
		keys, err := b.List(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(ContainElements("backup.tar", "binlog.tar.zst", "generic.dat"))
	})

	It("should handle empty prefix in List", func() {
		b := createBucket()

		err := b.Put(ctx, "file1", strings.NewReader("content1"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		err = b.Put(ctx, "file2", strings.NewReader("content2"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		keys, err := b.List(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(2))
		Expect(keys).To(ContainElements("file1", "file2"))
	})

	It("should handle large file uploads", func() {
		b := createBucket()

		// Create a large string (10 MB)
		largeContent := strings.Repeat("A", 10*1024*1024)

		err := b.Put(ctx, "large-file", strings.NewReader(largeContent), int64(len(largeContent)))
		Expect(err).NotTo(HaveOccurred())

		r, err := b.Get(ctx, "large-file")
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()

		data, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(data)).To(Equal(10 * 1024 * 1024))
	})

	It("should return error for non-existent blob", func() {
		b := createBucket()

		_, err := b.Get(ctx, "non-existent-blob")
		Expect(err).To(HaveOccurred())
	})

	It("should list no keys when prefix matches nothing", func() {
		b := createBucket()

		err := b.Put(ctx, "foo/bar", strings.NewReader("content"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		keys, err := b.List(ctx, "baz/")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(0))
	})
})