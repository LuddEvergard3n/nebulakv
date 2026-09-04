# Measured storage benchmarks

## v0.2 with memory admission

[Raw v0.2 run](benchmark-v0.2-windows.txt), same command/hardware below, taken while
Docker smoke tests were also running. GET: 30.35–33.40 ns/op; replacement SET:
69.47–81.04 ns/op. Both still reported 0 B/op and 0 allocs/op. Mixed GET/SET:
45.01–65.56 ns/op. SET now performs budget accounting, so the previous timings should
not be presented as the current implementation's performance. These short samples
still exclude network, persistence, snapshot transfer and new-key growth.

## Historical v0.1 run

2026-09-04, Windows/amd64, Go 1.27.1, Intel Core i9-13900HX, default
GOMAXPROCS=32. The host was also running Docker/build work. These short samples
are development measurements, not controlled capacity estimates.

```sh
go test ./internal/storage -run '^$' -bench . -benchmem -benchtime=250ms -count=3
```

| Workload | Observed ns/op range (3 runs) | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Existing-key GET | 28.49–30.02 | 0 | 0 |
| Existing-key replacement SET | 39.35–42.60 | 0 | 0 |
| Alternating GET/SET | 34.00–51.50 | 0 | 0 |
| Contended INCR, parallelism multiplier 1 | 262.8–282.3 | 15 | 2 |
| Contended INCR, multiplier 2 | 270.2–284.7 | 15 | 2 |
| Contended INCR, multiplier 4 | 266.0–289.5 | 15 | 2 |
| Contended INCR, multiplier 8 | 288.5–307.6 | 15 | 2 |

GET/SET reuse one fixed key and value. The map is initialized outside the timed
loop. INCR contends on a shared counter; `SetParallelism` multiplies GOMAXPROCS,
so these labels mean approximately 32/64/128/256 worker goroutines, not clients.
The benchmark includes locking and clock lookup but excludes RESP, TCP, disk,
random key distributions, new-key allocations and large payloads.

[Raw final run](benchmark-windows.txt).
[Raw initial run](benchmark-windows-before.txt) captured the previous path:
single-key reads allocated a multi-key result and writes constructed unnecessary
journal records. GET used 25 B/op (2 allocations), SET used 88 B/op (5 allocations).
The optimized path avoids those allocations; timing ratios should not be treated
as a controlled comparison because both workload plumbing and host load changed.

External reproduction with a running memory-only server:

```sh
redis-benchmark -p 6380 -t set,get -n 10000 -c 16 -P 4
```

External throughput and persistent-write latency have not been measured. Do not
convert these nanosecond storage microbenchmarks into advertised server requests/s.
