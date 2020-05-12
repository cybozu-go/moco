[![GitHub release](https://img.shields.io/github/release/cybozu-go/myso.svg?maxAge=60)][releases]
[![CircleCI](https://circleci.com/gh/cybozu-go/myso.svg?style=svg)](https://circleci.com/gh/cybozu-go/myso)
[![GoDoc](https://godoc.org/github.com/cybozu-go/myso?status.svg)][godoc]
[![Go Report Card](https://goreportcard.com/badge/github.com/cybozu-go/myso)](https://goreportcard.com/report/github.com/cybozu-go/myso)
[![Docker Repository on Quay](https://quay.io/repository/cybozu/myso/status "Docker Repository on Quay")](https://quay.io/repository/cybozu/myso)

MySO
====

MySO is a MySQL operator to construct and manage lossless semi-synchronous replicated MySQL servers using binary logs.

MySO is designed for the following properties:

- Integrity
  - Do not lose any data under a given degree of faults.
- Availability
  - Keep the MySQL cluster available under a given degree of faults.
- Serviceability
  - Perform a quick recovery by combining full backup and binary logs.

Features
--------

TBD

Supported MySQL versions
------------------------

\>=8.0.20

Documentation
--------------

[docs](docs/) directory contains documents about designs and specifications.

License
-------

MySO is licensed under MIT license.

[releases]: https://github.com/cybozu-go/myso/releases
[godoc]: https://godoc.org/github.com/cybozu-go/myso
