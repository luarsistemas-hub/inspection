#!/usr/bin/env sh
set -eu

api=${INSPECTION_SMOKE_API_URL:-http://localhost:8080}
web=${INSPECTION_SMOKE_WEB_URL:-http://localhost:3000}

health=$(curl -fsS "$api/healthz")
printf '%s' "$health" | grep -q 'alive'
ready=$(curl -fsS "$api/readyz")
printf '%s' "$ready" | grep -q 'ready'
curl -fsS "$web/sw.js" | grep -q 'inspection-static-v1'
curl -fsS "$web/manifest.webmanifest" | grep -q 'Inspe'

headers=$(mktemp)
trap 'rm -f "$headers"' EXIT
curl -fsSI "$web/" >"$headers"
grep -qi '^x-content-type-options: nosniff' "$headers"
grep -qi '^x-frame-options: DENY' "$headers"
grep -qi '^referrer-policy: same-origin' "$headers"
grep -qi '^content-security-policy:' "$headers"
printf '%s\n' 'smoke: health, readiness, PWA shell and security headers passed'
