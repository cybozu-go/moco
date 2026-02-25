---
name: Clustering Consistency Invariants
description: Critical consistency and safety rules for MOCO clustering logic. These rules must be followed to prevent data loss and split-brain when modifying the clustering engine.
applyTo: clustering/*
---

# Cluster consistency rules for `MySQLCluster`

This note explains important safety rules in `clustering/`.
These rules protect data consistency between replicas and help avoid split-brain.
If you change clustering logic, keep all rules true.

## There is only one valid primary

- `status.currentPrimaryIndex` is the single source of truth for the writable primary.
- In normal mode, only that instance may be writable (`read_only=0`, `super_read_only=0`).
- Every non-primary instance must stay `super_read_only=1`.
- In intermediate-primary mode (`spec.replicationSourceSecretName != nil`), the primary is also read-only.

Why this matters: it prevents two writers and different data histories.

## Do not switch primary before GTID catch-up

For a planned switchover:

1. Set the old primary to read-only.
2. Kill existing client connections on the old primary.
3. Make sure the candidate executes the old primary `ExecutedGTID` (`WaitForGTID`).
4. Update `status.currentPrimaryIndex` only after that.

Why this matters: transactions accepted by the old primary are visible on the new primary after switchover.

## In failover, block the old primary path first

For an unplanned failover:

1. Stop `IO_THREAD` on all replicas first.
2. Choose the next primary only from non-errant replicas.
3. Wait until the new primary executes all its retrieved GTIDs.
4. Then update `status.currentPrimaryIndex`.

Why this matters: it reduces split-brain risk after failure or network partition.

## Errant replicas must be isolated

- A replica is errant when `replica.executed - primary.executed` contains GTIDs from UUIDs other than the primary UUID.
- Errant replicas are not counted as healthy/degraded members.
- Errant replicas are not used as failover candidates.
- Replication is stopped on errant replicas.
- Errant replicas must not keep role labels (`moco.cybozu.com/role`).
- When primary `ExecutedGTID` is unavailable (for example, when the primary is down or may have lost data), errant state saved in `status.errantReplicaList` is used.
- A replica can rejoin only after re-initialization clears errant state.

Why this matters: it prevents inconsistent data from returning silently.

## Replica checks are strict

A replica is counted as healthy only when all conditions are true:

- Pod is Ready.
- MySQL status is available.
- `super_read_only=1`.
- Replication points to the current primary host.
- Replica is not errant.

Also, if `SemiSyncMasterWaitSessions > 0`, treat the replica as unavailable (hangup protection).

Why this matters: Pod readiness alone is not enough for safe replication.

## Failover needs a majority of good replicas

- `Failed`: more than half of non-primary replicas are reachable, non-errant, have replication status, and have GTID data.
- `Lost`: this majority condition is not met.
- In normal mode, this is used together with loss-less semi-sync settings:
  - primary: `rpl_semi_sync_master_enabled=ON`, `rpl_semi_sync_master_wait_for_slave_count=floor(spec.replicas/2)`
  - replicas: `rpl_semi_sync_slave_enabled=ON`

Why this matters: this combination helps ensure the surviving replicas include at least one non-errant replica with the latest acknowledged transactions.

## Do not reconfigure a replica when required transactions are already purged on primary

Before `CHANGE REPLICATION SOURCE TO` equivalent, check:

- `primary.purged_gtid - replica.executed_gtid` must be empty.

If it is not empty, skip reconfiguration in this reconcile cycle and keep retrying later.

Why this matters: `CHANGE REPLICATION SOURCE TO` resets replica relay logs. If done too early, some transactions may exist in neither the replica relay log nor the primary binlog, and recovery may require re-initialization.
