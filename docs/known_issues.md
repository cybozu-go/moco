# Known issues

This document lists the known issues of MOCO.

- [Multi-threaded replication](#multi-threaded-replication)

## Multi-threaded replication

_Status: not fixed as of MOCO v0.9.5_

If you use MOCO with MySQL version 8.0.25 or earlier, you should not configure the replicas with `slave_parallel_workers` > 1.
Multi-threaded replication will cause the replica to fail to resume after the crash.

This issue is registered as https://github.com/cybozu-go/moco/issues/322 and will be addressed at no distant date.
