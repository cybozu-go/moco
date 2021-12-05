# Summary

[MOCO](README.md)

# User manual

- [Getting started](getting_started.md)
    - [Deploying MOCO](setup.md)
    - [Helm Chart](helm.md)
    - [Installing kubectl-moco](install-plugin.md)
- [Usage](usage.md)
- [Advanced topics](advanced.md)
    - [Building your own imge](custom-mysqld.md)
- [Troubleshooting](troubles.md)

# References

- [Custom resources](crd.md)
    - [MySQLCluster v1beta1](crd_mysqlcluster_v1beta1.md)
    - [MySQLCluster v1beta2](crd_mysqlcluster_v1beta2.md)
    - [BackupPolicy](crd_backuppolicy.md)
- [Commands](commands.md)
    - [kubectl-moco](kubectl-moco.md)
    - [moco-controller](moco-controller.md)
    - [moco-backup](moco-backup.md)
- [Metrics](metrics.md)

# Developer documents

- [Design notes](notes.md)
    - [Goals](design.md)
    - [Reconciliation](reconcile.md)
    - [Clustering](clustering.md)
    - [Backup and restore](backup.md)
    - [Upgrading mysqld](upgrading.md)
    - [Security](security.md)
