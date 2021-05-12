package moco

const (
	// Version is the MOCO version
	Version = "0.8.3"

	// FluentBitImage is the image for slow-log sidecar container.
	FluentBitImage = "quay.io/cybozu/fluent-bit:1.7.4.1"

	// ExporterImage is the image for mysqld_exporter sidecar container.
	ExporterImage = "quay.io/cybozu/mysqld_exporter:0.13.0-rc.0.1"
)
