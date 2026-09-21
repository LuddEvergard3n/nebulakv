# Implementation record

## Inspection

2026-09-04: the supplied workspace contains unrelated existing projects. There
was no NebulaKV directory, source code, dependency manifest or existing test suite
to run. This project is created separately; existing projects are preserved.
Go and redis-cli were absent from PATH. Docker CLI exists but its daemon was not
reachable. A portable official Go archive is used with SHA-256 verification.

## Incremental plan

1. RESP2 decoder/encoder: fragmented reads, binary values, bounds and malformed
   input tests. Verify before adding the database.
2. Atomic memory storage and command dispatch: string commands, conditional SET,
   deterministic expiry tests and concurrent increments.
3. TCP service: flags, connection limits, deadlines, shutdown, metrics and actual
   network tests.
4. Append-only persistence: write-before-apply, absolute deadlines, binary-safe
   records, restart and incomplete-tail tests.
5. Distribution and review: CI, Docker recipe, measured benchmarks, architecture,
   interview demo and explicit validation report.

## Results

1. RESP tests and vet passed before storage was introduced. Commit `a7f760c`.
2. Commands, deterministic expiration, concurrency and multi-key tests passed.
   Commit `4d45b07`.
3. TCP fragmentation, pipelining, limits, concurrency and shutdown tests passed.
   Commit `a078529`.
4. Journal replay, binary strings, truncated tails, corruption, file ownership and
   write failure tests passed. Commit `88d7351`.
5. Measured allocation cleanup and stronger active-expiration assertion passed.
   Commit `28d222c`.
6. Docker build/race tests and official redis-cli checks passed, including normal
   and abrupt restart. Documentation, CI configuration and review added.

Docker became available after the initial inspection. The first project's core
acceptance gates are recorded in [VALIDATION.md](VALIDATION.md). The first hosted CI
run passed after publication; the other seven projects remain separate work.

## v0.2 requested extensions

The user requested implementation and testing of the four previously listed limits.
The previous scope boundary was expanded explicitly.

1. AUTH/password-file and accounted dataset quota: `853e328`; Windows tests and vet passed.
2. Manual/automatic journal rewrite: `edf647d`; failure, concurrency and restart tests passed.
3. Authenticated read-only snapshot replication: `f3b33f2`; complete/partial/oversized
   transfer, epoch changes, startup gating and replica persistence tests passed.
4. Full Linux race suite and Docker build passed; official redis-cli demonstrated all
   four extensions, including automatic reconnection after abrupt primary restart.
5. Documentation, CI smoke coverage and fresh microbenchmarks updated.
