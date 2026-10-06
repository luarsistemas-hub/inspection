#!/usr/bin/env bash
set -euo pipefail
source /etc/inspection/public.env

compose_file="${INSPECTION_COMPOSE_FILE:-/opt/inspection/current/compose.yaml}"
compose=(docker compose --env-file /etc/inspection/compose.env -f "$compose_file")
deadline=$((SECONDS + 300))

retry_http() {
  local label="$1" url="$2" code
  while (( SECONDS < deadline )); do
    if curl --fail --location --silent --show-error --output /dev/null --max-time 10 "$url"; then
      return 0
    fi
    sleep 3
  done
  echo "Smoke check timed out: $label ($url)" >&2
  return 1
}

retry_private_storage() {
  local url="${STORAGE_ORIGIN}/${OCI_S3_BUCKET}/" status
  while (( SECONDS < deadline )); do
    status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' --max-time 10 "$url" || true)"
    [[ "$status" == 403 || "$status" == 404 ]] && return 0
    sleep 3
  done
  echo "Storage smoke check timed out; expected private-bucket 403 or 404." >&2
  return 1
}

retry_storage_cors() {
  local url="${STORAGE_ORIGIN}/${OCI_S3_BUCKET}/inspection-cors-smoke-missing-object" headers origins
  while (( SECONDS < deadline )); do
    headers="$(curl --silent --show-error --dump-header - --output /dev/null --max-time 10 --header "Origin: ${CAPTURE_ORIGIN}" "$url" || true)"
    origins="$(printf '%s\n' "$headers" | tr -d '\r' | awk 'tolower($1) == "access-control-allow-origin:" {print $2}')"
    if [[ "$origins" == "$CAPTURE_ORIGIN" ]] && printf '%s\n' "$headers" | tr -d '\r' | grep -Eiq '^access-control-expose-headers:.*etag'; then
      return 0
    fi
    sleep 3
  done
  echo "Storage CORS smoke check timed out; expected one Capture origin and exposed ETag." >&2
  return 1
}

services=(postgres dragonfly rabbitmq keycloak)
if grep -q '^  litellm:' "$compose_file"; then services+=(litellm); fi
services+=(inspection-api inspection-worker inspection-scheduler admin dashboard capture onboarding)
for service in "${services[@]}"; do
  id="$("${compose[@]}" ps -q "$service")"
  [[ -n "$id" ]] || { echo "Compose service has no container: $service" >&2; exit 1; }
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id")"
  [[ "$health" == healthy ]] || { echo "Compose service is not healthy: $service ($health)" >&2; exit 1; }
done
id="$("${compose[@]}" ps -q caddy)"
[[ -n "$id" ]] && [[ "$(docker inspect --format '{{.State.Status}}' "$id")" == running ]] || { echo "Caddy is not running" >&2; exit 1; }

retry_http Admin "${ADMIN_ORIGIN}/"
retry_http Dashboard "${DASHBOARD_ORIGIN}/"
retry_http Capture "${CAPTURE_ORIGIN}/healthz"
retry_http Onboarding "${ONBOARDING_ORIGIN}/"
retry_http API "${API_ORIGIN}/readyz"
retry_http OIDC "${OIDC_ISSUER}/.well-known/openid-configuration"
retry_private_storage
retry_storage_cors
echo "Compose health, public HTTPS, API readiness, OIDC discovery and private storage CORS passed."
