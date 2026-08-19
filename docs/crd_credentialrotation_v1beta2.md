
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
| spec |  | [CredentialRotationSpec](#credentialrotationspec) | false |
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

CredentialRotationSpec defines the desired state of CredentialRotation.\n\nThe CR is single-use: creating it starts one rotation cycle, and the target MySQLCluster is identified by the CR's own name and namespace (CredentialRotation name must equal MySQLCluster name). The spec is immutable except for the one-way Discard flip.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| discard | Discard requests the discard phase of the cycle: it removes the old (retained) passwords from MySQL and completes the rotation. It must be false at create time and may only be set to true while the verification window is open (DiscardReady=True). It can never be set back to false. Discard is a bool rather than a stage enum on purpose: the spec has exactly one legal transition, and a one-way flag cannot express an invalid request. | bool | false |

[Back to Custom Resources](#custom-resources)

#### CredentialRotationStatus

CredentialRotationStatus defines the observed state of CredentialRotation.

| Field | Description | Scheme | Required |
| ----- | ----------- | ------ | -------- |
| observedGeneration | ObservedGeneration reflects the .metadata.generation that the controller has most recently reconciled. | int64 | false |
| phase | Phase is the workflow position. Empty until the first reconcile. The value set is open: clients must tolerate unknown values. | RotationPhase | false |
| message | Message is a human-readable detail for the current phase. On Failed it explains what went wrong and names the matching recovery procedure; on Blocked or a pause it names the obstacle; on Succeeded it shows the scheduled TTL deletion time. | string | false |
| rotationID | RotationID is the UUID for this cycle. Set when the pending passwords are seeded. The value, if non-empty, is a canonical 36-character UUID. | string | false |
| completionTime | CompletionTime is set when the phase turns terminal (Succeeded or Failed). The TTL deadline for the automatic deletion of a Succeeded CR is computed from it. | *[metav1.Time](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Time) | false |
| conditions | Conditions represent the latest available observations. Three conditions are exposed: DiscardReady (the verification-window gate), DualPassword (MySQL's physical state), and Finished (the machine-readable terminal signal). The workflow position lives in Phase, not in the conditions. | [][metav1.Condition](https://pkg.go.dev/k8s.io/apimachinery/pkg/apis/meta/v1#Condition) | false |

[Back to Custom Resources](#custom-resources)
