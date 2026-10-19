#!/usr/bin/env bash
set -euo pipefail
source /etc/inspection/public.env

compose_file="${INSPECTION_COMPOSE_FILE:-/opt/inspection/current/compose.yaml}"
compose_env="${INSPECTION_COMPOSE_ENV:-/etc/inspection/compose.env}"
compose=(docker compose --env-file "$compose_env" -f "$compose_file")
deadline=$((SECONDS + 300))

retry_http() {
  local label="$1" url="$2"
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

script_path="$(readlink -f "${BASH_SOURCE[0]}")"
script_dir="$(dirname "$script_path")"
catalog_helper="$script_dir/component-catalog.sh"
if [[ ! -s "$catalog_helper" ]]; then
  host_tools_version="$(basename "$script_dir")"
  releases_root="${INSPECTION_RELEASES_ROOT:-/opt/inspection/releases}"
  [[ "$host_tools_version" =~ ^[a-f0-9]{40}$ ]] && catalog_helper="$releases_root/$host_tools_version/deploy/oci/host/component-catalog.sh"
fi
[[ -s "$catalog_helper" ]] || { echo "Component catalog resolver is missing for host script: $script_path" >&2; exit 1; }
# shellcheck source=deploy/oci/host/component-catalog.sh
source "$catalog_helper"
inspection_resolve_component_catalog "$script_path"
catalog="$INSPECTION_RESOLVED_COMPONENT_CATALOG"
compose_services="$("${compose[@]}" config --services)"
compose_environment="$("${compose[@]}" config --environment)"
service_output="$(python3 - "$catalog" "$compose_services" "$compose_environment" <<'PY'
import json, sys
try:
    components=json.load(open(sys.argv[1], encoding="utf-8"))["components"]
except (OSError, ValueError, KeyError, TypeError) as exc:
    raise SystemExit(f"invalid or missing component catalog: {exc}")
if not isinstance(components, dict) or not components or any(not isinstance(item, dict) or not isinstance(item.get("image"), str) or not isinstance(item.get("service"), (str, type(None))) for item in components.values()):
    raise SystemExit("component catalog must define image and service for every component")
services=set(sys.argv[2].splitlines())
environment={line.partition("=")[0] for line in sys.argv[3].splitlines() if "=" in line}
required=[]
for name, item in components.items():
    service=item["service"]
    if service is None:
        continue
    if service in services:
        required.append(service)
    elif item["image"] in environment:
        raise SystemExit(f"Compose image {item['image']} is configured but service {service!r} is missing")
print("\n".join(required))
PY
)"
services=()
while IFS= read -r service; do [[ -n "$service" ]] && services+=("$service"); done <<< "$service_output"
services=(postgres dragonfly rabbitmq keycloak litellm "${services[@]}")
for service in "${services[@]}"; do
  id="$("${compose[@]}" ps -q "$service")"
  [[ -n "$id" ]] || { echo "Compose service has no container: $service" >&2; exit 1; }
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id")"
  [[ "$health" == healthy ]] || { echo "Compose service is not healthy: $service ($health)" >&2; exit 1; }
done
if printf '%s\n' "${services[@]}" | grep -Fxq inspection-video-worker; then
  "${compose[@]}" exec -T inspection-video-worker ffmpeg -version >/dev/null
  "${compose[@]}" exec -T inspection-video-worker ffprobe -version >/dev/null
  "${compose[@]}" exec -T inspection-video-worker sh -eu -c 'f=/var/lib/inspection-video-scratch/.smoke-$$; : > "$f"; rm -f "$f"'
  retry_http 'video worker readiness' http://127.0.0.1:8091/readyz
  metrics_token="$(cat /etc/inspection/secrets.d/INSPECTION_METRICS_TOKEN)"
  [[ -n "$metrics_token" ]] || { echo 'Metrics token is missing.' >&2; exit 1; }
  video_metrics_body=''
  while (( SECONDS < deadline )); do
    video_metrics_body="$(curl --fail --silent --show-error --max-time 10 --header "Authorization: Bearer ${metrics_token}" http://127.0.0.1:8091/metrics 2>/dev/null || true)"
    if grep -Eq '^inspection_video_[a-zA-Z0-9_]*(\{|[[:space:]])' <<< "$video_metrics_body"; then break; fi
    sleep 3
  done
  unset metrics_token
  grep -Eq '^inspection_video_[a-zA-Z0-9_]*(\{|[[:space:]])' <<< "$video_metrics_body" || { echo 'Video worker authenticated metrics smoke check failed.' >&2; exit 1; }
fi
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
