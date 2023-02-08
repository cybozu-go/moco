package bucket

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"google.golang.org/api/option"
)

var _ = Describe("GCSBucket", func() {
	ctx := context.Background()
	var dataDir string

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "")
		Expect(err).NotTo(HaveOccurred())
		err = os.Mkdir(filepath.Join(dir, "test"), os.ModePerm)
		Expect(err).NotTo(HaveOccurred())
		dataDir = dir
		err = exec.Command("docker", "run", "--rm", "--name=fake-gcs-server", "-d", "-p", "4443:4443",
			"-v", fmt.Sprintf("%s:/data", dir),
			"fsouza/fake-gcs-server", "-scheme", "http", "-public-host=localhost:4443", "-port=4443").Run()
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			client, err := storage.NewClient(ctx, option.WithEndpoint("http://localhost:4443/storage/v1/"), option.WithoutAuthentication())
			if err != nil {
				return err
			}
			_, err = client.Bucket("test").Attrs(ctx)
			if err != nil {
				return err
			}
			return nil
		}, 60).Should(Succeed())
	})

	AfterEach(func() {
		exec.Command("docker", "kill", "fake-gcs-server").Run()
		time.Sleep(1 * time.Second)
		os.RemoveAll(dataDir)
	})

	It("should put and get objects", func() {
		b, err := NewGCSBucket(ctx, "test", option.WithEndpoint("http://localhost:4443/storage/v1/"), option.WithoutAuthentication())
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
		b, err := NewGCSBucket(ctx, "test", option.WithEndpoint("http://localhost:4443/storage/v1/"), option.WithoutAuthentication())
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
})
