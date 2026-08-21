// Package log provides the logger shared by the MOCO components.
package log

import (
	"os"
	"slices"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zapgrpc"
	"google.golang.org/grpc/grpclog"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Setup creates a logger and registers it to controller-runtime, klog and gRPC,
// then returns it. Without the redirection, client-go and gRPC write their own
// text formats to stderr.
func Setup(opts *crzap.Options) logr.Logger {
	logger := newLogger(opts)

	crlog.SetLogger(logger)

	// Stack traces of the library entries consist of frames inside the libraries.
	libOpts := *opts
	libOpts.StacktraceLevel = zapcore.DPanicLevel
	libLogger := newZap(&libOpts)

	// ContextualLogger lets the libraries use the logger without going through klog.
	klog.SetLoggerWithOptions(zapr.NewLogger(libLogger).WithName("klog"), klog.ContextualLogger(true))

	// gRPC logs every connection state change at the info level. zap.IncreaseLevel
	// is not used here because it complains when the configured level is higher.
	grpcOpts := libOpts
	grpcOpts.Level = zap.LevelEnablerFunc(func(l zapcore.Level) bool {
		return l >= zapcore.WarnLevel && (opts.Level == nil || opts.Level.Enabled(l))
	})
	grpclog.SetLoggerV2(zapgrpc.NewLogger(newZap(&grpcOpts).Named("grpc")))

	return logger
}

// newLogger is equivalent to crzap.New(crzap.UseFlagOptions(opts)) except that
// the returned logger does not sample log entries. crzap.New drops entries when
// the same message is logged more than 100 times a second, which happens while
// reconciling a lot of MySQLClusters.
// https://github.com/kubernetes-sigs/controller-runtime/blob/v0.23.3/pkg/log/zap/zap.go#L170-L243
func newLogger(opts *crzap.Options) logr.Logger {
	return zapr.NewLogger(newZap(opts))
}

func newZap(opts *crzap.Options) *zap.Logger {
	o := *opts
	o.ZapOpts = slices.Clone(opts.ZapOpts)
	addDefaults(&o)

	sink := zapcore.AddSync(o.DestWriter)
	zapOpts := append(o.ZapOpts, zap.AddStacktrace(o.StacktraceLevel), zap.ErrorOutput(sink))
	core := zapcore.NewCore(&crzap.KubeAwareEncoder{Encoder: o.Encoder, Verbose: o.Development}, sink, o.Level)

	return zap.New(core, zapOpts...)
}

// addDefaults is a copy of the unexported crzap.Options.addDefaults.
func addDefaults(o *crzap.Options) {
	if o.DestWriter == nil {
		o.DestWriter = os.Stderr
	}

	if o.Development {
		if o.NewEncoder == nil {
			o.NewEncoder = newConsoleEncoder
		}
		if o.Level == nil {
			o.Level = zapcore.DebugLevel
		}
		if o.StacktraceLevel == nil {
			o.StacktraceLevel = zapcore.WarnLevel
		}
		o.ZapOpts = append(o.ZapOpts, zap.Development())
	} else {
		if o.NewEncoder == nil {
			o.NewEncoder = newJSONEncoder
		}
		if o.Level == nil {
			o.Level = zapcore.InfoLevel
		}
		if o.StacktraceLevel == nil {
			o.StacktraceLevel = zapcore.ErrorLevel
		}
	}

	if o.TimeEncoder == nil {
		o.TimeEncoder = zapcore.RFC3339TimeEncoder
	}

	if o.Encoder == nil {
		// Prepended so that EncoderConfigOptions can override it.
		encOpts := append([]crzap.EncoderConfigOption{func(ec *zapcore.EncoderConfig) {
			ec.EncodeTime = o.TimeEncoder
		}}, o.EncoderConfigOptions...)
		o.Encoder = o.NewEncoder(encOpts...)
	}
}

func newJSONEncoder(opts ...crzap.EncoderConfigOption) zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	for _, opt := range opts {
		opt(&encoderConfig)
	}
	return zapcore.NewJSONEncoder(encoderConfig)
}

func newConsoleEncoder(opts ...crzap.EncoderConfigOption) zapcore.Encoder {
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	for _, opt := range opts {
		opt(&encoderConfig)
	}
	return zapcore.NewConsoleEncoder(encoderConfig)
}
