#!/usr/bin/env bash
set -euo pipefail

release="${1:-}"
mode="${2:-}"
[[ "$release" =~ ^[a-f0-9]{40}$ ]] || { echo "Invalid release SHA" >&2; exit 2; }
[[ -z "$mode" || "$mode" == --rollback ]] || { echo "Invalid deployment mode" >&2; exit 2; }
release_dir="/opt/inspection/releases/$release"
[[ -d "$release_dir/deploy/oci" && -s "/etc/inspection/releases/$release.env" ]] || { echo "Release bundle or manifest is missing" >&2; exit 1; }
grep -qx "RELEASE_SHA=$release" "/etc/inspection/releases/$release.env" || { echo "Release manifest SHA does not match" >&2; exit 1; }

exec 9>/run/lock/inspection-deploy.lock
flock -n 9 || { echo "Another deployment is running" >&2; exit 1; }
export PATH="/opt/inspection/oci-cli/bin:$PATH"
set -a
source /etc/inspection/public.env
set +a
previous_compose_env="$(mktemp /run/inspection-compose-before-deploy.XXXXXX)"
previous_manifest="$(mktemp /run/inspection-manifest-before-deploy.XXXXXX)"
previous_target="$(readlink /opt/inspection/current || true)"
had_compose_env=0
had_manifest=0
if [[ -f /etc/inspection/compose.env ]]; then install -m 0600 /etc/inspection/compose.env "$previous_compose_env"; had_compose_env=1; fi
if [[ -f /etc/inspection/releases/current.env ]]; then install -m 0600 /etc/inspection/releases/current.env "$previous_manifest"; had_manifest=1; fi
activated=0
migrations_started=0
on_exit() {
  result=$?
  docker logout ghcr.io >/dev/null 2>&1 || true
  if [[ "$result" -ne 0 ]]; then
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
    if [[ -n "$previous_target" ]]; then ln -sfn "$previous_target" /opt/inspection/current; fi
    if [[ "$activated" == 1 || "$migrations_started" == 1 ]]; then
      if [[ "$had_compose_env" == 1 && -n "$previous_target" && -f "$previous_target/compose.yaml" ]]; then
        # Bring the previously promoted release back instead of leaving production down.
        docker compose --env-file /etc/inspection/compose.env -f "$previous_target/compose.yaml" up -d >/dev/null 2>&1 \
          || echo "Could not restart the previous release automatically; run ops.sh status and rollback manually." >&2
      else
        docker compose --env-file /etc/inspection/compose.env -f "$release_dir/deploy/oci/compose.yaml" stop >/dev/null 2>&1 || true
      fi
    fi
    echo "Release $release was not promoted; current release pointer and manifest were preserved. Database migrations are never rolled back automatically." >&2
  fi
  rm -f "$previous_compose_env" "$previous_manifest"
  exit "$result"
}
trap on_exit EXIT
INSPECTION_RELEASE_ENV="/etc/inspection/releases/$release.env" INSPECTION_BUNDLE_DIR="$release_dir/deploy/oci" /usr/local/sbin/inspection-secrets-refresh

ghcr_user="$(cat /etc/inspection/secrets.d/GHCR_USERNAME)"
ghcr_token="$(cat /etc/inspection/secrets.d/GHCR_READ_TOKEN)"
printf '%s' "$ghcr_token" | docker login ghcr.io --username "$ghcr_user" --password-stdin >/dev/null
unset ghcr_token

dc=(docker compose --env-file /etc/inspection/compose.env -f "$release_dir/deploy/oci/compose.yaml")
"${dc[@]}" config --quiet
"${dc[@]}" pull
"${dc[@]}" up -d postgres rabbitmq dragonfly keycloak
migrations_started=1
"${dc[@]}" run --rm inspection-migrate
"${dc[@]}" run --rm --no-deps inspection-prompt-seed
activated=1
"${dc[@]}" up -d
docker logout ghcr.io >/dev/null 2>&1 || true
INSPECTION_COMPOSE_FILE="$release_dir/deploy/oci/compose.yaml" /usr/local/sbin/inspection-smoke
install -m 0600 "/etc/inspection/releases/$release.env" /etc/inspection/releases/current.env
ln -sfn "$release_dir/deploy/oci" /opt/inspection/current
echo "Inspection release $release is deployed. Mode: ${mode:-deploy}."
