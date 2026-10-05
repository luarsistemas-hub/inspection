#!/usr/bin/env bash
set -euo pipefail

release="${1:-}"
mode="${2:-}"
[[ "$release" =~ ^[a-f0-9]{40}$ ]] || { echo "Invalid release SHA" >&2; exit 2; }
[[ -z "$mode" || "$mode" == --rollback ]] || { echo "Invalid deployment mode" >&2; exit 2; }
release_dir="/opt/inspection/releases/$release"
release_env="/etc/inspection/releases/$release.env"
[[ -d "$release_dir/deploy/oci" && -s "$release_env" ]] || { echo "Release bundle or manifest is missing" >&2; exit 1; }
grep -qx "RELEASE_SHA=$release" "$release_env" || { echo "Release manifest SHA does not match" >&2; exit 1; }

exec 9>/run/lock/inspection-deploy.lock
flock -n 9 || { echo "Another deployment is running" >&2; exit 1; }
export PATH="/opt/inspection/oci-cli/bin:$PATH"
set -a
source /etc/inspection/public.env
set +a

block_path=/etc/inspection/deployment-blocked
previous_compose_env="$(mktemp /run/inspection-compose-before-deploy.XXXXXX)"
previous_manifest="$(mktemp /run/inspection-manifest-before-deploy.XXXXXX)"
previous_block="$(mktemp /run/inspection-block-before-deploy.XXXXXX)"
previous_target="$(readlink /opt/inspection/current || true)"
had_compose_env=0
had_manifest=0
had_block=0
if [[ -f /etc/inspection/compose.env ]]; then install -m 0600 /etc/inspection/compose.env "$previous_compose_env"; had_compose_env=1; fi
if [[ -f /etc/inspection/releases/current.env ]]; then install -m 0600 /etc/inspection/releases/current.env "$previous_manifest"; had_manifest=1; fi
if [[ -f "$block_path" ]]; then install -m 0600 "$block_path" "$previous_block"; had_block=1; fi

phase=preflight
block_reason=""
app_services=(inspection-api inspection-worker inspection-scheduler admin dashboard capture onboarding caddy)
atomic_link() {
  local target="$1" link="$2" temporary="${2}.new.$$"
  ln -s "$target" "$temporary"
  mv -Tf "$temporary" "$link"
}
write_block() {
  local temporary="${block_path}.new.$$"
  printf 'RELEASE_SHA=%s\nPHASE=%s\nREASON=%s\n' "$release" "$phase" "$block_reason" > "$temporary"
  chmod 0600 "$temporary"
  chown root:root "$temporary"
  mv -f "$temporary" "$block_path"
}
restore_pre_mutation() {
  if [[ "$had_compose_env" == 1 ]]; then
    install -o root -g root -m 0600 "$previous_compose_env" /etc/inspection/.compose.env.rollback
    mv -f /etc/inspection/.compose.env.rollback /etc/inspection/compose.env
  else
    rm -f /etc/inspection/compose.env
  fi
  if [[ "$had_manifest" == 1 ]]; then
    install -o root -g root -m 0600 "$previous_manifest" /etc/inspection/releases/.current.env.rollback
    mv -f /etc/inspection/releases/.current.env.rollback /etc/inspection/releases/current.env
  else
    rm -f /etc/inspection/releases/current.env
  fi
  if [[ "$had_block" == 1 ]]; then
    install -o root -g root -m 0600 "$previous_block" /etc/inspection/.deployment-blocked.rollback
    mv -f /etc/inspection/.deployment-blocked.rollback "$block_path"
  else
    rm -f "$block_path"
  fi
  if [[ -n "$previous_target" ]]; then atomic_link "$previous_target" /opt/inspection/current; fi
  if [[ "$phase" == app_stopping && "$mode" != --rollback && "$had_block" == 0 && "$had_compose_env" == 1 && -n "$previous_target" && -f "$previous_target/compose.yaml" ]]; then
    docker compose --env-file /etc/inspection/compose.env -f "$previous_target/compose.yaml" up -d --no-deps --wait --wait-timeout 300 "${app_services[@]}" >/dev/null 2>&1 \
      || echo "Could not resume the previous release. Inspect ops.sh status and recover manually." >&2
  fi
}
on_exit() {
  result=$?
  docker logout ghcr.io >/dev/null 2>&1 || true
  if [[ "$result" -ne 0 ]]; then
    if [[ "$phase" == preflight || ( "$phase" == app_stopping && "$mode" != --rollback ) ]]; then
      restore_pre_mutation
    else
      block_reason="Deployment failed during $phase; application services remain stopped."
      write_block
      docker compose --env-file /etc/inspection/compose.env -f "$release_dir/deploy/oci/compose.yaml" stop inspection-api inspection-worker inspection-scheduler admin dashboard capture onboarding caddy >/dev/null 2>&1 || true
      echo "Release $release was not promoted. Services remain stopped and a deployment block was recorded; do not restart until schema compatibility is verified." >&2
    fi
  fi
  rm -f "$previous_compose_env" "$previous_manifest" "$previous_block"
  exit "$result"
}
trap on_exit EXIT

INSPECTION_RELEASE_ENV="$release_env" INSPECTION_BUNDLE_DIR="$release_dir/deploy/oci" /usr/local/sbin/inspection-secrets-refresh
ghcr_user="$(cat /etc/inspection/secrets.d/GHCR_USERNAME)"
ghcr_token="$(cat /etc/inspection/secrets.d/GHCR_READ_TOKEN)"
printf '%s' "$ghcr_token" | docker login ghcr.io --username "$ghcr_user" --password-stdin >/dev/null
unset ghcr_token

dc=(docker compose --env-file /etc/inspection/compose.env -f "$release_dir/deploy/oci/compose.yaml")
"${dc[@]}" config --quiet
"${dc[@]}" pull

if [[ "$mode" == --rollback ]]; then
  [[ ! -e "$block_path" ]] || echo "Checking target image against the current database before clearing the deployment block."
  "${dc[@]}" run --rm --no-deps inspection-schema-check
  phase=app_stopping
  write_block
  if [[ -n "$previous_target" && -f "$previous_target/compose.yaml" ]]; then
    docker compose --env-file "$previous_compose_env" -f "$previous_target/compose.yaml" stop "${app_services[@]}"
  fi
  phase=rollback_starting
  "${dc[@]}" up -d --no-deps --wait --wait-timeout 300 "${app_services[@]}"
  phase=smoke
  INSPECTION_COMPOSE_FILE="$release_dir/deploy/oci/compose.yaml" /usr/local/sbin/inspection-smoke
else
  phase=app_stopping
  write_block
  if [[ -n "$previous_target" && -f "$previous_target/compose.yaml" && "$had_compose_env" == 1 ]]; then
    docker compose --env-file "$previous_compose_env" -f "$previous_target/compose.yaml" stop inspection-api inspection-worker inspection-scheduler
  fi
  phase=dependencies
  write_block
  "${dc[@]}" up -d --wait --wait-timeout 300 postgres rabbitmq dragonfly keycloak
  "${dc[@]}" run --rm --no-deps inspection-keycloak-check
  phase=migration
  write_block
  "${dc[@]}" run --rm inspection-migrate
  "${dc[@]}" run --rm --no-deps inspection-prompt-seed
  phase=application
  write_block
  "${dc[@]}" up -d --no-deps --wait --wait-timeout 300 "${app_services[@]}"
  phase=smoke
  INSPECTION_COMPOSE_FILE="$release_dir/deploy/oci/compose.yaml" /usr/local/sbin/inspection-smoke
fi

phase=promoting
write_block
if [[ "$had_manifest" == 1 ]]; then
  install -m 0600 "$previous_manifest" /etc/inspection/releases/.previous.env.new
  mv -f /etc/inspection/releases/.previous.env.new /etc/inspection/releases/previous.env
fi
install -m 0600 "$release_env" /etc/inspection/releases/.current.env.new
mv -f /etc/inspection/releases/.current.env.new /etc/inspection/releases/current.env
atomic_link "$release_dir/deploy/oci" /opt/inspection/current
rm -f "$block_path"
echo "Inspection release $release is deployed. Mode: ${mode:-deploy}."
