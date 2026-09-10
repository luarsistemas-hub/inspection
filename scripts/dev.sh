#!/usr/bin/env bash
set -Eeuo pipefail

workspace="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${INSPECTION_ENV_FILE:-$workspace/.env.inspection}"
usage() {
  cat <<'EOF'
Uso: ./scripts/dev.sh <api|worker|scheduler|admin|dashboard|capture|all>

A infraestrutura deve estar ativa (./scripts/local.sh infra).
Cada processo ocupa um terminal e termina com Ctrl+C.
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
    export INSPECTION_ALLOWED_ORIGINS="http://localhost:3000,http://localhost:3002,http://localhost:3003"
  elif [[ "$INSPECTION_ALLOWED_ORIGINS" == "http://localhost:3000,http://localhost:3002,http://localhost:3003" && "${INSPECTION_ADMIN_PORT:-3000}" != "3000" ]]; then
    export INSPECTION_ALLOWED_ORIGINS="http://localhost:${INSPECTION_ADMIN_PORT},http://localhost:${INSPECTION_DASHBOARD_PORT:-3002},http://localhost:${INSPECTION_CAPTURE_PORT:-3003}"
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
  cd "$workspace/apps/$app_dir"
  if [[ ! -d node_modules || ! -f node_modules/@inspection/design-system/dist/index.js || package-lock.json -nt node_modules/.package-lock.json ]]; then
    npm ci
  fi
}

component="${1:-help}"
[[ "$component" == "help" || "$component" == "-h" || "$component" == "--help" ]] && { usage; exit 0; }
[[ "$component" =~ ^(api|worker|scheduler|admin|dashboard|capture|all)$ ]] || { usage; die "componente desconhecido: $component"; }
load_env
case "$component" in
  api) need go; check_port "${INSPECTION_API_PORT:-8080}" "API"; export INSPECTION_HTTP_ADDR="${INSPECTION_API_ADDR:-:${INSPECTION_API_PORT:-8080}}"; exec go run ./services/inspection/cmd/inspection-api ;;
  worker)
    need go; check_port "${INSPECTION_WORKER_PORT:-8082}" "worker"; export INSPECTION_HTTP_ADDR="${INSPECTION_WORKER_ADDR:-:${INSPECTION_WORKER_PORT:-8082}}"
    export INSPECTION_DISPATCHER_DATABASE_URL="${INSPECTION_DISPATCHER_DATABASE_URL:-$INSPECTION_DATABASE_URL}"
    exec go run ./services/inspection/cmd/inspection-worker
    ;;
  scheduler) need go; check_port "${INSPECTION_SCHEDULER_PORT:-8083}" "scheduler"; export INSPECTION_HTTP_ADDR="${INSPECTION_SCHEDULER_ADDR:-:${INSPECTION_SCHEDULER_PORT:-8083}}"; exec go run ./services/inspection/cmd/inspection-scheduler ;;
  admin|dashboard|capture)
    need node; need npm
    case "$component" in admin) app_port=3000; app_dir=admin;; dashboard) app_port=3002; app_dir=dashboard;; capture) app_port=3003; app_dir=capture;; esac
    check_port "${app_port}" "$component"
    prepare_frontend "$app_dir"
    exec npm run dev -- --port "$app_port"
    ;;
  all)
    need node; need npm
    prepare_design_system
    for product in admin dashboard capture; do
      case "$product" in admin) port=3000;; dashboard) port=3002;; capture) port=3003;; esac
      check_port "$port" "$product"
      prepare_frontend "$product"
      (cd "$workspace/apps/$product" && npm run dev -- --port "$port") &
    done
    wait
    ;;
esac
