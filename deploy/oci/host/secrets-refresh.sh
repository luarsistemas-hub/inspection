#!/usr/bin/env bash
set -euo pipefail

config_dir=/etc/inspection
release_env="${INSPECTION_RELEASE_ENV:-$config_dir/releases/current.env}"
bundle_dir="${INSPECTION_BUNDLE_DIR:-/opt/inspection/current}"
export PATH="/opt/inspection/oci-cli/bin:$PATH"
exec 9>/run/lock/inspection-deploy.lock
flock -w 1800 9 || { echo 'Timed out waiting for the deployment lock.' >&2; exit 1; }
set -a
source "$config_dir/public.env"
set +a
temporary="$(mktemp -d /run/inspection-secrets.XXXXXX)"
cleanup() {
  rm -rf "$temporary"
  rm -f "$config_dir"/secrets.d/.*."$$" "$config_dir"/.compose.env."$$" "$config_dir"/.inspection-realm.json."$$"
}
trap cleanup EXIT
chmod 0700 "$temporary"

while read -r name ocid extra; do
  [[ -z "${name:-}" || "$name" == \#* ]] && continue
  [[ -n "${ocid:-}" && -z "${extra:-}" && "$name" =~ ^[A-Z][A-Z0-9_]*$ && "$ocid" == ocid1.vaultsecret.* ]] || { echo "Invalid secret map entry" >&2; exit 1; }
  install -d -m 0700 "$config_dir/secrets.d"
  if ! oci secrets secret-bundle get --secret-id "$ocid" --auth instance_principal --query 'data."secret-bundle-content".content' --raw-output 2>/dev/null | base64 --decode > "$temporary/$name"; then
    echo "OCI Vault secret retrieval or decoding failed" >&2
    exit 1
  fi
  python3 - "$temporary/$name" <<'PY'
from pathlib import Path
import sys

value = Path(sys.argv[1]).read_bytes()
if any(character in value for character in (b"\n", b"\r", b"\0", b"'")):
    raise SystemExit("secrets with newlines, NUL bytes or single quotes are not supported")
PY
  chmod 0600 "$temporary/$name"
done < "$config_dir/secrets.map"

[[ -s "$temporary/POSTGRES_ADMIN_PASSWORD" && -s "$temporary/KEYCLOAK_PROVISIONING_SECRET" && -s "$temporary/GHCR_READ_TOKEN" ]] || { echo "Required OCI secrets are missing" >&2; exit 1; }
for secret_file in "$temporary/"*; do
  name="$(basename "$secret_file")"
  staged="$config_dir/secrets.d/.${name}.$$"
  install -o root -g root -m 0600 "$secret_file" "$staged"
  mv -f "$staged" "$config_dir/secrets.d/$name"
done

compose_env="$config_dir/.compose.env.$$"
cat "$config_dir/public.env" "$release_env" > "$compose_env"
for secret_file in "$temporary/"*; do
  name="$(basename "$secret_file")"
  value="$(cat "$secret_file")"
  # Compose dotenv single-quoted values are literal, so no escaping is applied.
  printf "%s='%s'\n" "$name" "$value" >> "$compose_env"
  unset value
done
urlencode() { python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.stdin.read(), safe=""), end="")'; }
runtime_password="$(urlencode < "$temporary/INSPECTION_RUNTIME_PASSWORD")"
worker_password="$(urlencode < "$temporary/INSPECTION_WORKER_PASSWORD")"
admin_password="$(urlencode < "$temporary/POSTGRES_ADMIN_PASSWORD")"
rabbitmq_user="$(printf %s "$RABBITMQ_USER" | urlencode)"
rabbitmq_password="$(urlencode < "$temporary/RABBITMQ_PASSWORD")"
{
  printf "INSPECTION_RUNTIME_DATABASE_URL='postgres://inspection_runtime:%s@postgres:5432/inspection?sslmode=disable'\n" "$runtime_password"
  printf "INSPECTION_DISPATCHER_DATABASE_URL='postgres://inspection_worker:%s@postgres:5432/inspection?sslmode=disable'\n" "$worker_password"
  printf "INSPECTION_MIGRATION_DATABASE_URL='postgres://postgres:%s@postgres:5432/inspection?sslmode=disable'\n" "$admin_password"
  printf "RABBITMQ_URL_USER='%s'\nRABBITMQ_URL_PASSWORD='%s'\n" "$rabbitmq_user" "$rabbitmq_password"
} >> "$compose_env"
unset runtime_password worker_password admin_password rabbitmq_user rabbitmq_password
chmod 0600 "$compose_env"
chown root:root "$compose_env"
chmod 0600 "$compose_env"
mv -f "$compose_env" "$config_dir/compose.env"

keycloak_secret="$(cat "$config_dir/secrets.d/KEYCLOAK_PROVISIONING_SECRET")"
super_admin_password="$(cat "$config_dir/secrets.d/SUPER_ADMIN_PASSWORD")"
jq --arg admin "$ADMIN_ORIGIN" --arg dashboard "$DASHBOARD_ORIGIN" --arg subject "$SUPER_ADMIN_SUBJECT" --arg secret "$keycloak_secret" --arg admin_password "$super_admin_password" \
  '.clients[0].redirectUris=[$admin+"/auth/callback"] | .clients[0].webOrigins=[$admin] | .clients[0].attributes["post.logout.redirect.uris"]=$admin+"/overview" | .clients[1].redirectUris=[$dashboard+"/auth/callback"] | .clients[1].webOrigins=[$dashboard] | .clients[1].attributes["post.logout.redirect.uris"]=$dashboard+"/" | .clients[2].secret=$secret | .users[0].id=$subject | .users[0].credentials[0].value=$admin_password' \
  "$bundle_dir/keycloak-realm.json" > "$config_dir/.inspection-realm.json.$$"
unset keycloak_secret super_admin_password admin_password
# The Keycloak container runs as uid 1000 and must read the bind-mounted realm import.
chown 1000:1000 "$config_dir/.inspection-realm.json.$$"
chmod 0400 "$config_dir/.inspection-realm.json.$$"
mv -f "$config_dir/.inspection-realm.json.$$" "$config_dir/inspection-realm.json"
echo "OCI secrets refreshed and protected runtime configuration written."
