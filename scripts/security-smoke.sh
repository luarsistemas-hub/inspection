#!/usr/bin/env sh
set -eu

api=${INSPECTION_SECURITY_API_URL:-http://localhost:8080}
products="${INSPECTION_SECURITY_PRODUCT_URLS:-http://localhost:3000/ http://localhost:3002/ http://localhost:3003/healthz}"
body='{"query":"query { tenant { id } }"}'
response=$(curl -fsS -H 'Content-Type: application/json' -d "$body" "$api/graphql")
printf '%s' "$response" | grep -q 'UNAUTHENTICATED'

headers=$(mktemp)
trap 'rm -f "$headers"' EXIT
for product_url in $products; do curl -fsSI "$product_url" >"$headers"
! grep -qi '^access-control-allow-origin: \*' "$headers"
grep -qi 'frame-ancestors' "$headers"
grep -qi 'connect-src' "$headers"
! grep -qi '^x-powered-by:' "$headers"
done
printf '%s\n' 'security: unauthenticated GraphQL and browser isolation checks passed'
