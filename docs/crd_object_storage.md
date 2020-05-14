# ObjectStorage

`ObjectStorage` is a custom resource definition (CRD) that represents
the object storage for storing binlog and dump.

| Field        | Type                                        | Description                               |
| ------------ | ------------------------------------------- | ----------------------------------------- |
| `apiVersion` | string                                      | APIVersion.                               |
| `kind`       | string                                      | Kind.                                     |
| `metadata`   | [ObjectMeta]                                | Standard object's metadata.               |
| `spec`       | [ObjectStorageSpec](#ObjectStorageStatus)   | Specification of desired state of binlog. |
| `status`     | [ObjectStorageStatus](#ObjectStorageStatus) | Most recently observed status of binlog.  |

## ObjectStorageSpec

| Field                  | Type            | Required | Description                                                           |
| ---------------------- | --------------- | -------- | --------------------------------------------------------------------- |
| `endpoint`             | [Value](#Value) | Yes      | Endpoint of object storage.                                           |
| `bucket`               | [Value](#Value) | Yes      | Bucket name.                                                          |
| `region`               | [Value](#Value) | No       | Region of object storage.                                             |
| `prefix`               | string          | No       | File name prefix.                                                     |
| `credentialSecretName` | string          | No       | Secret name created by the controller. This contains credential info. |

## Value

| Field       | Type                | Description                                                   |
| ----------- | ------------------- | ------------------------------------------------------------- |
| `value`     | string              | Value of this field.                                          |
| `valueFrom` | [`Source`](#Source) | Source for the value. Cannot be used if `value` is not empty. |

## Source

| Field             | Type                     | Description                   |
| ----------------- | ------------------------ | ----------------------------- |
| `configMapKeyRef` | [`ConfigMapKeySelector`] | Selects a key of a ConfigMap. |

[objectmeta]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta
[`configmapkeyselector`]: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/
