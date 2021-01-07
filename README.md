[![GitHub release](https://img.shields.io/github/release/cybozu-go/moco.svg?maxAge=60)][releases]
[![CircleCI](https://circleci.com/gh/cybozu-go/moco.svg?style=svg)](https://circleci.com/gh/cybozu-go/moco)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/cybozu-go/moco)](https://pkg.go.dev/github.com/cybozu-go/moco)
[![Go Report Card](https://goreportcard.com/badge/github.com/cybozu-go/moco)](https://goreportcard.com/report/github.com/cybozu-go/moco)

# MOCO

MOCO is a Kubernetes operator for MySQL.
Its primary function is to manage a cluster of MySQL using binlog-based, semi-synchronous replication.

MOCO is designed for the following properties:

- Durability
  - Do not lose any data under a given degree of faults.
- Availability
  - Keep the MySQL cluster available under a given degree of faults.
- Business Continuity
  - Perform a quick recovery if some failure is occurred.

## Features

TBD

## Supported MySQL versions

8.0.18 and 8.0.20

## Documentation

[docs](docs/) directory contains documents about designs and specifications.

## Docker images

Docker images are available on [Quay.io](https://quay.io/repository/cybozu/moco)

## License

MOCO is licensed under MIT license.

[releases]: https://github.com/cybozu-go/moco/releases
[godoc]: https://godoc.org/github.com/cybozu-go/moco
