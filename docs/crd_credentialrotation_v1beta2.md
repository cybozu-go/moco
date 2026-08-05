
### Custom Resources

* [CredentialRotation](#credentialrotation)

### Sub Resources

* [CredentialRotationList](#credentialrotationlist)
* [CredentialRotationSpec](#credentialrotationspec)
* [CredentialRotationStatus](#credentialrotationstatus)

#### CredentialRotation

CredentialRotation is the Schema for the credentialrotations API

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ObjectMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ObjectMeta) | false |
| spec |  | [CredentialRotationSpec](#credentialrotationspec) | true |
| status |  | [CredentialRotationStatus](#credentialrotationstatus) | false |

[Back to Custom Resources](#custom-resources)

#### CredentialRotationList

CredentialRotationList contains a list of CredentialRotation

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| metadata |  | [metav1.ListMeta](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#ListMeta) | false |
| items |  | [][CredentialRotation](#credentialrotation) | true |

[Back to Custom Resources](#custom-resources)

#### CredentialRotationSpec

CredentialRotationSpec defines the desired state of CredentialRotation. The target MySQLCluster is identified by the CR's own name and namespace (CredentialRotation name must equal MySQLCluster name).

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| rotationGeneration | RotationGeneration is a monotonically increasing counter. A fresh CR must be created with the value 1; the validating webhook rejects any other value at create time so the counter aligns 1:1 with the number of rotation cycles performed against this CR. Subsequent rotations are triggered by bumping this value via update. | int64 | true |
| discardGeneration | DiscardGeneration is a monotonically increasing counter that triggers the discard step. Must satisfy 0 <= discardGeneration <= rotationGeneration. A fresh CR must be created with the value 0; the validating webhook rejects any other value at create time. Bumping this value via update (typically to match rotationGeneration) signals the controller to discard the retained old password from the previous rotation. The bump is only honored while the CR is in the awaiting-discard steady state (DiscardReady=True, DualPassword=True). | int64 | true |

[Back to Custom Resources](#custom-resources)

#### CredentialRotationStatus

CredentialRotationStatus defines the observed state of CredentialRotation.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| observedGeneration | ObservedGeneration reflects the .metadata.generation that the controller has most recently reconciled. Clients (kstatus, ArgoCD, Flux) use this together with the RotationReady/DiscardReady conditions to determine whether the controller has caught up with the latest spec change. | int64 | true |
| conditions | Conditions represent the latest available observations of the rotation state. Three orthogonal observations are exposed: RotationReady, DiscardReady, and DualPassword. The internal workflow step is derived from their combination together with the spec/observed generation comparison; it is not stored on the CR. See docs/designdoc/credential_rotation_crd.md for the canonical Type/Reason definitions and the step matrix. | [][metav1.Condition](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Condition) | false |
| rotationID | RotationID is the UUID for the in-flight rotation cycle. Empty when no cycle is active. The value, if non-empty, is a canonical 36-character UUID. | string | false |
| observedRotationGeneration | ObservedRotationGeneration is the last rotationGeneration whose rotation phase completed: RETAIN succeeded on every instance and the pending password was promoted to the controller Secret's current password. It is updated immediately after promotion, before Secret distribution and the StatefulSet rollout, so equality with spec.rotationGeneration does not imply that distribution, the rollout, or the full rotate-discard cycle has completed: RotationReady=True is only set at the very end of the full cycle (after the discard phase finalises). | int64 | true |
| observedDiscardGeneration | ObservedDiscardGeneration is the last discardGeneration that completed successfully. Equality with spec.discardGeneration is a necessary condition for the cycle to leave the discard phase, but not sufficient on its own: DiscardReady=True is only set in the awaiting-discard steady state, which additionally requires DualPassword=True and the post-distribute rollout to have settled. | int64 | true |

[Back to Custom Resources](#custom-resources)
