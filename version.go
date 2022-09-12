package moco

const (
	// Version is the MOCO version
	Version = "0.13.0"

	// FluentBitImage is the image for slow-log sidecar container.
	FluentBitImage = "quay.io/cybozu/fluent-bit:1.9.7.2"

	// ExporterImage is the image for mysqld_exporter sidecar container.
	ExporterImage = "quay.io/cybozu/mysqld_exporter:0.14.0.2"
)
