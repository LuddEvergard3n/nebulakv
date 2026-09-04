# Authentication, memory, compaction and replication

These four extensions were implemented after the first core milestone. They remain
standard-library Go modules. The tested commands and failure modes are listed below.

## Authentication

Create a local UTF-8 file containing one nonempty password line. The repository
ignores `secrets/`; do not commit real credentials. A final LF or CRLF is accepted.

```sh
nebulakv --password-file ./secrets/password.txt --appendonly --data ./data
redis-cli -p 6380 --askpass PING
```

The password is loaded at startup. Rotate it by changing the file and restarting.
AUTH supports `AUTH password` and `AUTH default password`. Access is per connection;
authentication on one connection never authorizes another. A wrong or malformed
AUTH revokes prior authentication, and five consecutive failures close that connection.
Failure limiting is per connection, not a global brute-force defense. Without a
password file, authentication remains disabled for the original local workflow.

This does not provide multiple users, ACLs, hashed password files or TLS. Passwords
travel over TCP; use trusted local/private transport. File permission and secret
distribution remain responsibilities of the deployment environment.

## Dataset budget and process ceiling

```sh
nebulakv --maxmemory 67108864
```

`maxmemory` is a positive byte count. The default is 64 MiB, accounting for key bytes,
value bytes and a 96-byte per-entry allowance. Empty keys/values therefore still have
a cost. INFO exposes `accounted_memory` and `maxmemory` separately from raw key/value bytes.

Before a write is journaled, its final net cost is calculated. Multi-key admission
is atomic and handles repeated keys by their final values. If pressure remains after
expired entries are reclaimed, the complete command returns OOM without changing
live data or appending a journal record. No eviction policy silently removes live
values. Deletes, shrinking replacements and expiration release accounted bytes.

Replay enforces the same budget; a journal whose intermediate replay state exceeds
the configured limit fails startup. To migrate an old large history to a smaller
budget, start it with a sufficient budget, compact, stop and restart with the smaller
budget. Do not lower the budget below the live dataset.

Accounting is not exact RSS. Map buckets, Go runtime memory, network buffers and
replication staging are separate. A receiving replica temporarily has live plus
staged maps; a source retains at most one exported snapshot. For an actual container
ceiling, use:

```sh
docker run --rm --memory 256m --memory-swap 256m \
  -p 127.0.0.1:6380:6380 nebulakv:local
```

The Docker ceiling is kernel-enforced and can terminate the process if exceeded.
It is not graceful eviction. The end-to-end test configured this ceiling and verified
Docker reported 268,435,456 bytes; it did not deliberately exhaust/kill the container
through memory pressure. Dataset over-budget rejection was exercised directly.

## Compaction

```sh
redis-cli -p 6380 REWRITEAOF
nebulakv --appendonly --data ./data --aof-rewrite-size 67108864
```

REWRITEAOF is a synchronous NebulaKV extension. It requires append-only persistence.
The store lock protects a consistent view while current live entries are written to
a candidate journal, synchronized and installed. Deleted/overwritten history is
discarded. Remaining expiry deadlines are preserved, as are binary keys and values.

A separate locked sidecar retains ownership across file replacement. Rename failure
reopens the old journal and permits further appends; a failure that prevents reopening
marks persistence unavailable. Tests inject replacement failure and exercise writes
concurrent with rewrite. Temporary files from a normal attempt are removed; a process
crash can leave a candidate that is ignored on startup and can be inspected manually.

Automatic rewrite is checked once per second when the threshold is reached and the
journal has doubled since its last rewrite. Default: 64 MiB. `--aof-rewrite-size 0`
disables automatic triggering. INFO exposes `aof_bytes` and `aof_rewrites`.
This limits history growth under ordinary operation; it is not a hard disk-size quota.
Compaction pauses storage operations and requires temporary free disk space.

## Primary and replica

Start the primary:

```sh
nebulakv --port 6380 --appendonly --data ./primary-data \
  --password-file ./secrets/password.txt
```

Start another process with a separate data directory:

```sh
nebulakv --port 6381 --appendonly --data ./replica-data \
  --password-file ./secrets/replica-password.txt \
  --replicaof 127.0.0.1:6380 --primary-password-file ./secrets/password.txt \
  --replica-interval 1s --replica-timeout 30s
```

The replica's incoming password and its primary password can be different. It checks
the primary's epoch/revision, downloads only after changes, validates a complete
bounded snapshot and installs it atomically. With persistence enabled, installation
replaces the replica journal before swapping its visible map. Unchanged polls do not
rewrite the replica disk. More than 1,024 keys work because entries are streamed as
individual bounded frames, not one unbounded RESP array.

Writes return READONLY on replicas. Data reads return LOADING until initial sync.
During a later primary outage, reads return the last completed snapshot. INFO shows
`role`, `primary_link_status`, `replica_syncs`, `replica_last_success_seconds` and
`replica_last_error`. Automatic retry handles primary restart and fresh epochs.

Limits are deliberate: full snapshots after changes, asynchronous/stale reads,
single-hop primary/replica topology, no Redis PSYNC, no automatic election or promotion,
and no synchronous durability acknowledgment from replicas. A successful primary
write does not prove any replica has received it. Snapshots copy map metadata and
retain immutable strings; large, frequently changed datasets need incremental replication.

## Reproduce the verified extension checks

```powershell
.\scripts\verify.ps1
docker build -t nebulakv:local .
.\scripts\resilience-smoke.ps1
```

The smoke script uses two temporary containers, an internal network, a test-only
password and the official redis-cli image. It asserts authentication, all-or-nothing
OOM rejection, reduced AOF size, read-only replication, expiry, primary abrupt
restart, automatic resynchronization and replica restart. It removes only the resources
it created. Unit/integration tests additionally cover malformed/interrupted snapshots,
wrong primary credentials, memory budget mismatch and journal replacement failure.

See [resilience-run.txt](resilience-run.txt) for the captured run. The test AOF shrank
from 8,995 to 127 bytes for its small overwrite-heavy fixture; that ratio is not a
general compression guarantee.
