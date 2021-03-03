Example of MySQLCluster Custom Resource
=======================================

- [Example](#example)
- [.metadata.namespace](#metadatanamespace)
- [.spec.replica](#specreplica)
- [.spec.serviceTemplate](#specservicetemplate)
- [.spec.podTemplate.spec.containers](#specpodtemplatespeccontainers)
  - [`mysqld` container](#mysqld-container)
  - [logging containers](#logging-containers)
- [.spec.podTemplate.spec.volumes](#specpodtemplatespecvolumes)
- [.spec.dataVolumeClaimTemplateSpec](#specdatavolumeclaimtemplatespec)
- [.spec.mysqlConfigMapName](#specmysqlconfigmapname)

MOCO provides wide configurability for the MySQL cluster via [MySQLCluster](crd_mysql_cluster.md) CRD.
Especially `.spec.podTemplate` allows users to write almost any type of Pod configuration.
This configurability might, however, confuse the users about how to write their MySQLCluster CRs.
This document shows and explains an example of a MySQLCluster CR.

The strict spec of MySQLCluster CRD is [given separately](crd_mysql_cluster.md).

Example
-------

This is an example of MySQLCluster CR and its auxiliary resources.

```yaml
apiVersion: moco.cybozu.com/v1alpha1
kind: MySQLCluster
metadata:
  name: my-cluster
  namespace: sandbox
spec:
  replicas: 3
  serviceTemplate:
    metadata:
      annotations:
        metallb.universe.tf/address-pool: inter-site-network
    spec:
      type: LoadBalancer
  podTemplate:
    spec:
      containers:
      - name: mysqld
        image: quay.io/cybozu/moco-mysql:8.0.20.6
        resources:
          requests:
            memory: "512Mi"
        livenessProbe:
          exec:
            command: ["/moco-bin/moco-agent", "ping"]
          initialDelaySeconds: 5
          periodSeconds: 5
        readinessProbe:
          httpGet:
            path: /health
            port: 9080
          initialDelaySeconds: 10
          periodSeconds: 5
      - name: err-log
        image: fluent/fluent-bit:1.7.2
        volumeMounts:
        - name: err-fluent-bit-config
          mountPath: /fluent-bit/etc/fluent-bit.conf
          readOnly: true
          subPath: fluent-bit.conf
        - name: var-log
          mountPath: /var/log/mysql
          readOnly: true
      - name: slow-log
        image: fluent/fluent-bit:1.7.2
        volumeMounts:
        - name: slow-fluent-bit-config
          mountPath: /fluent-bit/etc/fluent-bit.conf
          readOnly: true
          subPath: fluent-bit.conf
        - name: var-log
          mountPath: /var/log/mysql
          readOnly: true
      securityContext:
        runAsUser: 10000
        runAsGroup: 10000
        fsGroup: 10000
      volumes:
      - name: err-fluent-bit-config
        configMap:
          name: err-fluent-bit-config
      - name: slow-fluent-bit-config
        configMap:
          name: slow-fluent-bit-config
  dataVolumeClaimTemplateSpec:
    storageClassName: topolvm-provisioner
    accessModes: [ "ReadWriteOnce" ]
    resources:
      requests:
        storage: 3Gi
  mysqlConfigMapName: my-cluster-mycnf
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-cluster-mycnf
  namespace: sandbox
data:
  max_connections: "5000"
  max_connect_errors: "10"
  max_allowed_packet: 1G
  max_heap_table_size: 64M
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: err-fluent-bit-config
  namespace: sandbox
data:
  fluent-bit.conf: |-
    [INPUT]
      Name           tail
      Path           /var/log/mysql/mysql.err
      Read_from_Head true
    [OUTPUT]
      Name     file
      Match    *
      Path     /dev
      File     stdout
      Format   template
      Template {log}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: slow-fluent-bit-config
  namespace: sandbox
data:
  fluent-bit.conf: |-
    [INPUT]
      Name           tail
      Path           /var/log/mysql/mysql.slow
      Read_from_Head true
    [OUTPUT]
      Name     file
      Match    *
      Path     /dev
      File     stdout
      Format   template
      Template {log}
```

.metadata.namespace
-------------------

The Kubernetes resources derived from a MySQLCluster CR are created in the same namespace with the CR, as usual.

.spec.replica
-------------

Currently, only 3 is supported.

.spec.serviceTemplate
---------------------

You can provide a template for the Services.
This example specifies that the IP addresses for LoadBalancer-type Services should be assigned from the `inter-site-network` pool of [MetalLB](https://metallb.universe.tf/) to make the created MySQL cluster accessible from other sites.

.spec.podTemplate.spec.containers
---------------------------------

You need to specify at least 1 container named `mysqld` here.
The example specifies additional 2 containers to extract logs from MySQL log files.

You cannot use `agent` as the name of an additional container.

### `mysqld` container

The container named `mysqld` is used to run MySQL servers.
You can [build the container image](build-mysql.md) by yourself or use one from [the list of pre-build images](https://quay.io/repository/cybozu/moco-mysql?tag=latest&tab=tags).

It is a good practice to specify the resource requests.

There are 2 probes in the example: the liveness probe and the readiness probe.
The liveness probe uses `/moco-bin/moco-agent ping` in the `mysqld` container to check whether the MySQL server is running or not.
The readiness probe uses the `/health` endpoint to check the status of the MySQL server.
This endpoint is handled by `/moco-bin/moco-agent server` in the sidecar container `agent`, which is added by MOCO.

The executable file `/moco-bin/moco-agent` is inserted into each container by an init container.
So you need not prepare the moco-agent binary.

### logging containers

There are 2 additional containers in the example.
They extract logs from MySQL log files into their `stdout` streams.
The logs are then handled by Kubernetes.
You can see the logs by `kubectl logs -c err-log <pod_name>` and `kubectl logs -c slow-log <pod_name>`.

The logging containers use [Fluent Bit](https://fluentbit.io/) to tail the rotated log files without loss.
This command is not included in the MySQL container image.
You can use other log-shipping tools for exporting logs to `stdout` and/or the external log database.

The logging containers mount several volumes including `var-log`.
`var-log` is not listed explicitly in `volumes` because they are managed by MOCO.

The ConfigMaps used to give the configuration of Filebeat have general names, `err-fluent-bit-config` and `slow-fluent-bit-config`, because they can be shared among multiple MySQLClusters.

.spec.podTemplate.spec.volumes
------------------------------

You can define volumes in the Pod template.
The example defines 2 `configMap` volumes and 2 `emptyDir` volumes, all for the logging containers described above.

Some volume names are reserved by MOCO to define the system volumes.
The reserved names include `mysql-data`, `mysql-conf`, `var-run`, `var-log`, `tmp`, `mysql-conf-template`, `replication-source-secret`, and `my-cnf-secret`.

.spec.dataVolumeClaimTemplateSpec
---------------------------------

You need to specify a `PersistentVolumeClaim` template to store the MySQL data persistently.
The example uses [TopoLVM](https://github.com/topolvm/topolvm), which exposes node-local storage to Kubernetes.
You can use any StorageClass as you like.

.spec.mysqlConfigMapName
------------------------

You can specify a ConfigMap to configure the MySQL server.
The key-value pairs in `.data` of the pointed ConfigMap are treated as if they were given in the `[mysqld]` group of the option file.

Note that all values must be string types in the ConfigMap YAML.

The administrator of the MOCO controller can set the default values and the unchangeable values for MySQL server options.
See the [manual of `moco-controller`](moco-controller.md) for details.
