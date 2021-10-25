# Migrate from kustomize to Helm

This document describes the steps to migrate from kustomize to Helm.

## Install Helm chart

There is no significant difference between the manifests installed by kusomize and those installed by Helm.

If a resource with the same name already exists in the Cluster, Helm will not be able to create the resource.

```console
$ helm repo add moco https://cybozu-go.github.io/moco/
$ helm repo update
$ helm install --namespace moco-system moco moco/moco
Error: rendered manifests contain a resource that already exists. Unable to continue with install: ServiceAccount "moco-controller-manager" in namespace "moco-system" exists and cannot be imported into the current release: invalid ownership metadata; label validation error: missing key "app.kubernetes.io/managed-by": must be set to "Helm"; annotation validation error: missing key "meta.helm.sh/release-name": must be set to "moco"; annotation validation error: missing key "meta.helm.sh/release-namespace": must be set to "moco-system"
```

Before installing Helm chart, you need to manually delete the resources. You do not need to delete Namespace, CRD and BackupPolicy/MySQLCluster custom resources at this time.

```console
$ helm template --namespace moco-system moco moco/moco | kubectl delete -f -
serviceaccount "moco-controller-manager" deleted
clusterrole.rbac.authorization.k8s.io "moco-backuppolicy-editor-role" deleted
clusterrole.rbac.authorization.k8s.io "moco-backuppolicy-viewer-role" deleted
clusterrole.rbac.authorization.k8s.io "moco-manager-role" deleted
clusterrole.rbac.authorization.k8s.io "moco-mysqlcluster-editor-role" deleted
clusterrole.rbac.authorization.k8s.io "moco-mysqlcluster-viewer-role" deleted
clusterrolebinding.rbac.authorization.k8s.io "moco-manager-rolebinding" deleted
role.rbac.authorization.k8s.io "moco-leader-election-role" deleted
rolebinding.rbac.authorization.k8s.io "moco-leader-election-rolebinding" deleted
service "moco-webhook-service" deleted
deployment.apps "moco-controller" deleted
certificate.cert-manager.io "moco-controller-grpc" deleted
certificate.cert-manager.io "moco-grpc-ca" deleted
certificate.cert-manager.io "moco-serving-cert" deleted
issuer.cert-manager.io "moco-grpc-issuer" deleted
issuer.cert-manager.io "moco-selfsigned-issuer" deleted
mutatingwebhookconfiguration.admissionregistration.k8s.io "moco-mutating-webhook-configuration" deleted
validatingwebhookconfiguration.admissionregistration.k8s.io "moco-validating-webhook-configuration" deleted
```

Then install Helm chart again.

```console
$ helm install --namespace moco-system moco moco/moco
NAME: moco
LAST DEPLOYED: Wed Oct 13 11:28:54 2021
NAMESPACE: moco-system
STATUS: deployed
REVISION: 1
TEST SUITE: None
```
