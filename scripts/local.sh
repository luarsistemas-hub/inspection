#!/usr/bin/env bash
set -Eeuo pipefail

workspace="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${INSPECTION_ENV_FILE:-$workspace/.env.inspection}"
compose_file="$workspace/deploy/docker-compose.yml"

usage() {
  cat <<'EOF'
Uso: ./scripts/local.sh <comando>

Comandos:
  init             cria .env.inspection a partir de .env.example
  up               constrói e sobe a stack completa (Admin, Dashboard, Capture e Onboarding)
  infra            sobe somente infraestrutura, bootstrap e migrations
  migrate          executa migrations e recria os papéis locais
  seed             cria dados locais para QA após o onboarding do Admin
  status           mostra containers e URLs locais
  logs [serviço]   acompanha logs de toda a stack ou de um serviço
  down             para a stack preservando volumes
  help             mostra esta ajuda

Variáveis úteis: INSPECTION_ENV_FILE e INSPECTION_STARTUP_TIMEOUT (segundos).
EOF
}

die() { printf 'erro: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "comando obrigatório não encontrado: $1"; }

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
}

compose() { docker compose --env-file "$env_file" -f "$compose_file" "$@"; }

wait_http() {
  local url="$1" label="$2" timeout="${INSPECTION_STARTUP_TIMEOUT:-180}" start now
  start="$(date +%s)"
  while :; do
    if curl --silent --show-error --fail --max-time 3 "$url" >/dev/null 2>&1; then
      printf '%s disponível: %s\n' "$label" "$url"; return 0
    fi
    now="$(date +%s)"
    (( now - start >= timeout )) && die "tempo esgotado aguardando $label ($url); veja: ./scripts/local.sh logs"
    sleep 2
  done
}

wait_service_completion() {
  local service="$1" timeout="${INSPECTION_STARTUP_TIMEOUT:-180}" start now container_id status exit_code
  start="$(date +%s)"
  while :; do
    container_id="$(compose ps -aq "$service" 2>/dev/null || true)"
    if [[ -n "$container_id" ]]; then
      status="$(docker inspect --format '{{.State.Status}}' "$container_id")"
      case "$status" in
        exited)
          exit_code="$(docker inspect --format '{{.State.ExitCode}}' "$container_id")"
          [[ "$exit_code" == "0" ]] || die "$service terminou com código $exit_code; veja: ./scripts/local.sh logs $service"
          printf '%s concluído.\n' "$service"
          return 0
          ;;
        created|running|restarting) ;;
        *) die "estado inesperado para $service: $status" ;;
      esac
    fi
    now="$(date +%s)"
    (( now - start >= timeout )) && die "tempo esgotado aguardando $service; veja: ./scripts/local.sh logs $service"
    sleep 2
  done
}

init() {
  [[ -e "$env_file" ]] && { printf 'já existe: %s\n' "$env_file"; return 0; }
  cp "$workspace/.env.example" "$env_file"
  printf 'criado: %s\n' "$env_file"
  printf 'revise os valores antes de executar processos fora do Docker.\n'
}

require_base() { need docker; need curl; [[ -f "$compose_file" ]] || die "Compose não encontrado: $compose_file"; }

check_port() {
  local port="$1" label="$2"
  if command -v lsof >/dev/null 2>&1 && lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    die "porta $port ($label) já está ocupada; altere INSPECTION_*_PORT ou pare o processo"
  fi
}

case "${1:-help}" in
  init) init ;;
  up)
    require_base; [[ -f "$env_file" ]] || init; load_env
    if ! compose ps --status running -q inspection-api 2>/dev/null | grep -q .; then
      check_port "${INSPECTION_API_PORT:-8080}" "API"
      check_port "${INSPECTION_ADMIN_PORT:-3000}" "Admin"
      check_port "${INSPECTION_DASHBOARD_PORT:-3002}" "Dashboard"
      check_port "${INSPECTION_CAPTURE_PORT:-3003}" "Capture"
      check_port "${INSPECTION_ONBOARDING_PORT:-3004}" "Onboarding"
    fi
    compose up -d --build
    wait_http "http://localhost:${INSPECTION_API_PORT:-8080}/healthz" "API"
    wait_http "http://localhost:${INSPECTION_API_PORT:-8080}/readyz" "API pronta"
    wait_http "http://localhost:${INSPECTION_ADMIN_PORT:-3000}/" "Admin"
    wait_http "http://localhost:${INSPECTION_DASHBOARD_PORT:-3002}/inspections" "Dashboard"
    wait_http "http://localhost:${INSPECTION_CAPTURE_PORT:-3003}/manifest.webmanifest" "Capture"
    wait_http "http://localhost:${INSPECTION_ONBOARDING_PORT:-3004}/" "Onboarding"
    ;;
  infra)
    require_base; [[ -f "$env_file" ]] || init; load_env
    compose up -d postgres redis minio rabbitmq mailpit twilio-fake litellm-stub gotenberg gotenberg-stub keycloak minio-setup inspection-bootstrap inspection-migrate inspection-runtime-bootstrap keycloak-super-admin-bootstrap
    wait_service_completion inspection-bootstrap
    wait_service_completion inspection-migrate
    wait_service_completion inspection-runtime-bootstrap
    wait_service_completion keycloak-super-admin-bootstrap
    printf 'infraestrutura e migrations concluídas.\n'
    ;;
  migrate)
    require_base; [[ -f "$env_file" ]] || init; load_env
    compose up -d postgres minio minio-setup inspection-bootstrap
    compose run --rm inspection-migrate
    compose run --rm inspection-runtime-bootstrap
    printf 'migrations e papéis locais concluídos.\n'
    ;;
  seed)
    require_base; [[ -f "$env_file" ]] || init; load_env
    started_api=0
    if ! compose ps --status running -q inspection-api 2>/dev/null | grep -q .; then
      check_port "${INSPECTION_API_PORT:-8080}" "API de seed"
      compose up -d --build inspection-api
      started_api=1
    fi
    cleanup_seed_api() {
      if [[ "$started_api" == 1 ]]; then
        compose stop inspection-api >/dev/null 2>&1 || true
      fi
    }
    trap cleanup_seed_api EXIT
    wait_http "http://localhost:${INSPECTION_API_PORT:-8080}/healthz" "API de seed"
    wait_http "http://localhost:${INSPECTION_API_PORT:-8080}/readyz" "API de seed pronta"
    compose run --rm --no-deps --build inspection-seed
    trap - EXIT
    cleanup_seed_api
    ;;
  status)
    require_base; [[ -f "$env_file" ]] || init; load_env
    compose ps
    cat <<EOF

URLs locais:
  Admin          http://localhost:${INSPECTION_ADMIN_PORT:-3000}
  Dashboard      http://localhost:${INSPECTION_DASHBOARD_PORT:-3002}
  Capture        http://localhost:${INSPECTION_CAPTURE_PORT:-3003}
  Onboarding     http://localhost:${INSPECTION_ONBOARDING_PORT:-3004}
  GraphQL        http://localhost:${INSPECTION_API_PORT:-8080}/graphql
  Health/ready   http://localhost:${INSPECTION_API_PORT:-8080}/healthz | /readyz
  Keycloak       http://localhost:8081 (credenciais em .env.inspection)
  MinIO API      http://localhost:${INSPECTION_MINIO_PORT:-9002}
  MinIO Console  http://localhost:${INSPECTION_MINIO_CONSOLE_PORT:-9003} (inspection/inspection-local-secret)
  RabbitMQ       http://localhost:${INSPECTION_RABBITMQ_MANAGEMENT_PORT:-15673} (inspection/inspection)
  Mailpit        http://localhost:${INSPECTION_MAILPIT_UI_PORT:-8026}
EOF
    ;;
  logs)
    require_base; [[ -f "$env_file" ]] || init; load_env
    if [[ -n "${2:-}" ]]; then compose logs -f "$2"; else compose logs -f; fi
    ;;
  down)
    require_base; [[ -f "$env_file" ]] || init; load_env; compose down
    ;;
  help|-h|--help) usage ;;
  *) usage; die "comando desconhecido: $1" ;;
esac
