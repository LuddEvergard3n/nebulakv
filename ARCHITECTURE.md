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

Locks use `flock` on supported Unix systems and `LockFileEx` on Windows; closing
the handle or process termination releases ownership. This avoids stale sentinel
lock files after crashes. Network filesystems and sharing a directory through
multiple containers/hosts are outside the tested durability model.

There is no compaction. Replay time and disk size grow with history. File fsync
alone is not a guarantee against device caches, filesystem errors or loss of the
directory entry during power failure. No power-cut testing has been performed.

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
the same lock, emphasizing simple ordering over throughput. No memory quota or
eviction means a trusted client can exhaust the process by inserting enough data.

Before adding sharding, measure realistic TCP and persistence workloads, define
atomicity across shards, and choose how to serialize durable commits. Before AOF
rewrite, specify snapshot consistency, atomic replacement, fsync/rename behavior,
and recovery on both Windows and Linux.
