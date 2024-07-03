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
    - [Customize system container](customize-system-container.md)
    - [Change volumeClaimTemplates](change-pvc-template.md)
    - [Rollout strategy](rolling-update-strategy.md)
- [Known issues](known_issues.md)

# References

- [Custom resources](crd.md)
    - [MySQLCluster v1beta2](crd_mysqlcluster_v1beta2.md)
    - [BackupPolicy v1beta2](crd_backuppolicy_v1beta2.md)
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
