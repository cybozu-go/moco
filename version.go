package moco

const (
	// Version is the MOCO version
	Version = "0.27.1"

	// FluentBitImage is the image for slow-log sidecar container.
	FluentBitImage = "ghcr.io/cybozu-go/moco/fluent-bit:3.1.7.1"

	// ExporterImage is the image for mysqld_exporter sidecar container.
	ExporterImage = "ghcr.io/cybozu-go/moco/mysqld_exporter:0.16.0.1"
)
