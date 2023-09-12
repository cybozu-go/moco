package moco

const (
	// Version is the MOCO version
	Version = "0.17.0"

	// FluentBitImage is the image for slow-log sidecar container.
	FluentBitImage = "ghcr.io/cybozu-go/moco/fluent-bit:2.1.8.2"

	// ExporterImage is the image for mysqld_exporter sidecar container.
	ExporterImage = "ghcr.io/cybozu-go/moco/mysqld_exporter:0.15.0.2"
)
