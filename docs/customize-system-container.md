# Customize default container

MOCO has containers that are automatically added by the system in addition to containers added by the user.
(e.g. `agent`, `moco-init` etc...)

The `MySQLCluster.spec.podTemplate.overwriteContainers` field can be used to overwrite such containers.
Currently, only container resources can be overwritten.
`overwriteContainers` is only available in MySQLCluster v1beta2.

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: default
  name: test
spec:
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: ghcr.io/cybozu-go/moco/mysql:8.0.30
    overwriteContainers:
    - name: agent
      resources:
        requests:
          cpu: 50m
```

## System containers

The following is a list of system containers used by MOCO.
Specifying container names in `overwriteContainers` that are not listed here will result in an error in API validation.

| Name            | Default CPU Requests/Limits | Default Memory Requests/Limits | Description                                                                                                                                             |
| --------------- | --------------------------- | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| agent           | `100m` / `100m`             | `100Mi` / `100Mi`              | MOCO's agent container running in sidecar. refs: https://github.com/cybozu-go/moco-agent                                                                |
| moco-init       | `100m` / `100m`             | `300Mi` / `300Mi`              | Initializes MySQL data directory and create a configuration snippet to give instance specific configuration values such as server_id and admin_address. |
| slow-log        | `100m` / `100m`             | `20Mi` / `20Mi`                | Sidecar container for outputting slow query logs.                                                                                                       |
| mysqld-exporter | `200m` / `200m`             | `100Mi` / `100Mi`              | MySQL server exporter sidecar container.                                                                                                                |
