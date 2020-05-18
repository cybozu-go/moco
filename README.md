[![GitHub release](https://img.shields.io/github/release/cybozu-go/myso.svg?maxAge=60)][releases]
[![CircleCI](https://circleci.com/gh/cybozu-go/myso.svg?style=svg)](https://circleci.com/gh/cybozu-go/myso)
[![GoDoc](https://godoc.org/github.com/cybozu-go/myso?status.svg)][godoc]
[![Go Report Card](https://goreportcard.com/badge/github.com/cybozu-go/myso)](https://goreportcard.com/report/github.com/cybozu-go/myso)

# MySO

MySO is a Kubernetes operator for MySQL.
Its primary function is to manage a cluster of MySQL using binlog-based, semi-synchronous replication.

MySO is designed for the following properties:

- Durability
  - Do not lose any data under a given degree of faults.
- Availability
  - Keep the MySQL cluster available under a given degree of faults.
- Business Continuity
  - Perform a quick recovery if some failure is occurred.

## Features

TBD

## Supported MySQL versions

\>=8.0.20

## Documentation

[docs](docs/) directory contains documents about designs and specifications.

## Docker images

Docker images are available on [Quay.io](https://quay.io/repository/cybozu/myso)

## License

MySO is licensed under MIT license.

[releases]: https://github.com/cybozu-go/myso/releases
[godoc]: https://godoc.org/github.com/cybozu-go/myso
