# Known issues

This document lists the known issues of MOCO.

- [Multi-threaded replication](#multi-threaded-replication)

## Multi-threaded replication

_Status: Resolved_

If you use MOCO with MySQL version 8.0.25 or earlier, you should not configure the replicas with `replica_parallel_workers` > 1.
Multi-threaded replication will cause the replica to fail to resume after the crash.

Currently, MOCO does not support MySQL version 8.0.25 or earlier, so this issue does not occur.
