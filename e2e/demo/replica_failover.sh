#!/bin/bash

cat 00_create_table.sql | ../mysql.sh 2> /dev/null

cat 01_flush.sql | ../mysql.sh 2> /dev/null

CLUSTER_UID=$(kubectl -n e2e-test get mysqlcluster mysqlcluster -o json | jq -r .metadata.uid)
INSTANCE=2
kubectl delete pvc -n e2e-test mysql-data-mysqlcluster-${CLUSTER_UID}-${INSTANCE} &
kubectl patch pvc -n e2e-test mysql-data-mysqlcluster-${CLUSTER_UID}-${INSTANCE} -p '{"metadata": {"finalizers" : null}}'

kubectl delete pod -n e2e-test mysqlcluster-${CLUSTER_UID}-${INSTANCE}