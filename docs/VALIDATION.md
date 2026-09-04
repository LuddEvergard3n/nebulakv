# Validation record

Date: 2026-09-04. Updated for v0.2 extensions. This report describes executed checks,
not planned capabilities.

| Gate | Result and scope |
| --- | --- |
| Formatting | `gofmt` applied; format check passed in the Docker build |
| Static analysis | `go vet ./...` passed on Windows and Linux |
| Unit/integration tests | `go test ./...` passed on Windows, including actual TCP connections |
| Race detector | `go test -race ./...` passed inside the Linux Docker build |
| Windows race detector | Not run: local C compiler not installed; Linux race gate passed |
| Build | Windows executable and static Linux container binary built |
| Docker | Multi-stage image `nebulakv:local` built; service ran under non-root UID 65532 |
| redis-cli | Official `redis:8-alpine` client connected and passed the asserted smoke script |
| SET/GET and SET options | SET/GET, NX+GET and increment verified with redis-cli; broader combinations unit-tested |
| TTL | Deterministic storage tests and actual redis-cli expiration passed |
| Restart | Values survived both graceful and abrupt container termination/restart |
| Binary persistence | Invalid UTF-8, NUL and CRLF values/keys survived file close/reopen tests |
| Recovery | Incomplete final records recovered; complete checksum corruption rejected |
| File ownership | Concurrent journal opens rejected on Windows and Linux; reopen after close passed |
| Authentication | Connection-local AUTH, failed reauthentication, five-failure closure and password-file validation passed; official client NOAUTH gate passed |
| Memory admission | Concurrent and atomic multi-key quota tests passed; official client oversized MSET returned OOM without changing existing data |
| Process memory ceiling | Both test containers configured with 256 MiB memory/no extra swap; Docker configuration inspected, no forced OOM-kill experiment |
| Compaction | Live/binary/TTL preservation, subsequent append/restart, concurrent writes and injected rename failure passed on Windows and Linux |
| Compaction size | Official client fixture reduced AOF from 8,995 to 127 bytes |
| Replication | Authenticated primary/replica synchronization, unchanged revisions, stale-data retention, read-only checks and replica restart passed |
| Replication failures | Interrupted/duplicate/malformed/oversized snapshots, wrong primary credentials and initial LOADING gate tested |
| Automatic reconnect | Primary killed and restarted; running replica received later writes automatically |
| Parser fuzzing | Five-second run, four workers, 427,637 executions; no failure in that run |
| Benchmarks | Three short Windows samples per storage workload; raw results committed |
| CI | GitHub Actions workflow exists for Windows/Linux and Docker; hosted execution not yet performed |

The v0.2 Docker image included all four extensions. Build output reported passing
race tests for command, config, persistence, replication, RESP, server and storage.

The official client image was used only for redis-cli. No Redis server implements
any part of NebulaKV. The temporary test server was removed by the smoke script.

Evidence: [asserted compatibility run](compatibility-run.txt),
[raw storage benchmarks](benchmark-windows.txt),
[initial measurements](benchmark-windows-before.txt),
[v0.2 extension smoke](resilience-run.txt),
[v0.2 microbenchmarks](benchmark-v0.2-windows.txt).

## Not verified in this environment

- An actual hosted GitHub Actions run or public repository/release.
- macOS/FreeBSD execution, network filesystems, faulty disks and power-loss recovery.
- Production traffic, long-duration load/soak tests, full Redis compatibility,
  persistent-write throughput, memory exhaustion behavior or an independent security audit.
- Automatic failover/consensus, incremental replication, TLS and forced memory-pressure kills.

The core acceptance checks for this educational first project have been executed.
That does not imply production readiness or completion of the other seven projects.
