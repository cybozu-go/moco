Design notes
============

Motivation
----------

Automate operations of binlog-based replication on MySQL.

Reason for why we choose semi-sync replication and don't use InnoDB cluster is InnoDB cluster does not allow large (>2GB) transactions.

These softwares provide operator functionality over MySQL, but they are designed on different motivation.
- https://github.com/oracle/mysql-operator
- https://github.com/presslabs/mysql-operator
- Percona?

Goals
-----
- Use Custom Resource Definition to automate construction of MySQL database using replication.
- Provide functionality for stabilizing distributed systems that MySQL replication does not provide.
- While keeping consistency, automating configuration of cluster as much as possible including fail-over situation.

Components
----------

- operator: Automate MySQL management using MySQL CR and User CR.
- Backup job: Upload logical full-backup and binary logs onto object storage.
- Command-line tools: Utility tools to manipulate MySQL from external location (e.g. change master manually).

binlog, dump をとる pod はオペレータと同一ではない
ns に属した pod になるため

CR にどういう state を持たせるか。マニュアル操作するとずれるのでは？

master 選出はオペレータでやる
コマンドラインツールはそれを実行するだけ

オペレータにAPIを持たせるのは微妙。CR 書き換えにしたい
master をオペレータで選んで CR に master spec を記録する？

または MySQL のポッドをわざと落とす。

### External components

- Object storage: store logical backup and binary logs
- cert-manager: automate to provide client certification and master-slave certification

Architecture
------------
MySQL instance via statefulset

Packaging and deployment
------------------------
