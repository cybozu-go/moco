package moco

const (
	// Version is the MOCO version
	Version = "0.16.1"

	// FluentBitImage is the image for slow-log sidecar container.
	FluentBitImage = "quay.io/cybozu/fluent-bit:2.0.9.1"

	// ExporterImage is the image for mysqld_exporter sidecar container.
	ExporterImage = "quay.io/cybozu/mysqld_exporter:0.14.0.4"
)
