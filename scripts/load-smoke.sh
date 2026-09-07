#!/usr/bin/env sh
set -eu

api=${INSPECTION_LOAD_API_URL:-http://localhost:8080}
requests=${INSPECTION_LOAD_REQUESTS:-100}
parallel=${INSPECTION_LOAD_PARALLEL:-20}
target_ms=${INSPECTION_LOAD_P95_MS:-300}
times=$(mktemp)
trap 'rm -f "$times"' EXIT

seq "$requests" | xargs -P "$parallel" -I '{}' sh -c 'curl -fsS -o /dev/null -w "%{time_total}\n" "$0/healthz"' "$api" >"$times"
count=$(wc -l <"$times" | tr -d ' ')
test "$count" -eq "$requests"
rank=$(( (count * 95 + 99) / 100 ))
p95=$(sort -n "$times" | sed -n "${rank}p")
p95_ms=$(awk -v seconds="$p95" 'BEGIN { printf "%d", seconds * 1000 + 0.5 }')
test "$p95_ms" -le "$target_ms"
printf 'load: %s requests, concurrency %s, p95 %sms (target %sms)\n' "$count" "$parallel" "$p95_ms" "$target_ms"
