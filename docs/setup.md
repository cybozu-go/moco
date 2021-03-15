Setup
=====

## Prerequisites

* Installation components for this operator is in [config](../config) directory.

* The operator manifests are managed with [kustomize](https://kustomize.io/), so it is necessary to install it first.

## Setup procesure

### Quick setup

You can point at [config](../config) to build kustomized components, like so

```shell
# Assumption: you're in the project root
$ cd config
$ kubectl apply -k .
or
$ kustomize build | kubectl apply -f - 
```

### Setup components in order

If you want to see what to be installed, you can also install components in order.

```shell
# Assumption: you're in the project root
$ cd config
# Create namespace
$ kubectl apply -f namespace.yaml
# Create CRD
$ kubectl apply -k crd
# Create RBAC
$ kubectl apply -k rbac
# Create manager deployment
$ kubectl apply -k manager
```

### Next step

If you want to create a MySQL Cluster, read [Example of MySQLCluster Custom Resource](./example_mysql_cluster.md) and [MySQLCluster](./crd_mysql_cluster.md).
