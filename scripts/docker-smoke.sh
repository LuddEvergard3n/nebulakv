#!/usr/bin/env bash
set -euo pipefail
image="${1:-nebulakv:local}"
name="nebulakv-smoke-$$"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker run -d --name "$name" "$image" --host 0.0.0.0 --appendonly --data /data >/dev/null
cli() { docker run --rm --network "container:$name" redis:8-alpine redis-cli -p 6380 --raw "$@"; }
ready=false
for attempt in $(seq 1 30); do
  if [ "$(cli PING 2>/dev/null)" = PONG ]; then ready=true; break; fi
  sleep 1
done
[ "$ready" = true ]
[ "$(cli SET greeting hello)" = OK ]
[ "$(cli GET greeting)" = hello ]
[ "$(cli SET temporary value PX 1000)" = OK ]
# Container startup overhead is deliberately included in this external demo.
sleep 2
[ "$(cli PTTL temporary)" = -2 ]
[ "$(cli SET counter 10)" = OK ]
[ "$(cli INCR counter)" = 11 ]
[ "$(cli SET greeting ignored NX GET)" = hello ]
cli INFO
docker restart "$name" >/dev/null
[ "$(cli GET greeting)" = hello ]
[ "$(cli GET counter)" = 11 ]
echo 'PASS: redis-cli, SET/GET, expiry, conditional SET, increment, restart'
