#!/usr/bin/env bash
set -Eeuo pipefail

workspace="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${INSPECTION_ENV_FILE:-$workspace/.env.inspection}"
usage() {
  cat <<'EOF'
Uso: ./scripts/dev.sh <api|worker|scheduler|web>

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
    [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
    key="${BASH_REMATCH[1]}"; value="${BASH_REMATCH[2]}"
    if printenv "$key" >/dev/null 2>&1; then :; else export "$key=$value"; fi
  done < "$env_file"
  if [[ "${NEXT_PUBLIC_INSPECTION_API_URL:-}" == "http://localhost:8080/graphql" && "${INSPECTION_API_PORT:-8080}" != "8080" ]]; then
    export NEXT_PUBLIC_INSPECTION_API_URL="http://localhost:${INSPECTION_API_PORT}/graphql"
  fi
  if [[ "${INSPECTION_ALLOWED_ORIGIN:-}" == "http://localhost:3000" && "${INSPECTION_WEB_PORT:-3000}" != "3000" ]]; then
    export INSPECTION_ALLOWED_ORIGIN="http://localhost:${INSPECTION_WEB_PORT}"
  fi
}
component="${1:-help}"
[[ "$component" == "help" || "$component" == "-h" || "$component" == "--help" ]] && { usage; exit 0; }
[[ "$component" =~ ^(api|worker|scheduler|web)$ ]] || { usage; die "componente desconhecido: $component"; }
load_env
case "$component" in
  api) need go; check_port "${INSPECTION_API_PORT:-8080}" "API"; export INSPECTION_HTTP_ADDR="${INSPECTION_API_ADDR:-:${INSPECTION_API_PORT:-8080}}"; exec go run ./services/inspection/cmd/inspection-api ;;
  worker)
    need go; check_port "${INSPECTION_WORKER_PORT:-8082}" "worker"; export INSPECTION_HTTP_ADDR="${INSPECTION_WORKER_ADDR:-:${INSPECTION_WORKER_PORT:-8082}}"
    export INSPECTION_DISPATCHER_DATABASE_URL="${INSPECTION_DISPATCHER_DATABASE_URL:-$INSPECTION_DATABASE_URL}"
    exec go run ./services/inspection/cmd/inspection-worker
    ;;
  scheduler) need go; check_port "${INSPECTION_SCHEDULER_PORT:-8083}" "scheduler"; export INSPECTION_HTTP_ADDR="${INSPECTION_SCHEDULER_ADDR:-:${INSPECTION_SCHEDULER_PORT:-8083}}"; exec go run ./services/inspection/cmd/inspection-scheduler ;;
  web)
    need node; need npm; web_port="${INSPECTION_WEB_PORT:-3000}"; check_port "$web_port" "frontend"; cd "$workspace/apps/web"
    if [[ ! -d node_modules || package-lock.json -nt node_modules/.package-lock.json ]]; then npm ci; fi
    exec npm run dev -- --port "$web_port"
    ;;
esac
