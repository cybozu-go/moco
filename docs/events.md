Events
======

MOCO records [Event] on MOCO's custom resources when the following specific events occurs.

## `MySQLCluster`

| Type    | Reason                              | Message                                                                                                   |
| :------ | :---------------------------------- | :-------------------------------------------------------------------------------------------------------- |
| Normal  | Initialization Succeeded            | Initialization phase finished successfully.                                                               |
| Warning | Initialization Failed               | Initialization phase failed. err=<error message>                                                          |
| Normal  | Waiting All Instances Available     | Waiting for all instances to become connected from MOCO. unavailable=<instance indexes>                   |
| Warning | Violation Occurred                  | Constraint violation occurred. Please resolve via manual operation. err=<error message>                   |
| Normal  | Waiting Relay Log Execution         | Waiting relay log execution on replica instance(s).                                                       |
| Normal  | Restoring Replica Instance(s)       | Restoring replica instance(s) by cloning with primary instance.                                           |
| Normal  | Primary Changed                     | Primary instance was changed from <instance index> to <instance index> because of failover or switchover. |
| Normal  | Intermediate Primary Configured     | Intermediate primary instance was configured with host=<address>                                          |
| Normal  | Intermediate Primary Unset          | Intermediate primary instance was unset.                                                                  |
| Normal  | Clustering Completed and Synced     | Clustering are completed. All instances are synced.                                                       |
| Warning | Clustering Completed but Not Synced | Clustering are completed. Some instance(s) are not synced. out_of_sync=<instance indexes>                 |

## `MySQLBackupSchedule`

TBD

## `MySQLSwitchOverJob`

TBD

[Event]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.18/#event-v1-core
