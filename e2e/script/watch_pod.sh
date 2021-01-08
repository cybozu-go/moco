#!/bin/bash

NS=$1

BINDIR=$(dirname "$0")/../bin
KUBECTL=$BINDIR/kubectl

while true; do
  $KUBECTL get pod -n $NS -w 2> /dev/null | while read line ; do
    echo "$(date '+%Y-%m-%d %H:%M:%S') ${line}"
  done
  sleep 5
done
