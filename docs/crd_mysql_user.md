MySQLUser
=========

**NOTE**: This custom resource will not be implemented soon.

`MySQLUser` is a custom resource definition (CRD) that represents a MySQL user.
See [MySQL document](https://dev.mysql.com/doc/refman/8.0/en/create-user.html) for the details.

| Field        | Type                                | Description                                |
| ------------ | ----------------------------------- | ------------------------------------------ |
| `apiVersion` | string                              | APIVersion.                                |
| `kind`       | string                              | Kind.                                      |
| `metadata`   | [ObjectMeta]                        | Standard object's metadata.                |
| `spec`       | [MySQLUserSpec](#MySQLUserSpec)     | Specification of the user.                 |
| `status`     | [MySQLUserStatus](#MySQLUserStatus) | Most recently observed status of the user. |

MySQLUserSpec
-------------

| Field            | Type                                      | Required | Description                                           |
| ---------------- | ----------------------------------------- | -------- | ----------------------------------------------------- |
| `clusterName`    | string                                    | Yes      | Name of `MySQLCluster`.                               |
| `tls`            | boolean                                   | No       | Require TLS connection if `true`. Default is `false`. |
| `resources`      | [UserResourceOption](#UserResourceOption) | No       | Specification of [MySQL account resource limits].     |
| `comment`        | string                                    | No       | Comment for the user.                                 |
| `attribute`      | string                                    | No       | Attribute for the user. It should be a valid JSON.    |
| `privilegeRules` | \[\][PrivilegeRule](#PrivilegeRule)       | No       | A list of privilege rules.                            |

PrivilegeRule
-------------

| Field            | Type     | Required | Description                                                       |
| ---------------- | -------- | -------- | ----------------------------------------------------------------- |
| `name`           | string   | Yes      | Name of `PrivilegeRule`.                                          |
| `privilegeType`  | []string | Yes      | See [Privileges Supported by MySQL][GRANT statement].             |
| `privilegeLevel` | string   | Yes      | Target database and/or table (e.g. db_name.\*, db_name.tbl_name). |

UserResourceOption
------------------

| Field                   | Type | Required | Description                                                                                      |
| ----------------------- | ---- | -------- | ------------------------------------------------------------------------------------------------ |
| `maxQueriesPerHour`     | int  | No       | The number of queries an account can issue per hour. Default is zero (no limits).                |
| `maxUpdatesPerHour`     | int  | No       | The number of updates an account can issue per hour. Default is zero (no limits).                |
| `maxConnectionsPerHour` | int  | No       | The number of times an account can connect to the server per hour. Default is zero (no limits).  |
| `maxUserConnections`    | int  | No       | The number of simultaneous connections to the server by an account. Default is zero (no limits). |

MySQLUserStatus
---------------

| Field        | Type                                          | Description                                            |
| ------------ | --------------------------------------------- | ------------------------------------------------------ |
| `roles`      | []string                                      | The user has been updated or not.                      |
| `tls`        | boolean                                       | TLS is enabled or not.                                 |
| `resources`  | [UserResourceStatus](#UserResourceStatus)     | The [MySQL account resource limits] applied currently. |
| `comment`    | string                                        | The comment applied currently.                         |
| `attribute`  | string                                        | The attribute applied currently.                       |
| `conditions` | \[\][MySQLUserCondition](#MySQLUserCondition) | The array of conditions.                               |

UserResourceStatus
------------------

| Field                   | Type | Description                                                                                      |
| ----------------------- | ---- | ------------------------------------------------------------------------------------------------ |
| `maxQueriesPerHour`     | int  | The number of queries an account can issue per hour. Default is zero (no limits).                |
| `maxUpdatesPerHour`     | int  | The number of updates an account can issue per hour. Default is zero (no limits).                |
| `maxConnectionsPerHour` | int  | The number of times an account can connect to the server per hour. Default is zero (no limits).  |
| `maxUserConnections`    | int  | The number of simultaneous connections to the server by an account. Default is zero (no limits). |

MySQLUserCondition
------------------

| Field                | Type   | Required | Description                                                      |
| -------------------- | ------ | -------- | ---------------------------------------------------------------- |
| `type`               | string | Yes      | The type of condition.                                           |
| `status`             | Enum   | Yes      | Status of the condition. One of `True`, `False`, `Unknown`.      |
| `reason`             | string | No       | One-word CamelCase reason for the condition's last transition.   |
| `message`            | string | No       | Human-readable message indicating details about last transition. |
| `lastTransitionTime` | [Time] | Yes      | The last time the condition transit from one status to another.  |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[MySQL account resource limits]: https://dev.mysql.com/doc/refman/8.0/en/user-resources.html
[GRANT Statement]: https://dev.mysql.com/doc/refman/8.0/en/grant.html
