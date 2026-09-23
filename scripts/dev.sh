#!/usr/bin/env bash
set -Eeuo pipefail

workspace="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${INSPECTION_ENV_FILE:-$workspace/.env.inspection}"
usage() {
  cat <<'EOF'
Uso: ./scripts/dev.sh <api|worker|scheduler|admin|dashboard|capture|onboarding|all>

A infraestrutura deve estar ativa (./scripts/local.sh infra).
Os comandos individuais iniciam um processo. `all` inicia API, worker,
scheduler e os quatro frontends no host; todos terminam com Ctrl+C.

Variáveis úteis: INSPECTION_ENV_FILE e INSPECTION_STARTUP_TIMEOUT (segundos).
EOF
}
die() { printf 'erro: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "comando obrigatório não encontrado: $1"; }
check_port() {
  local port="$1" label="$2"
  if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    die "porta $port ($label) já está ocupada"
  fi
}
wait_http() {
  local url="$1" label="$2" pid="${3:-}" timeout="${INSPECTION_STARTUP_TIMEOUT:-180}" start now
  start="$(date +%s)"
  while :; do
    if curl --silent --show-error --fail --max-time 3 "$url" >/dev/null 2>&1; then
      printf '%s disponível: %s\n' "$label" "$url"
      return 0
    fi
    if [[ -n "$pid" ]] && ! kill -0 "$pid" >/dev/null 2>&1; then
      wait "$pid" 2>/dev/null || true
      die "$label encerrou antes de ficar disponível; veja a saída do processo"
    fi
    now="$(date +%s)"
    (( now - start >= timeout )) && die "tempo esgotado aguardando $label ($url)"
    sleep 2
  done
}
load_env() {
  [[ -f "$env_file" ]] || die "arquivo $env_file não existe; execute ./scripts/local.sh init"
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^[[:space:]]*$|^[[:space:]]*# ]] && continue
    [[ "$line" =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
    key="${BASH_REMATCH[1]}"; value="${BASH_REMATCH[2]}"
    if printenv "$key" >/dev/null 2>&1; then :; else export "$key=$value"; fi
  done < "$env_file"
  if [[ "${NEXT_PUBLIC_INSPECTION_API_URL:-}" == "http://localhost:8080/graphql" && "${INSPECTION_API_PORT:-8080}" != "8080" ]]; then
    export NEXT_PUBLIC_INSPECTION_API_URL="http://localhost:${INSPECTION_API_PORT}/graphql"
  fi
  if [[ -z "${INSPECTION_ALLOWED_ORIGINS:-}" ]]; then
    export INSPECTION_ALLOWED_ORIGINS="http://localhost:3000,http://localhost:3002,http://localhost:3003,http://localhost:3004"
  elif [[ "$INSPECTION_ALLOWED_ORIGINS" == "http://localhost:3000,http://localhost:3002,http://localhost:3003" ]]; then
    export INSPECTION_ALLOWED_ORIGINS="http://localhost:${INSPECTION_ADMIN_PORT},http://localhost:${INSPECTION_DASHBOARD_PORT:-3002},http://localhost:${INSPECTION_CAPTURE_PORT:-3003},http://localhost:${INSPECTION_ONBOARDING_PORT:-3004}"
  fi
  if [[ "${INSPECTION_ENV:-local}" == "local" ]]; then
    # Compose supplies these local-only defaults. Keep host processes compatible
    # with the Docker stack even when .env.inspection predates these settings.
    export INSPECTION_ADMIN_ORIGIN="${INSPECTION_ADMIN_ORIGIN:-http://localhost:${INSPECTION_ADMIN_PORT:-3000}}"
    export INSPECTION_CAPTURE_ORIGIN="${INSPECTION_CAPTURE_ORIGIN:-http://localhost:${INSPECTION_CAPTURE_PORT:-3003}}"
    export NOTIFICATION_MAX_ATTEMPTS="${NOTIFICATION_MAX_ATTEMPTS:-4}"
    export NOTIFICATION_RETRY_DELAYS="${NOTIFICATION_RETRY_DELAYS:-5s,30s,5m}"
    export SMTP_TLS_MODE="${SMTP_TLS_MODE:-none}"
    export NOTIFICATION_WHATSAPP_PROVIDER="${NOTIFICATION_WHATSAPP_PROVIDER:-twilio}"
    export NOTIFICATION_PAYLOAD_KEYS="${NOTIFICATION_PAYLOAD_KEYS:-}"
    if [[ -z "$NOTIFICATION_PAYLOAD_KEYS" ]]; then
      export NOTIFICATION_PAYLOAD_KEYS='{"local-v1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}'
    fi
    export NOTIFICATION_ACTIVE_PAYLOAD_KEY="${NOTIFICATION_ACTIVE_PAYLOAD_KEY:-local-v1}"
    export TWILIO_TEMPLATE_MAP="${TWILIO_TEMPLATE_MAP:-}"
    if [[ -z "$TWILIO_TEMPLATE_MAP" ]]; then
      export TWILIO_TEMPLATE_MAP='{"capture-link:v1:pt-BR":"HX-local","recapture-link:v1:pt-BR":"HX-local","reminder:v1:pt-BR":"HX-local","critical-alert:v1:pt-BR":"HX-local"}'
    fi
  fi
}

prepare_design_system() {
  local package_dir="$workspace/packages/inspection-design-system"
  if [[ ! -f "$package_dir/dist/index.js" || ! -f "$package_dir/dist/styles.css" ]]; then
    (cd "$package_dir" && npm ci && npm run build)
  fi
}

prepare_frontend() {
  local app_dir="$1"
  prepare_design_system
  (
    cd "$workspace/apps/$app_dir"
    if [[ ! -d node_modules || ! -f node_modules/@inspection/design-system/dist/index.js || package-lock.json -nt node_modules/.package-lock.json ]]; then
      npm ci
    fi
  )
}

component="${1:-help}"
[[ "$component" == "help" || "$component" == "-h" || "$component" == "--help" ]] && { usage; exit 0; }
[[ "$component" =~ ^(api|worker|scheduler|admin|dashboard|capture|onboarding|all)$ ]] || { usage; die "componente desconhecido: $component"; }
load_env
case "$component" in
  api) need go; check_port "${INSPECTION_API_PORT:-8080}" "API"; export INSPECTION_HTTP_ADDR="${INSPECTION_API_ADDR:-:${INSPECTION_API_PORT:-8080}}"; cd "$workspace"; exec go run ./services/inspection/cmd/inspection-api ;;
  worker)
    need go; check_port "${INSPECTION_WORKER_PORT:-8082}" "worker"; export INSPECTION_HTTP_ADDR="${INSPECTION_WORKER_ADDR:-:${INSPECTION_WORKER_PORT:-8082}}"; cd "$workspace"
    export INSPECTION_DISPATCHER_DATABASE_URL="${INSPECTION_DISPATCHER_DATABASE_URL:-$INSPECTION_DATABASE_URL}"
    exec go run ./services/inspection/cmd/inspection-worker
    ;;
  scheduler) need go; check_port "${INSPECTION_SCHEDULER_PORT:-8083}" "scheduler"; export INSPECTION_HTTP_ADDR="${INSPECTION_SCHEDULER_ADDR:-:${INSPECTION_SCHEDULER_PORT:-8083}}"; cd "$workspace"; exec go run ./services/inspection/cmd/inspection-scheduler ;;
  admin|dashboard|capture|onboarding)
    need node; need npm
    case "$component" in admin) app_port="${INSPECTION_ADMIN_PORT:-3000}"; app_dir=admin;; dashboard) app_port="${INSPECTION_DASHBOARD_PORT:-3002}"; app_dir=dashboard;; capture) app_port="${INSPECTION_CAPTURE_PORT:-3003}"; app_dir=capture;; onboarding) app_port="${INSPECTION_ONBOARDING_PORT:-3004}"; app_dir=onboarding;; esac
    check_port "${app_port}" "$component"
    prepare_frontend "$app_dir"
    cd "$workspace/apps/$app_dir"
    exec npm run dev -- --port "$app_port"
    ;;
  all)
    need curl; need go; need node; need npm
    api_ready=0
    if curl --silent --show-error --fail --max-time 3 "http://localhost:${INSPECTION_API_PORT:-8080}/readyz" >/dev/null 2>&1; then
      api_ready=1
      printf 'API já disponível: http://localhost:%s/readyz\n' "${INSPECTION_API_PORT:-8080}"
    else
      check_port "${INSPECTION_API_PORT:-8080}" "API"
    fi
    check_port "${INSPECTION_WORKER_PORT:-8082}" "worker"
    check_port "${INSPECTION_SCHEDULER_PORT:-8083}" "scheduler"
    check_port "${INSPECTION_ADMIN_PORT:-3000}" "admin"
    check_port "${INSPECTION_DASHBOARD_PORT:-3002}" "dashboard"
    check_port "${INSPECTION_CAPTURE_PORT:-3003}" "capture"
    check_port "${INSPECTION_ONBOARDING_PORT:-3004}" "onboarding"
    prepare_design_system
    for product in admin dashboard capture onboarding; do
      prepare_frontend "$product"
    done
    cd "$workspace"
    children=()
    cleanup() {
      local status="$?"
      trap - EXIT INT TERM
      if ((${#children[@]})); then
        kill "${children[@]}" >/dev/null 2>&1 || true
        wait "${children[@]}" >/dev/null 2>&1 || true
      fi
      exit "$status"
    }
    trap cleanup EXIT INT TERM

    if (( api_ready == 0 )); then
      "$workspace/scripts/dev.sh" api & children+=("$!")
      wait_http "http://localhost:${INSPECTION_API_PORT:-8080}/readyz" "API pronta" "${children[0]}"
    fi
    "$workspace/scripts/dev.sh" worker & children+=("$!")
    "$workspace/scripts/dev.sh" scheduler & children+=("$!")
    for product in admin dashboard capture onboarding; do
      "$workspace/scripts/dev.sh" "$product" & children+=("$!")
    done
    wait "${children[@]}"
    ;;
esac
