# Trouble shooting

## Failed to initialize data directory for `mysqld`

If you see the following error message from an init container of `mysqld` Pod,

```
mysqld: Can't create directory '/var/lib/mysql/data/' (OS errno 13 - Permission denied)
2021-05-24T19:44:33.022939Z 0 [System] [MY-013169] [Server] /usr/local/mysql/bin/mysqld (mysqld 8.0.24) initializing of server in progress as process 12
2021-05-24T19:44:33.024090Z 0 [ERROR] [MY-013236] [Server] The designated data directory /var/lib/mysql/data/ is unusable. You can remove all files that the server added to it.
2021-05-24T19:44:33.024138Z 0 [ERROR] [MY-010119] [Server] Aborting
2021-05-24T19:44:33.024316Z 0 [System] [MY-010910] [Server] /usr/local/mysql/bin/mysqld: Shutdown complete (mysqld 8.0.24)  Source distribution.
```

the data directory is probably only writable for the `root` user.

To resolve the problem, add `fsGroup: 10000` to MySQLCluster as follows:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: default
  name: test
spec:
  podTemplate:
    spec:
      securityContext:
        fsGroup: 10000    # to make the data directory writable for `mysqld` container.
        fsGroupChangePolicy: "OnRootMismatch"  # available since k8s 1.20
...
```
