#!/bin/bash

NS=$1
NAME=$2
INDEX=$3
CONTAINER=$4

BINDIR=$(dirname "$0")/../bin
KUBECTL=$BINDIR/kubectl

function log {
  echo "$(date '+%Y-%m-%d %H:%M:%S') $@"
}

UNIQUE_NAME=$($KUBECTL -n $NS get mysqlcluster $NAME -o jsonpath='{.metadata.name}{"-"}{.metadata.uid}' 2> /dev/null)
if [ -z "$UNIQUE_NAME" ]; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') MySQLCluster is not found"
  exit
fi

STS_NAME=$(./e2e/bin/kubectl -n e2e-test-external get sts -lapp.kubernetes.io/name=moco-mysql -o jsonpath='{.items[0].metadata.name}')
$KUBECTL -n $NS logs $UNIQUE_NAME-$INDEX -c $CONTAINER --tail=-1
