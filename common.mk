# Kubernetes version for testing
# This variable is used for getting kube-apiserver (unit tests) and kindest/node (e2e test).
# Please specify a version that can be used with both (especially kindest/node).
# ref: https://hub.docker.com/r/kindest/node/tags
KUBERNETES_VERSION=1.19.4

# MySQL version for testing
MYSQL_VERSION = 8.0.20
