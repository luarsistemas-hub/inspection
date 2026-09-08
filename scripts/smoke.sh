#!/usr/bin/env sh
set -eu

api=${INSPECTION_SMOKE_API_URL:-http://localhost:8080}
admin=${INSPECTION_SMOKE_ADMIN_URL:-http://localhost:3000}
dashboard=${INSPECTION_SMOKE_DASHBOARD_URL:-http://localhost:3002}
capture=${INSPECTION_SMOKE_CAPTURE_URL:-http://localhost:3003}

health=$(curl -fsS "$api/healthz")
printf '%s' "$health" | grep -q 'alive'
ready=$(curl -fsS "$api/readyz")
printf '%s' "$ready" | grep -q 'ready'
curl -fsS "$capture/sw.js" | grep -q 'inspection-static-v1'
curl -fsS "$capture/manifest.webmanifest" | grep -q 'Inspe'

headers=$(mktemp)
trap 'rm -f "$headers"' EXIT
for product_url in "$admin" "$dashboard" "$capture"; do curl -fsSI "$product_url/" >"$headers"
grep -qi '^x-content-type-options: nosniff' "$headers"
grep -qi '^x-frame-options: DENY' "$headers"
grep -qi '^referrer-policy: same-origin' "$headers"
grep -qi '^content-security-policy:' "$headers"
done
printf '%s\n' 'smoke: health, readiness, PWA shell and security headers passed'
