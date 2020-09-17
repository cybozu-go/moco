#!/bin/bash

cat 00_create_table.sql | ../mysql.sh 2> /dev/null

for i in {0..2}
do 
    cat 01_display_table.sql | ../mysql.sh ${i} 
done

cat 02_flush.sql | ../mysql.sh 2> /dev/null

CLUSTER_UID=$(kubectl -n e2e-test get mysqlcluster mysqlcluster -o json | jq -r .metadata.uid)

INSTANCE=2
kubectl delete pvc -n e2e-test mysql-data-mysqlcluster-${CLUSTER_UID}-${INSTANCE} &
kubectl patch pvc -n e2e-test mysql-data-mysqlcluster-${CLUSTER_UID}-${INSTANCE} -p '{"metadata": {"finalizers" : null}}'

kubectl get pvc -n e2e-test mysql-data-mysqlcluster-${CLUSTER_UID}-${INSTANCE}

kubectl delete pod -n e2e-test mysqlcluster-${CLUSTER_UID}-${INSTANCE}

kubectl get pod -n e2e-test

kubectl logs pod -n e2e-test mysqlcluster-${CLUSTER_UID}-${INSTANCE} -c agent

for i in {0..2}
do 
    cat 01_display_table.sql | ../mysql.sh ${i}> /dev/null
done
