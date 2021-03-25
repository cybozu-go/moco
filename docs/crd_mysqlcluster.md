
### Custom Resources

* [MySQLCluster](#mysqlcluster)

### Sub Resources

* [MySQLClusterCondition](#mysqlclustercondition)
* [MySQLClusterList](#mysqlclusterlist)
* [MySQLClusterSpec](#mysqlclusterspec)
* [MySQLClusterStatus](#mysqlclusterstatus)
* [ObjectMeta](#objectmeta)
* [PersistentVolumeClaim](#persistentvolumeclaim)
* [PodTemplateSpec](#podtemplatespec)
* [ReconcileInfo](#reconcileinfo)
* [ServiceTemplate](#servicetemplate)

#### MySQLCluster

MySQLCluster is the Schema for the mysqlclusters API

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ObjectMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ObjectMeta) | false |
| spec |  | [MySQLClusterSpec](#mysqlclusterspec) | false |
| status |  | [MySQLClusterStatus](#mysqlclusterstatus) | false |

[Back to Custom Resources](#custom-resources)

#### MySQLClusterCondition

MySQLClusterCondition defines the condition of MySQLCluster.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| type | Type is the type of the condition. | [MySQLClusterConditionType](https://pkg.go.dev/github.com/cybozu-go/moco/api/v1beta1#MySQLClusterConditionType) | true |
| status | Status is the status of the condition. | [corev1.ConditionStatus](https://pkg.go.dev/k8s.io/api/core/v1#ConditionStatus) | true |
| reason | Reason is a one-word CamelCase reason for the condition's last transition. | string | false |
| message | Message is a human-readable message indicating details about last transition. | string | false |
| lastTransitionTime | LastTransitionTime is the last time the condition transits from one status to another. | [metav1.Time](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Time) | true |

[Back to Custom Resources](#custom-resources)

#### MySQLClusterList

MySQLClusterList contains a list of MySQLCluster

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ListMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ListMeta) | false |
| items |  | [][MySQLCluster](#mysqlcluster) | true |

[Back to Custom Resources](#custom-resources)

#### MySQLClusterSpec

MySQLClusterSpec defines the desired state of MySQLCluster

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| replicas | Replicas is the number of instances. Available values are 1, 3, and 5. | int32 | false |
| podTemplate | PodTemplate is a `Pod` template for MySQL server container. | [PodTemplateSpec](#podtemplatespec) | true |
| volumeClaimTemplates | VolumeClaimTemplates is a list of `PersistentVolumeClaim` templates for MySQL server container. A claim named \"mysql-data\" must be included in the list. | [][PersistentVolumeClaim](#persistentvolumeclaim) | true |
| serviceTemplate | ServiceTemplate is a `Service` template for both primary and replicas. | *[ServiceTemplate](#servicetemplate) | false |
| mysqlConfigMapName | MySQLConfigMapName is a `ConfigMap` name of MySQL config. | *string | false |
| replicationSourceSecretName | ReplicationSourceSecretName is a `Secret` name which contains replication source info. If this field is given, the `MySQLCluster` works as an intermediate primary. | *string | false |
| serverIDBase | ServerIDBase, if set, will become the base number of server-id of each MySQL instance of this cluster.  For example, if this is 100, the server-ids will be 100, 101, 102, and so on. If the field is not given or zero, MOCO automatically sets a random positive integer. | int32 | false |
| maxDelaySeconds | MaxDelaySeconds, if set, configures the readiness probe of mysqld container. For a replica mysqld instance, if it is delayed to apply transactions over this threshold, the mysqld instance will be marked as non-ready. The default is 60 seconds. | int | false |
| logRotationSchedule | LogRotationSchedule is a schedule in Cron format for MySQL log rotation. If not set, the default is to rotate logs every 5 minutes. | string | false |
| restore | Restore is the specification to perform Point-in-Time-Recovery from existing cluster. If this field is filled, start restoring. This field is unable to be updated. | *[RestoreSpec](#restorespec) | false |
| disableErrorLogContainer | DisableErrorLogContainer controls whether to add a log agent container name of the \"err-log\" to handle mysqld error logs. If set to true, no log agent container will be added. The default is false. If false and the user-defined \".spec.podTemplate.spec.containers\" contained a container named \"err-log\", it will be merged with the default container definition using StrategicMergePatch. | bool | false |
| disableSlowQueryLogContainer | DisableSlowQueryLogContainer controls whether to add a log agent container name of the \"slow-log\" to handle mysqld slow query logs. If set to true, no log agent container will be added. The default is false. If false and the user-defined \".spec.podTemplate.spec.containers\" contained a container named \"slow-log\", it will be merged with the default container definition using StrategicMergePatch. | bool | false |

[Back to Custom Resources](#custom-resources)

#### MySQLClusterStatus

MySQLClusterStatus defines the observed state of MySQLCluster

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| conditions | Conditions is an array of conditions. | [][MySQLClusterCondition](#mysqlclustercondition) | false |
| currentPrimaryIndex | CurrentPrimaryIndex is the index of the current primary Pod in StatefulSet. Initially, this is zero. | int | true |
| syncedReplicas | SyncedReplicas is the number of synced instances including the primary. | int | false |
| errantReplicas | ErrantReplicas is the number of instances that have errant transactions. | int | false |
| errantReplicaList | ErrantReplicaList is the list of indices of errant replicas. | []int | false |
| reconcileInfo | ReconcileInfo represents version information for reconciler. | [ReconcileInfo](#reconcileinfo) | true |

[Back to Custom Resources](#custom-resources)

#### ObjectMeta

ObjectMeta is metadata of objects. This is partially copied from metav1.ObjectMeta.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| name | Name is the name of the object. | string | false |
| labels | Labels is a map of string keys and values. | map[string]string | false |
| annotations | Annotations is a map of string keys and values. | map[string]string | false |

[Back to Custom Resources](#custom-resources)

#### PersistentVolumeClaim

PersistentVolumeClaim is a user's request for and claim to a persistent volume. This is slightly modified from corev1.PersistentVolumeClaim.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata | Standard object's metadata. | [ObjectMeta](#objectmeta) | true |
| spec | Spec defines the desired characteristics of a volume requested by a pod author. | [corev1.PersistentVolumeClaimSpec](https://pkg.go.dev/k8s.io/api/core/v1#PersistentVolumeClaimSpec) | true |

[Back to Custom Resources](#custom-resources)

#### PodTemplateSpec

PodTemplateSpec describes the data a pod should have when created from a template. This is slightly modified from corev1.PodTemplateSpec.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata | Standard object's metadata.  The name in this metadata is ignored. | [ObjectMeta](#objectmeta) | false |
| spec | Specification of the desired behavior of the pod. The name of the MySQL server container in this spec must be `mysqld`. | [corev1.PodSpec](https://pkg.go.dev/k8s.io/api/core/v1#PodSpec) | true |

[Back to Custom Resources](#custom-resources)

#### ReconcileInfo



| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| generation | Generation is the `metadata.generation` value of the last reconciliation. See also https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#status-subresource | int64 | false |
| reconcileVersion | ReconcileVersion is the version of the operator reconciler. | int | true |

[Back to Custom Resources](#custom-resources)

#### ServiceTemplate

ServiceTemplate defines the desired spec and annotations of Service

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata | Standard object's metadata.  Only `annotations` and `labels` are valid. | [ObjectMeta](#objectmeta) | false |
| spec | Spec is the ServiceSpec | *[corev1.ServiceSpec](https://pkg.go.dev/k8s.io/api/core/v1#ServiceSpec) | false |

[Back to Custom Resources](#custom-resources)
