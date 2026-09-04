# Validation record

Date: 2026-09-04. This report describes executed checks, not planned capabilities.

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
| Parser fuzzing | Five-second run, four workers, 427,637 executions; no failure in that run |
| Benchmarks | Three short Windows samples per storage workload; raw results committed |
| CI | GitHub Actions workflow exists for Windows/Linux and Docker; hosted execution not yet performed |

The Docker image used for the final smoke checks included the memory-only allocation
optimization and updated active-expiration assertion. Build output reported passing
race tests for command, config, persistence, RESP, server and storage packages.

The official client image was used only for redis-cli. No Redis server implements
any part of NebulaKV. The temporary test server was removed by the smoke script.

Evidence: [asserted compatibility run](compatibility-run.txt),
[raw storage benchmarks](benchmark-windows.txt),
[initial measurements](benchmark-windows-before.txt).

## Not verified in this environment

- An actual hosted GitHub Actions run or public repository/release.
- macOS/FreeBSD execution, network filesystems, faulty disks and power-loss recovery.
- Production traffic, long-duration load/soak tests, full Redis compatibility,
  persistent-write throughput, memory exhaustion behavior or an independent security audit.
- AOF rewrite/compaction: intentionally not implemented, as an extension milestone.

The core acceptance checks for this educational first project have been executed.
That does not imply production readiness or completion of the other seven projects.
