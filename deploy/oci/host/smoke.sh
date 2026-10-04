#!/usr/bin/env bash
set -euo pipefail
source /etc/inspection/public.env
for name in ADMIN_ORIGIN DASHBOARD_ORIGIN CAPTURE_ORIGIN ONBOARDING_ORIGIN; do
  value="${!name:-}"
  [[ "$value" == https://* ]] || { echo "${name} must be HTTPS" >&2; exit 1; }
curl --fail --silent --show-error --output /dev/null --max-time 15 "$value/"
done
for name in API_ORIGIN AUTH_ORIGIN STORAGE_ORIGIN; do
  [[ "${!name:-}" == https://* ]] || { echo "${name} must be HTTPS" >&2; exit 1; }
done
curl --fail --silent --show-error --output /dev/null --max-time 15 "${API_ORIGIN}/readyz"
curl --fail --silent --show-error --output /dev/null --max-time 15 "${OIDC_ISSUER}/.well-known/openid-configuration"
storage_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' --max-time 15 "${STORAGE_ORIGIN}/${OCI_S3_BUCKET}/")"
[[ "$storage_status" == 403 ]] || { echo "Anonymous bucket probe returned HTTP ${storage_status}; expected private-bucket 403." >&2; exit 1; }
compose=(docker compose --env-file /etc/inspection/compose.env -f "${INSPECTION_COMPOSE_FILE:-/opt/inspection/current/compose.yaml}")
running="$(${compose[@]} ps --status running --services)"
for service in postgres dragonfly rabbitmq keycloak inspection-api inspection-worker inspection-scheduler admin dashboard capture onboarding caddy; do
  grep -Fxq "$service" <<<"$running" || { echo "Compose service is not running: $service" >&2; exit 1; }
done
echo "Public HTTPS, private storage, API readiness, OIDC discovery and required Compose services passed."
