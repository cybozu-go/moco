package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/pkg/bucket"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/spf13/cobra"
)

var commonArgs struct {
	workDir        string
	threads        int
	region         string
	endpointURL    string
	usePathStyle   bool
	backendType    string
	caCertFilePath string
}

func makeBucket(bucketName string) (bucket.Bucket, error) {
	switch commonArgs.backendType {
	case constants.BackendTypeS3:
		return makeS3Bucket(bucketName)
	case constants.BackendTypeGCS:
		return makeGCSBucket(bucketName)
	default:
		return makeS3Bucket(bucketName)
	}
}

func makeS3Bucket(bucketName string) (bucket.Bucket, error) {
	var opts []func(*s3.Options)
	if len(commonArgs.region) > 0 {
		opts = append(opts, bucket.WithRegion(commonArgs.region))
	}
	if len(commonArgs.endpointURL) > 0 {
		opts = append(opts, bucket.WithEndpointURL(commonArgs.endpointURL))
	}
	if commonArgs.usePathStyle {
		opts = append(opts, bucket.WithPathStyle())
	}
	if len(commonArgs.caCertFilePath) > 0 {
		caCertFile, err := os.ReadFile(commonArgs.caCertFilePath)
		if err != nil {
			return nil, err
		}
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if ok := caCertPool.AppendCertsFromPEM(caCertFile); !ok {
			return nil, fmt.Errorf("failed to add ca cert")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.RootCAs = caCertPool
		opts = append(opts, bucket.WithHTTPClient(&http.Client{
			Transport: transport,
		}))
	}
	return bucket.NewS3Bucket(bucketName, opts...)
}

func makeGCSBucket(bucketName string) (bucket.Bucket, error) {
	return bucket.NewGCSBucket(context.Background(), bucketName)
}

var mysqlPassword = os.Getenv("MYSQL_PASSWORD")

var rootCmd = &cobra.Command{
	Use:     "moco-backup",
	Version: moco.Version,
	Short:   "backup and restore MySQL data",
	Long:    "Backup and restore MySQL data.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		if len(mysqlPassword) == 0 {
			return errors.New("no MYSQL_PASSWORD environment variable")
		}
		if len(commonArgs.endpointURL) > 0 {
			_, err := url.Parse(commonArgs.endpointURL)
			if err != nil {
				return fmt.Errorf("invalid endpoint URL %s: %w", commonArgs.endpointURL, err)
			}
		}

		// mysqlsh command creates some files in $HOME.
		os.Setenv("HOME", commonArgs.workDir)
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&commonArgs.workDir, "work-dir", "/work", "The writable working directory")
	pf.IntVar(&commonArgs.threads, "threads", 4, "The number of threads to be used")
	pf.StringVar(&commonArgs.region, "region", "", "Region used for object storage API")
	pf.StringVar(&commonArgs.endpointURL, "endpoint", "", "Object storage API endpoint URL")
	pf.BoolVar(&commonArgs.usePathStyle, "use-path-style", false, "Use path-style S3 API")
	pf.StringVar(&commonArgs.backendType, "backend-type", "s3", "The identifier for the object storage to be used.")
	pf.StringVar(&commonArgs.caCertFilePath, "ca-certs", "", "Path to SSL CA certificate file instead of system default")
}
