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

for service in postgres dragonfly rabbitmq keycloak inspection-api inspection-worker inspection-scheduler admin dashboard capture onboarding; do
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
echo "Compose health, public HTTPS, API readiness, OIDC discovery and private storage passed."
