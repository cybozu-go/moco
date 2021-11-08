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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	. "github.com/onsi/ginkgo"
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
			"minio/minio", "server", "/data").Run()
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
			},
		}))
		Expect(err).NotTo(HaveOccurred())
		client := s3.NewFromConfig(cfg,
			s3.WithEndpointResolver(s3.EndpointResolverFromURL("http://localhost:9000")),
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
		os.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
		os.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")

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

	It("should calculate the partSize correctly", func() {
		partSize := decidePartSize(600 << 30)
		Expect(partSize).Should(BeNumerically("==", DefaultPartSize))

		partSize = decidePartSize(700 << 30)
		Expect(partSize).Should(BeNumerically("==", 200<<20))
	})
})
