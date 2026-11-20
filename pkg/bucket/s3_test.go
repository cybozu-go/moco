package bucket

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("S3Bucket", func() {
	ctx := context.Background()
	var dataDir string

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "")
		Expect(err).NotTo(HaveOccurred())
		dataDir = dir
		err = exec.Command("docker", "run", "--rm", "--name=moco-minio", "-d", "-p", "9000:9000",
			"-v", fmt.Sprintf("%s:/data", dir),
			"pgsty/silo", "server", "/data").Run()
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			conn, err := net.Dial("tcp", "localhost:9000")
			if err != nil {
				return err
			}
			conn.Close()
			return nil
		}, 60).Should(Succeed())

		cfg, err := config.LoadDefaultConfig(ctx, config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
				Source:          "minio default credentials",
			}}))
		Expect(err).NotTo(HaveOccurred())
		client := s3.NewFromConfig(cfg,
			WithEndpointURL("http://localhost:9000"),
			WithRegion("us-east-1"),
			WithPathStyle(),
		)

		cbi := &s3.CreateBucketInput{
			Bucket: aws.String("test"),
		}
		_, err = client.CreateBucket(ctx, cbi)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		exec.Command("docker", "kill", "moco-minio").Run()
		time.Sleep(1 * time.Second)
		os.RemoveAll(dataDir)
	})

	It("should put and get objects", func() {
		os.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")
		os.Setenv("AWS_REGION", "us-east-1")

		b, err := NewS3Bucket("test", WithEndpointURL("http://localhost:9000"), WithPathStyle())
		Expect(err).NotTo(HaveOccurred())

		err = b.Put(ctx, "foo/bar", strings.NewReader("01234567890123456789"), 128<<20)
		Expect(err).NotTo(HaveOccurred())

		r, err := b.Get(ctx, "foo/bar")
		Expect(err).NotTo(HaveOccurred())
		defer r.Close()

		data, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())

		Expect(data).To(Equal([]byte("01234567890123456789")))

		for i := range 1100 {
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
		os.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")
		os.Setenv("AWS_REGION", "us-east-1")

		b, err := NewS3Bucket("test", WithEndpointURL("http://localhost:9000"), WithPathStyle())
		Expect(err).NotTo(HaveOccurred())

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
		os.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")
		os.Setenv("AWS_REGION", "us-east-1")

		b, err := NewS3Bucket("test", WithEndpointURL("http://localhost:9000"), WithPathStyle())
		Expect(err).NotTo(HaveOccurred())

		err = b.Put(ctx, "foo1/bar", strings.NewReader("01234567890123456789"), 128<<20)
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

	It("should calculate the partSize correctly", func() {
		partSize := decidePartSize(0)
		Expect(partSize).Should(BeNumerically("==", 0))

		partSize = decidePartSize(1)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit))

		partSize = decidePartSize(50)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit))

		partSize = decidePartSize(PartSizeUnit*UploadParts - 1)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit))

		partSize = decidePartSize(PartSizeUnit * UploadParts)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit))

		partSize = decidePartSize(PartSizeUnit*UploadParts + 1)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit*2))

		partSize = decidePartSize(PartSizeUnit*2*UploadParts - 1)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit*2))

		partSize = decidePartSize(PartSizeUnit * 2 * UploadParts)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit*2))

		partSize = decidePartSize(PartSizeUnit*2*UploadParts + 1)
		Expect(partSize).Should(BeNumerically("==", PartSizeUnit*3))
	})
})

// zeroReader is an unseekable reader that yields zero bytes.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

var _ = Describe("S3Bucket multipart checksum", func() {
	// putMultipart uploads PartSizeUnit+1 bytes to a fake S3 server, which forces
	// a two-part upload, and returns the request headers of each UploadPart.
	putMultipart := func(optFns ...func(*s3.Options)) []http.Header {
		var mu sync.Mutex
		var parts []http.Header
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			q := r.URL.Query()
			switch {
			case r.Method == http.MethodPost && q.Has("uploads"):
				fmt.Fprint(w, `<InitiateMultipartUploadResult><Bucket>test</Bucket><Key>key</Key><UploadId>uid</UploadId></InitiateMultipartUploadResult>`)
			case r.Method == http.MethodPut && q.Has("partNumber"):
				mu.Lock()
				parts = append(parts, r.Header.Clone())
				mu.Unlock()
				w.Header().Set("ETag", `"etag"`)
			case r.Method == http.MethodPost && q.Has("uploadId"):
				fmt.Fprint(w, `<CompleteMultipartUploadResult><Bucket>test</Bucket><Key>key</Key><ETag>"etag"</ETag></CompleteMultipartUploadResult>`)
			default:
				w.WriteHeader(http.StatusNotImplemented)
			}
		}))
		defer srv.Close()

		opts := append([]func(*s3.Options){
			WithEndpointURL(srv.URL),
			WithPathStyle(),
			WithRegion("us-east-1"),
			WithCredentials(credentials.NewStaticCredentialsProvider("ak", "sk", "")),
		}, optFns...)
		b, err := NewS3Bucket("test", opts...)
		Expect(err).NotTo(HaveOccurred())

		err = b.Put(context.Background(), "key", io.LimitReader(zeroReader{}, PartSizeUnit+1), PartSizeUnit+1)
		Expect(err).NotTo(HaveOccurred())
		Expect(parts).To(HaveLen(2))
		return parts
	}

	hasChecksum := func(h http.Header) bool {
		for k := range h {
			if strings.HasPrefix(strings.ToLower(k), "x-amz-checksum-") || strings.EqualFold(k, "X-Amz-Trailer") {
				return true
			}
		}
		return false
	}

	It("should send optional checksums on parts by default", func() {
		for _, h := range putMultipart() {
			Expect(hasChecksum(h)).To(BeTrue(), "headers: %v", h)
		}
	})

	It("should omit optional checksums on parts when RequestChecksumCalculation is WhenRequired", func() {
		parts := putMultipart(func(o *s3.Options) {
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		})
		for _, h := range parts {
			Expect(hasChecksum(h)).To(BeFalse(), "headers: %v", h)
		}
	})
})
