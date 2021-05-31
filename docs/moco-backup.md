# `moco-backup`

`moco-backup` command is used in `ghcr.io/cybozu-go/moco-backup` container.
Normally, users need not take care of this command.

## Environment variables

`moco-backup` takes configurations of S3 API from environment variables.
For details, read documentation of [`EnvConfig` in github.com/aws/aws-sdk-go-v2/config][EnvConfig].

It also requires `MYSQL_PASSWORD` environment variable to be set.

## Global command-line flags

```
Global Flags:
      --endpoint string   S3 API endpoint URL
      --region string     AWS region
      --threads int       The number of threads to be used (default 4)
      --use-path-style    Use path-style S3 API
      --work-dir string   The writable working directory (default "/work")
```

## Subcommands

### `backup` subcommand

Usage: `moco-backup backup BUCKET NAMESPACE NAME`

- `BUCKET`: The bucket name.
- `NAMESPACE`: The namespace of the MySQLCluster.
- `NAME`: The name of the MySQLCluster.

### `restore subcommand

Usage: `moco-backup restore BUCKET SOURCE_NAMESPACE SOURCE_NAME NAMESPACE NAME YYYYMMDD-hhmmss`

- `BUCKET`: The bucket name.
- `SOURCE_NAMESPACE`: The source MySQLCluster's namespace.
- `SOURCE_NAME`: The source MySQLCluster's name.
- `NAMESPACE`: The target MySQLCluster's namespace.
- `NAME`: The target MySQLCluster's name.
- `YYYYMMDD-hhmmss`: The point-in-time to restore data.  e.g. `20210523-150423`

[EnvConfig]: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config#EnvConfig
