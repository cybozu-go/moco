# Make object storage selectable

## Context

The only object storage interface currently supported by MOCO for storing backups is Amazon S3.
Amazon S3 interfaces are available for many object storage.
As a result, MOCO is currently able to support many object storage.

However, there are cases that are not supported.

* [cybozu-go/moco#427](https://github.com/cybozu-go/moco/issues/427)

The aws-sdk-go v2 client used by MOCO cannot use Google Cloud Storage which supports Amazon S3 API compatibility.[^1]

[^1]: https://github.com/aws/aws-sdk-go-v2/issues/1816

If there is motivation to support non-Amazon S3 compatible object storage other than this issue,
MOCO's current API is not capable of supporting it.

As an example Azure Blob Storage does not support S3 API. (But, proxy exists[^2])

[^2]: https://devblogs.microsoft.com/cse/2016/05/22/access-azure-blob-storage-from-your-apps-using-s3-api/

Therefore, this proposal proposes extensions to MOCO's API to enable support for object storage other than Amazon S3.

## Goals

* No breaking changes
* Extend API to allow non-Amazon S3 compatible object storage

## Non-goals

* Only proposes API extensions to enable non-S3 compatible object storage
  * The implementation to support other object storage is carried out in other processes

## ActualDesign

Add `.spec.jobConfig.bucketConfig.backendType` field to BackupPolicy.

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: BackupPolicy
metadata:
  namespace: backup
  name: daily
spec:
  jobConfig:
...
    bucketConfig:
      backendType: s3 # added
      bucketName: moco
      endpointURL: http://minio.default.svc:9000
      usePathStyle: true
...
```

The `.spec.jobConfig.bucketConfig.backendType` is the identifier of the storage provider to use.
Defaults to `s3`.

Defaulting to `s3` is appropriate because the Amazon S3 API is supported by many storage providers and for backward compatibility.
moco-controller treats as S3 if `backendType` is empty.

```go
type BucketConfig struct {
...
	// +kubebuilder:validation:Enum=s3
	// +kubebuilder:default=s3
	// +optional
	BackendType string `json:"backendType,omitempty"`
...
}
```

When extending the object storage supported by MOCO, add the type of Enum that can be specified in `backendType`.
As an example, adding support for Google Cloud Storage (GCS) would look like this.

```diff
- // +kubebuilder:validation:Enum=s3
+ // +kubebuilder:validation:Enum=s3;gcs
```

moco-controller reads the `backendType` and switches the client to use.
