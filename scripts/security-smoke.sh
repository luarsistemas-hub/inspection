#!/usr/bin/env sh
set -eu

api=${INSPECTION_SECURITY_API_URL:-http://localhost:8080}
web=${INSPECTION_SECURITY_WEB_URL:-http://localhost:3000}
body='{"query":"query { tenant { id } }"}'
response=$(curl -fsS -H 'Content-Type: application/json' -d "$body" "$api/graphql")
printf '%s' "$response" | grep -q 'UNAUTHENTICATED'

headers=$(mktemp)
trap 'rm -f "$headers"' EXIT
curl -fsSI "$web/" >"$headers"
! grep -qi '^access-control-allow-origin: \*' "$headers"
grep -qi 'frame-ancestors' "$headers"
grep -qi 'connect-src' "$headers"
! grep -qi '^x-powered-by:' "$headers"
printf '%s\n' 'security: unauthenticated GraphQL and browser isolation checks passed'
