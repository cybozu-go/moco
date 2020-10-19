Events
======

MOCO records [Event] on MOCO's custom resources when the following specific events occurs.

## `MySQLCluster`

| Reason                              | Message                                                                                                   |
| :---------------------------------- | :-------------------------------------------------------------------------------------------------------- |
| Initialization Succeeded            | Initialization phase finished successfully.                                                               |
| Initialization Failed               | Initialization phase failed. err=<error message>                                                          |
| Waiting All Instances Available     | Waiting for all instances to become connected from MOCO. unavailable=<instance indexes>                   |
| Violation Occurred                  | Constraint violation occurred. Please resolve via manual operation. err=<error message>                   |
| Waiting Relay Log Execution         | Waiting relay log execution on replica instance(s).                                                       |
| Restoring Replica Instance(s)       | Restoring replica instance(s) by cloning with primary instance.                                           |
| Primary Changed                     | Primary instance was changed from <instance index> to <instance index> because of failover or switchover. |
| Clustering Completed and Synced     | Clustering are completed. All instances are synced.                                                       |
| Clustering Completed but Not Synced | Clustering are completed. Some instance(s) are not synced. out_of_sync=<instance indexes>                 |

## `MySQLBackupSchedule`

TBD

## `MySQLSwitchOverJob`

TBD

[Event]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#event-v1-core
