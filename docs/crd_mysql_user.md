MySQLUser
=========

`MySQLUser` is a custom resource definition (CRD) that represents a MySQL user.
See [MySQL document](https://dev.mysql.com/doc/refman/8.0/en/create-user.html) for the details.

| Field        | Type                                | Description                                                           |
| ------------ | ----------------------------------- | --------------------------------------------------------------------- |
| `apiVersion` | string                              | APIVersion.                                                           |
| `kind`       | string                              | Kind.                                                                 |
| `metadata`   | [ObjectMeta]                        | Standard object's metadata with a special annotation described below. |
| `spec`       | [MySQLUserSpec](#MySQLUserSpec)     | Specification of the user.                                            |
| `status`     | [MySQLUserStatus](#MySQLUserStatus) | Most recently observed status of the user.                            |

MySQLUserSpec
-------------

| Field       | Type                                      | Description                                                                                                    |
| ----------- | ----------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `roles`     | []string                                  | A set of [MySQL roles](https://dev.mysql.com/doc/refman/8.0/en/roles.html) to access data.                     |
| `tls`       | boolean                                   | Use TLS if `true`. Default is `false`.                                                                         |
| `resources` | [UserResourceOption](#UserResourceOption) | Specification of [MySQL account resource limits](https://dev.mysql.com/doc/refman/8.0/en/user-resources.html). |
| `comment`   | string                                    | Comment for the user.                                                                                          |
| `attribute` | string                                    | Attribute for the user. It should be a valid JSON.                                                             |

UserResourceOption
------------------

| Field                   | Type | Description                                                                                      |
| ----------------------- | ---- | ------------------------------------------------------------------------------------------------ |
| `maxQueriesPerHour`     | int  | The number of queries an account can issue per hour. Default is zero (no limits).                |
| `maxUpdatesPerHour`     | int  | The number of updates an account can issue per hour. Default is zero (no limits).                |
| `maxConnectionsPerHour` | int  | The number of times an account can connect to the server per hour. Default is zero (no limits).  |
| `maxUserConnections`    | int  | The number of simultaneous connections to the server by an account. Default is zero (no limits). |

MySQLUserStatus
---------------

| Field   | Type   | Description                           |
| ------- | ------ | ------------------------------------- |
| `phase` | string | The phase of user creation or update. |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
