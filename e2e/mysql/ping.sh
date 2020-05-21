#!/bin/sh
if [ -f /var/lib/mysql/init-once-completed ]; then
  mysqladmin --defaults-extra-file=/tmp/ping.cnf ping
else
  exit 1
fi
