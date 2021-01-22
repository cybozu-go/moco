#!/bin/bash

NS=$1
NAME=$2
VARIABLES=$3

BINDIR=$(dirname "$0")/../bin
KUBECTL=$BINDIR/kubectl
KUBECTLMOCO=$BINDIR/kubectl-moco

UNIQUE_NAME=moco-$NAME

function log {
  echo "$(date '+%Y-%m-%d %H:%M:%S') $@"
}

function loop {
  # check cluster status
  BUF=$($KUBECTL -n $NS get mysqlcluster $NAME -o jsonpath='{.spec.replicas}{" "}{.status.ready}' 2> /dev/null)
  if [ -z "$BUF" ]; then
    log "MySQLCluster is not found"
    return
  fi
  array=($BUF)
  REPLICAS=${array[0]}
  STATUS=${array[1]}

  log "$UNIQUE_NAME status=$STATUS replicas=$REPLICAS"

  # check each instances
  for index in $(seq 0 $(($REPLICAS - 1))); do
    # confirm pod is running
    PHASE=$($KUBECTL -n $NS get pod $UNIQUE_NAME-$index -o jsonpath='{.status.phase}' 2> /dev/null)
    if [ "$PHASE" != "Running" ]; then
      log "instance[$index] pod is not Running: $PHASE"
      continue
    fi

    # confirm mysqld is ready
    query="SHOW VARIABLES WHERE variable_name = 'version';"
    VERSION=$(echo $query | $KUBECTLMOCO -n $NS mysql -i --index $index $NAME 2> /dev/null | grep -w version | awk '{ print $2}')
    if [ -z "$VERSION" ]; then
      log "instance[$index] mysqld is not ready"
      continue
    fi
    log "instance[$index] mysqld is ready: $VERSION"

    # get system variables
    vars=(${VARIABLES//,/ })
    for v in "${vars[@]}"; do
      query="SHOW VARIABLES WHERE variable_name = '$v';"
      VALUE=$(echo $query | $KUBECTLMOCO -n $NS mysql -i --index $index $NAME 2> /dev/null | grep -w $v | awk '{ print $2}')
      if [ -n "$VALUE" ]; then
          log "instance[$index] $v = $VALUE"
      fi
    done
  done
}

while true; do
  loop
  sleep 5
done
