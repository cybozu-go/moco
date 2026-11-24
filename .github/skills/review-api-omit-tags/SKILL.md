---
name: review-api-omit-tags
description: 'Review the json omitempty and omitzero tags on fields in api/v1beta2/*_types.go. Use when reviewing or changing json struct tags in MOCO API type files.'
argument-hint: 'Name the api/v1beta2 type file or types to review, or leave empty to review all *_types.go files'
---

# Review API Type Omit Tags

Use this workflow to review the `json` struct tags of fields in `api/v1beta2/*_types.go`. Check only the omit option of each tag.

## Scope

- Review the fields declared in `api/v1beta2/*_types.go`.
- Skip embedded fields tagged `json:",inline"`, such as `metav1.TypeMeta`.
- Do not review other files, such as `zz_generated.deepcopy.go`.

## Rule: Choose the Omit Option by Go Type

Choose the option from the Go type of the field. For a named type, use its underlying type.

| Go field type | Required option | Example |
| --- | --- | --- |
| `int`, `int32`, `int64`, `string`, `bool` (including named types based on them) | `omitempty` for optional fields; no omit option for required fields (see Exceptions) | `` Threads int `json:"threads,omitempty"` `` in `job_types.go` |
| Pointer of scalar type `*T` | `omitempty` | `` TimeZone *string `json:"timeZone,omitempty"` `` in `backuppolicy_types.go` |
| Slice `[]T` or map `map[K]V` | `omitzero` | `` Collectors []string `json:"collectors,omitzero"` `` in `mysqlcluster_types.go` |
| Struct value (not a pointer) | `omitempty,omitzero` | `` ReconcileInfo ReconcileInfo `json:"reconcileInfo,omitempty,omitzero"` `` in `mysqlcluster_types.go` |

Why the options differ:

- `encoding/json` never omits a struct value with `omitempty` alone. A struct needs `omitzero`.
- `omitzero` on a slice or map omits only a nil value. An empty, non-nil value is still encoded as `[]` or `{}`.
- `omitempty` omits zero values of scalar types, so it fits optional `int`, `string`, and `bool`.
- `omitempty` omits a nil pointer, so an unset pointer field is absent from the JSON.

## Exceptions

- `metav1.ObjectMeta`, `metav1.ListMeta`, and `[]metav1.Condition` use `omitempty` only, not `omitzero`. By convention, as in [the Cluster API](https://github.com/kubernetes-sigs/cluster-api/blob/main/api/core/v1beta2/cluster_types.go) `ClusterStatus.Conditions` field.
- A required `int`, `string`, or `bool` field must not have `omitempty`. If its zero value is valid, `omitempty` must not be used because it drops the zero value from the JSON. If its zero value is invalid (rejected by validation), either option is acceptable. Example: `` SourceIndex int `json:"sourceIndex"` `` in `mysqlcluster_types.go`, where `0` is a valid ordinal and the field is marked `+required`.

## References

- [Kubernetes API conventions](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md): read "Optional vs. Required" and "Serialization of optional/required fields" for the background.
- [Markers for CRD validations](https://book.kubebuilder.io/reference/markers/crd-validation.html): read this when a finding depends on validation markers.
- [Markers for CRD processing](https://book.kubebuilder.io/reference/markers/crd-processing.html): read this when a finding depends on processing markers.
