package moco

const (
	// Version is the MOCO version
	Version = "0.34.0"

	// FluentBitImage is the image for slow-log sidecar container.
	FluentBitImage = "ghcr.io/cybozu-go/moco/fluent-bit:5.0.2.1"

	// ExporterImage is the image for mysqld_exporter sidecar container.
	ExporterImage = "ghcr.io/cybozu-go/moco/mysqld_exporter:0.19.0.1"
)
