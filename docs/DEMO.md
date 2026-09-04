# Interview walkthrough

## Start

Build and verify with `go test ./...` and `go build -o bin/nebulakv ./cmd/nebulakv`.
On Windows use `scripts/verify.ps1` then `bin/nebulakv.exe`.
Start with `--appendonly --data ./data` and use another terminal for redis-cli.

```sh
redis-cli -p 6380 PING
redis-cli -p 6380 SET greeting hello
redis-cli -p 6380 GET greeting
redis-cli -p 6380 SET greeting ignored NX GET
redis-cli -p 6380 GET greeting
redis-cli -p 6380 MSET left 10 right 20
redis-cli -p 6380 MGET left right missing
redis-cli -p 6380 INCR left
redis-cli -p 6380 SET session temporary PX 10000
redis-cli -p 6380 PTTL session
redis-cli -p 6380 INFO
```

Explain that NX prevents replacement but GET still returns the previous value.
Explain that MSET is one atomic operation and one journal record. Wait ten seconds
then GET session: it is missing. Stop the server with Ctrl+C and restart with the
same directory; greeting and left survive, while session remains absent.

## Inspect the implementation

1. Follow `internal/resp/decoder.go`: why is TCP fragmentation normal?
2. Follow `internal/storage/store.go`: where does the mutation become visible?
3. Follow `internal/persistence/aof.go`: when is an acknowledgment safe to send?
4. Show the incomplete-tail and corruption tests: why are they different cases?
5. Run the benchmark and explain exactly what is excluded from the measurement.

## Docker-only client

If redis-cli is not installed locally, run the server container with a name:

```sh
docker run --name nebulakv-demo -p 127.0.0.1:6380:6380 nebulakv:local \
  --host 0.0.0.0 --appendonly --data /data
```

In another terminal, replace each `redis-cli -p 6380` invocation above with:

```sh
docker run --rm --network container:nebulakv-demo redis:8-alpine redis-cli -p 6380
```

Use `docker stop nebulakv-demo` and `docker start nebulakv-demo` to demonstrate
restart persistence. The container's writable layer persists until it is removed.
The smoke scripts automate this with temporary containers and assertions.

## Suggested explanation

Present only behavior you can reproduce and code you can explain. The implementation
was developed with AI assistance; the portfolio value comes from understanding,
reviewing and extending its decisions rather than hiding that assistance.
