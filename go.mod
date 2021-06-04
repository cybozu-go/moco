module github.com/cybozu-go/moco

go 1.16

require (
	github.com/aws/aws-sdk-go-v2 v1.6.0
	github.com/aws/aws-sdk-go-v2/config v1.3.0
	github.com/aws/aws-sdk-go-v2/credentials v1.2.1
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.2.2
	github.com/aws/aws-sdk-go-v2/service/s3 v1.9.0
	github.com/cybozu-go/moco-agent v0.6.6
	github.com/go-logr/logr v0.4.0
	github.com/go-logr/stdr v0.4.0
	github.com/go-sql-driver/mysql v1.6.0
	github.com/google/go-cmp v0.5.6
	github.com/jmoiron/sqlx v1.3.4
	github.com/onsi/ginkgo v1.16.2
	github.com/onsi/gomega v1.12.0
	github.com/prometheus/client_golang v1.10.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.25.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	go.uber.org/zap v1.17.0
	google.golang.org/grpc v1.38.0
	k8s.io/api v0.20.7
	k8s.io/apimachinery v0.20.7
	k8s.io/cli-runtime v0.20.7
	k8s.io/client-go v0.20.7
	k8s.io/klog v1.0.0
	k8s.io/kubectl v0.20.7
	k8s.io/utils v0.0.0-20210521133846-da695404a2bc
	sigs.k8s.io/controller-runtime v0.8.3
)
