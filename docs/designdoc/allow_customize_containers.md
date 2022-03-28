# Allow users to customize containers

## Context

Currently, MOCO has containers that are automatically added by the system in addition to containers added by the user.
(e.g. `agent`, `moco-init` etc...)
Containers automatically added by the system do not allow user customization.

There are cases where users want to customize container resources, as in Issue [cybozu-go/moco#235](https://github.com/cybozu-go/moco/issues/235).
This design document examines how users can customize containers that are automatically added by the system.

## Goals

* No breaking changes
* Allows users to customize containers that are automatically added by the system
* Extendable functionality for future customization beyond container resources
* Users can only customize authorized fields
  * Do not allow customization of fields that would make operation untenable (e.g. `command`)

## Non-goals

* No customization provided at the time of implementation other than requested container resources
* No enhancement will be added to the `moco.cybozu.com/v1beta1` API.

## ActualDesign

Add `.spec.podTemplate.overwriteContainers` field to MySQLCluster.

```yaml
spec:
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: quay.io/cybozu/mysql:8.0.28
    overwriteContainers:
    - name: agent
      resources:
        requests:
          cpu: 50m
```

Pseudo-code:

```go
type PodTemplateSpec struct {
	ObjectMeta
	Spec                PodSpecApplyConfiguration
	// +optional
	OverwriteContainers []OverwriteContainer
}

// +kubebuilder:validation:Enum=agent;moco-init;slow-log;mysqld-exporter
type OverwriteableContainerName string

const (
	AgentContainerName             OverwriteableContainerName = constants.AgentContainerName
	InitContainerName              OverwriteableContainerName = constants.InitContainerName
	SlowQueryLogAgentContainerName OverwriteableContainerName = constants.SlowQueryLogAgentContainerName
	ExporterContainerName          OverwriteableContainerName = constants.ExporterContainerName
)

type OverwriteContainer struct {
	// +kubebuilder:validation:Required
	Name OverwriteableContainerName
	// +optional
	Resources *corev1ac.ResourceRequirementsApplyConfiguration
}
```

`overwriteContainers` is a container definition with only name and customizable fields.
moco-controller refers to `overwriteContainers` when creating containers and overwrites fields if the container name matches.
The Name field is required and validated by Enum.
`overwriteContainers` does not distinguish between initContainer and container.

No merge logic is provided for container resources to avoid implicit value setting situations.

Pros:

* Expandable
* Users can learn about customizable fields from MySQLCluster specs

Cons:

* If you want different customizable fields for different containers, you cannot express

## AlternativesConsidered

I considered defining fields for each container and customizing them,
but decided not to do so because of the complexity and the impact of future disruptive changes due to the tight coupling of the API and containers.

```yaml
spec:
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: quay.io/cybozu/mysql:8.0.28
    overwriteContainers:
      agent:
        resources:
          requests:
            cpu: 50m
      moco-init:
        resources:
          requests:
            cpu: 100m
      slow-log:
        resources:
          requests:
            cpu: 100m
      mysqld-exporter:
        resources:
          requests:
            cpu: 100m
```
