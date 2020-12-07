#!/bin/sh
if [ -f /var/lib/mysql/init-once-completed ]; then
  mysqladmin --defaults-extra-file=/var/lib/mysql/misc.cnf ping
else
  exit 1
fi
