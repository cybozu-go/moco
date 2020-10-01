#!/bin/bash

cat 00_create_table.sql | ../mysql.sh 2> /dev/null

for i in {0..2}
do
    cat 01_display_table.sql | ../mysql.sh ${i}
done

CLUSTER_UID=$(kubectl -n e2e-test get mysqlcluster mysqlcluster -o json | jq -r .metadata.uid)

INSTANCE=0
kubectl delete pvc -n e2e-test mysql-data-mysqlcluster-${CLUSTER_UID}-${INSTANCE} &
kubectl patch pvc -n e2e-test mysql-data-mysqlcluster-${CLUSTER_UID}-${INSTANCE} -p '{"metadata": {"finalizers" : null}}'

kubectl get pvc -n e2e-test

kubectl delete pod -n e2e-test mysqlcluster-${CLUSTER_UID}-${INSTANCE}

kubectl get pod -n e2e-test

kubectl logs -n e2e-test mysqlcluster-${CLUSTER_UID}-${INSTANCE} -c agent

cat 01_display_table.sql | ../mysql.sh $INSTANCE 2> /dev/null
