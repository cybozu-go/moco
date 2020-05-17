ObjectStorage
=============

`ObjectStorage` is a custom resource definition (CRD) that represents
an object storage for storing binlog and dump.

| Field        | Type                                    | Description                          |
| ------------ | --------------------------------------- | ------------------------------------ |
| `apiVersion` | string                                  | APIVersion.                          |
| `kind`       | string                                  | Kind.                                |
| `metadata`   | [ObjectMeta]                            | Standard object's metadata.          |
| `spec`       | [ObjectStorageSpec](#ObjectStorageSpec) | Specification of the object storage. |

ObjectStorageSpec
-----------------

| Field        | Type            | Required | Description                                                           |
| ------------ | --------------- | -------- | --------------------------------------------------------------------- |
| `endpoint`   | [Value](#Value) | Yes      | Endpoint of object storage.                                           |
| `bucket`     | [Value](#Value) | Yes      | Bucket name.                                                          |
| `region`     | [Value](#Value) | No       | Region of object storage.                                             |
| `prefix`     | string          | No       | Prefix to object names.                                               |
| `secretName` | string          | No       | Secret name created by the controller. This contains credential info. |

Value
-----

| Field       | Type              | Description                                                      |
| ----------- | ----------------- | ---------------------------------------------------------------- |
| `value`     | string            | Value of this field. Cannot be used if `valueFrom` is not empty. |
| `valueFrom` | [Source](#Source) | Source for the value. Cannot be used if `value` is not empty.    |

Source
------

| Field             | Type                   | Description                   |
| ----------------- | ---------------------- | ----------------------------- |
| `configMapKeyRef` | [ConfigMapKeySelector] | Selects a key of a ConfigMap. |

[ObjectMeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[ConfigMapKeySelector]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#configmapkeyselector-v1-core
