# Architecture

## Boundaries

The executable wires configuration, storage, journal and server. Storage knows
only the `Journal.Append(Mutation)` interface. Persistence imports the mutation
schema and calls `Store.Replay` before clients are accepted. Command dispatch
translates validated arguments into storage calls; it does not own locks or disk
files. RESP has no knowledge of the database. No package imports a third-party library.

## RESP and TCP lifecycle

RESP2 prefixes identify simple strings, errors, integers, bulk strings and arrays.
Bulk payloads are length-delimited and may include NUL, CRLF or invalid UTF-8. Null
bulk strings represent missing values. Commands must be nonempty arrays of non-null
bulk strings. Nested arrays are decoded within a depth/value budget but rejected
as command arguments.

The decoder owns a buffered reader for the lifetime of each TCP connection. This
preserves bytes from later commands. Header lines are bounded by the reader size;
declared payload lengths are checked before allocation, and `io.ReadFull` handles
fragmented payloads. Aggregate bytes and value counts bound nested input. Protocol
errors close the connection after a best-effort error response because safely
resynchronizing a malformed byte stream is not generally possible.

One goroutine handles each admitted client. The accept loop closes excess clients
immediately rather than blocking while writing a rejection. Command reads have an
absolute deadline covering idle and partial-input time. Writes have a separate
deadline. Each response is flushed; this prioritizes simple latency behavior over
maximal pipeline batching.

## Storage and locking

`map[string]Entry` stores immutable binary strings and an optional absolute Unix
millisecond deadline. A single mutex protects the map, expiration counter and
journal ordering. Each operation samples the clock once, so MGET/EXISTS use one
logical instant and MSET cannot be observed half-applied.

The original alternatives were RWMutex, sharding and an event loop. RWMutex would
not benefit reads that remove expired keys. Sharding needs ordered multi-lock
transactions for multi-key commands and coordination with a globally ordered AOF.
An event loop still serializes operations and needs request queues. A mutex keeps
this implementation small at the cost of contention and read stalls during fsync.

GET returns an immutable string without copying data. Memory-only SET writes
directly to the map, avoiding construction of an unused journal record. Persistent
SET still follows the same append-before-apply ordering. No unsafe string conversion
is used for this optimization.

## Expiration

SET EX/PX and EXPIRE/PEXPIRE convert durations to absolute deadlines with checked
arithmetic. SET clears previous TTL unless a new TTL is supplied. Increment keeps
the previous TTL. Lazy lookup deletes an entry when `expires_at <= now`. A periodic
worker inspects up to 128 entries every 100 ms. Full diagnostic scans also expire
entries. Expiration deletes are not journaled: the original absolute deadline is
sufficient to keep an expired entry invisible after replay.

Deadlines use wall time so they survive process restarts. Wall-clock adjustments
can therefore change observed TTL. Cleanup scheduling is best-effort; it has no
strict bound across a very large map. Tests inject a deterministic clock to inspect
expiration exactly at boundaries. TTL rounds milliseconds, PTTL retains them.

## Persistence and replay

```text
file:   "NKV1\n" | record | record | ...
record: uint32 payload-length (big endian)
        uint32 CRC32-IEEE (big endian)
        JSON mutation payload
```

Mutation records contain either a clear operation or a list of final-value writes
and deletes. Byte slices encode as base64, preserving binary keys and values.
MSET occupies one record, so a truncated last record cannot replay half a command.
Conditional commands record their resolved effect, not their original condition.

With the storage mutex held, a write appends a complete framed record and calls
`File.Sync`, then updates memory. The client is acknowledged only after both.
A write or synchronization error marks the journal failed; subsequent writes are
rejected, reads remain available, and INFO reports the error. Memory is not updated
on a failed append. The record might nevertheless have reached disk: a client
error or disconnect is not proof that the mutation will be absent after recovery.

Open acquires an exclusive OS lock on the journal, checks its header and replays
validated records in order. Partial final record headers or payloads are truncated
to the last complete offset and synchronized before appending again. Bad lengths,
checksums, unknown JSON fields, malformed records and invalid file headers fail
startup. Complete corruption is never silently discarded. CRC32 detects accidental
damage, not malicious tampering. Journal record allocations are capped at 24 MiB.

Locks use `flock` on supported Unix systems and `LockFileEx` on Windows. A stable
`appendonly.lock` handle owns the directory while the data file is replaced; both
handles are locked during ordinary operation. The sidecar may remain on disk after
exit, but ownership is the OS lock, not file existence. Closing the handle or process
termination releases it. Network filesystem semantics remain outside the tested model.

REWRITEAOF compacts live entries into a same-directory temporary file. The store
lock keeps the source stable, and only one bounded record is encoded at a time.
The candidate is synced and closed, the original handle is closed, and the candidate
replaces the data file while the sidecar lease remains held. The journal is reopened
and relocked before appends resume. A rename failure reopens the unchanged old file;
unrecoverable reopen/directory-sync errors make persistence fail closed. Unix builds
sync the parent directory; Windows has no portable directory-sync operation here.
Power-loss durability is not claimed on either platform.

Automatic rewrite checks once per second when configured bytes exceed the threshold
and the journal has at least doubled since its last rewrite. This avoids repeatedly
rewriting a still-large live dataset. It is a compaction trigger, not a hard disk quota.
Crash-left candidate files are never replayed automatically; inspect them before
manual cleanup. Synchronous compaction can pause requests, so use modest datasets.

## Statistics

Connection and command totals use atomic counters. The active connection map has
its own mutex. Store statistics count live keys, expired keys and key/value bytes;
the latter excludes map buckets, strings/entry headers, goroutine stacks, socket
buffers, temporary records and runtime overhead. INFO's fields are snapshots taken
at nearby instants, not a global transactional monitoring snapshot.

## Shutdown

SIGINT/SIGTERM cancels the service context. The listener closes, the expiry worker
stops, read deadlines wake blocked clients and active handlers drain. Connections
still present after the drain deadline are forcibly closed. Serve waits for handlers
before returning, then the executable closes the journal. A storage operation
blocked inside an OS disk call is not cancellable by closing a TCP connection;
the drain timeout is a connection bound, not a hard process exit guarantee.

## Main costs and future decisions

Typical map operations are expected O(1); multi-key work scales with argument
count. KEYS is O(total key bytes × pattern tokens) plus sorting and locks the map.
INFO/DBSIZE scan every key. Persistent operations encode, append and fsync under
the same lock, emphasizing simple ordering over throughput. Admission checks enforce
key/value bytes plus a 96-byte allowance per entry, defaulting to 64 MiB. All final
effects of a multi-key command are checked before journal append or mutation. Expired
entries are reclaimed on pressure; otherwise the whole write returns OOM. This is a
dataset budget, not exact heap/RSS accounting. Use OS/container limits for the latter.

Before adding sharding, measure realistic TCP and persistence workloads, define
atomicity across shards, and choose how to serialize durable commits.

## Authentication and replication

AUTH is checked in the connection handler before command dispatch and synchronization.
Password files are bounded and loaded once on startup; SHA-256 digests are compared
in constant time. A failed AUTH revokes that connection's access, and five consecutive
failures close it. Only a single default user exists. This is not a password-storage
KDF or TLS; the configured secret and TCP transport require local/private protection.

A primary assigns a random epoch at process start and increments its revision with
every applied logical mutation. `NKV.SYNC token` is an internal RESP-framed operation.
An unchanged token returns UNCHANGED. Otherwise a consistent shallow copy of the map
is streamed as a header, one bounded entry frame per key and an END marker. The
primary admits one snapshot transfer at a time to bound retained snapshots.

The replica authenticates, checks count/bytes/types/unique keys/deadlines and stages
the entire snapshot within its dataset budget. Only the END marker and exact byte
accounting allow installation. With persistence enabled, journal replacement happens
before the visible map swap. The old map remains visible on malformed, interrupted
or oversized transfers. A new primary epoch forces a full resynchronization.

Replicas reject dataset writes. Before initial synchronization, data commands return
LOADING; after a later disconnection, they serve the last complete snapshot and INFO
reports link status, last error and age. Deadlines remain absolute. The configurable
poll interval defaults to one second. This uses O(dataset size) transfer after changes,
with live plus staged maps during replacement; it is not incremental replication,
linearizable reads, a consensus protocol or automatic failover. Replication workers
are cancelled and joined before the journal closes.
