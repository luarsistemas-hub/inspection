#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:-}"
release_dir="${2:-}"
approved_hash="${INSPECTION_APPROVED_PLAN_SHA256:-}"
[[ -s "$plan_path" && -d "$release_dir/deploy/oci" ]] || { echo 'Plan or candidate bundle is missing.' >&2; exit 2; }
[[ "$(id -u)" == 0 ]] || { echo 'Run the deployment executor with sudo.' >&2; exit 2; }

plan_hash="$(python3 - "$plan_path" <<'PY'
import hashlib, json, sys
plan = json.load(open(sys.argv[1], encoding="utf-8"))
claimed = plan.pop("planSha256", None)
actual = hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
if claimed != actual:
    raise SystemExit("Plan digest is invalid")
print(actual)
PY
)"
[[ "$approved_hash" == "$plan_hash" ]] || { echo 'Approved plan hash does not match the reviewed plan.' >&2; exit 1; }

deployment_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["deploymentId"])' "$plan_path")"
[[ "$deployment_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,100}$ ]] || { echo 'Invalid deployment ID.' >&2; exit 2; }
targets_file="$(mktemp /run/inspection-targets.XXXXXX)"
desired_file="$(mktemp /run/inspection-images.XXXXXX)"
stage_env="$(mktemp /etc/inspection/.compose.env.plan.XXXXXX)"
old_env="$(mktemp /run/inspection-compose-env.XXXXXX)"
old_receipt="$(mktemp /run/inspection-active-state.XXXXXX)"
old_block="$(mktemp /run/inspection-block.XXXXXX)"
cleanup() { rm -f "$targets_file" "$desired_file" "$stage_env" "$old_env" "$old_receipt" "$old_block"; }
trap cleanup EXIT

python3 - "$plan_path" "$targets_file" "$desired_file" <<'PY'
import json, re, sys
plan = json.load(open(sys.argv[1], encoding="utf-8"))
names = {"api":"inspection-api", "worker":"inspection-worker", "scheduler":"inspection-scheduler", "admin":"admin", "dashboard":"dashboard", "capture":"capture", "onboarding":"onboarding", "keycloak":"keycloak"}
targets = plan.get("targets")
if not isinstance(targets, list) or not targets or len(set(targets)) != len(targets):
    raise SystemExit("Invalid target list")
if set(targets) - set(names) - {"operations"}:
    raise SystemExit("Unknown deployment target")
images = plan.get("desiredImages")
keys = {name.upper() + "_IMAGE" for name in ("api", "worker", "scheduler", "operations", "admin", "dashboard", "capture", "onboarding", "keycloak")}
keys.update({"POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE", "LITELLM_IMAGE"})
if not isinstance(images, dict) or set(images) != keys:
    raise SystemExit("Desired composition is incomplete")
for key, value in images.items():
    if not isinstance(value, str) or not re.fullmatch(r"[^\s@]+@sha256:[a-f0-9]{64}", value):
        raise SystemExit("Every desired image must be pinned by digest")
with open(sys.argv[2], "w", encoding="ascii") as out:
    out.write("\n".join(names[name] for name in targets if name in names) + "\n")
with open(sys.argv[3], "w", encoding="ascii") as out:
    for key in sorted(keys): out.write(f"{key}={images[key]}\n")
PY

# Caddy bind-mounts the Caddyfile from its release directory. Recreate it when
# the candidate changes that file so the new configuration actually takes effect.
if ! cmp -s /opt/inspection/current/Caddyfile "$release_dir/deploy/oci/Caddyfile"; then
  printf 'caddy\n' >> "$targets_file"
fi

if [[ -s /etc/inspection/compose.env ]]; then install -m 0600 /etc/inspection/compose.env "$old_env"; else : > "$old_env"; fi
if [[ -s /etc/inspection/releases/current.env ]]; then install -m 0600 /etc/inspection/releases/current.env "$old_receipt"; else : > "$old_receipt"; fi
actual_base="$(python3 - "$old_receipt" <<'PY'
import hashlib, json, sys
values = {}
for line in open(sys.argv[1], encoding="utf-8"):
    key, sep, value = line.rstrip("\n").partition("=")
    if sep and key.endswith("_IMAGE"):
        if key in values: raise SystemExit("duplicate image key in active receipt")
        values[key] = value
legacy = {"API_IMAGE", "KEYCLOAK_IMAGE", "ADMIN_IMAGE", "DASHBOARD_IMAGE", "CAPTURE_IMAGE", "ONBOARDING_IMAGE", "POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE"}
if set(values) == legacy:
    for key in ("WORKER_IMAGE", "SCHEDULER_IMAGE", "OPERATIONS_IMAGE"): values[key] = values["API_IMAGE"]
if values and len(values) not in (13, 14): raise SystemExit("active image receipt is incomplete; synchronize state")
print(hashlib.sha256(json.dumps(values, sort_keys=True, separators=(",", ":")).encode()).hexdigest())
PY
)"
expected_base="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["expectedActiveStateSha256"])' "$plan_path")"
[[ "$actual_base" == "$expected_base" ]] || { echo 'Active host state changed since planning; synchronize and create a new plan.' >&2; exit 1; }

export PATH="/opt/inspection/oci-cli/bin:$PATH"
set -a
# shellcheck source=/dev/null
source /etc/inspection/public.env
set +a
python3 - "$plan_path" <<'PY'
import json, os, sys
plan=json.load(open(sys.argv[1], encoding="utf-8"))
keys={
    "admin":{"API_ORIGIN":"API_ORIGIN","AUTH_ORIGIN":"AUTH_ORIGIN","DASHBOARD_ORIGIN":"DASHBOARD_ORIGIN","CAPTURE_ORIGIN":"CAPTURE_ORIGIN"},
    "dashboard":{"API_ORIGIN":"API_ORIGIN","AUTH_ORIGIN":"AUTH_ORIGIN","ADMIN_ORIGIN":"ADMIN_ORIGIN","STORAGE_ORIGIN":"STORAGE_ORIGIN"},
    "capture":{"API_ORIGIN":"API_ORIGIN","STORAGE_ORIGIN":"STORAGE_ORIGIN"},
    "onboarding":{"API_ORIGIN":"API_ORIGIN","TURNSTILE_SITE_KEY":"TURNSTILE_SITE_KEY"},
}
for component, profile in plan.get("publicProfiles", {}).items():
    if component not in keys or set(profile) != set(keys[component]):
        raise SystemExit("invalid public build profile in deployment plan")
    for profile_key, env_key in keys[component].items():
        if profile[profile_key] != os.environ.get(env_key):
            raise SystemExit(f"{component} build profile does not match host configuration: {profile_key}")
PY
if [[ -s "$old_env" ]]; then cp "$old_env" "$stage_env"; fi
python3 - "$stage_env" "$desired_file" <<'PY'
import os, sys
path, images_path = sys.argv[1:]
values = {}
for line in open(path, encoding="utf-8"):
    key, sep, value = line.rstrip("\n").partition("=")
    if sep: values[key] = value
for line in open(images_path, encoding="ascii"):
    key, value = line.rstrip("\n").split("=", 1)
    values[key] = value
with open(path, "w", encoding="utf-8") as out:
    for key in sorted(values): out.write(f"{key}={values[key]}\n")
os.chmod(path, 0o600)
PY

block_path=/etc/inspection/deployment-blocked
had_block=0
if [[ -e "$block_path" ]]; then
  install -m 0600 "$block_path" "$old_block"
  had_block=1
fi
phase=preflight
migration_started=0
services_stopped=0
dc=(docker compose --env-file "$stage_env" -f "$release_dir/deploy/oci/compose.yaml")
compose_services=()
while IFS= read -r service; do [[ -n "$service" ]] && compose_services+=("$service"); done < "$targets_file"
gateway_selected=0
if grep -qx inspection-worker "$targets_file"; then gateway_selected=1; fi
changed_images=()
while IFS= read -r component; do
  [[ -n "$component" ]] || continue
  key="$(printf '%s_IMAGE' "$component" | tr '[:lower:]' '[:upper:]')"
  value="$(sed -n "s/^${key}=//p" "$desired_file")"
  [[ -n "$value" ]] && changed_images+=("$value")
done < <(python3 -c 'import json,sys; print("\n".join(json.load(open(sys.argv[1]))["targets"]))' "$plan_path")
if [[ "$(python3 -c 'import json,sys; print(str(json.load(open(sys.argv[1]))["operations"]["runMigration"]).lower())' "$plan_path")" == true ]]; then
  changed_images+=("$(sed -n 's/^OPERATIONS_IMAGE=//p' "$desired_file")")
fi
if [[ "$gateway_selected" == 1 ]]; then
  gateway_image="$(sed -n 's/^LITELLM_IMAGE=//p' "$desired_file")"
  old_gateway_image="$(sed -n 's/^LITELLM_IMAGE=//p' "$old_env")"
  [[ "$gateway_image" == "$old_gateway_image" ]] || changed_images+=("$gateway_image")
fi
block() {
  local reason="$1"
  printf 'DEPLOYMENT_ID=%s\nPLAN_SHA256=%s\nPHASE=%s\nREASON=%s\n' "$deployment_id" "$plan_hash" "$phase" "$reason" > "$block_path"
  chmod 0600 "$block_path"; chown root:root "$block_path"
}
on_exit() {
  result=$?
  trap - EXIT
  set +e
  docker logout ghcr.io >/dev/null 2>&1 || true
  if [[ "$result" -ne 0 && "$phase" != preflight ]]; then
    # Recovery recreates containers, deleting their logs. Retain them privately first.
    diagnostics="/var/lib/inspection-deploy/jobs/$deployment_id/diagnostics"
    if install -d -o root -g root -m 0700 "$diagnostics"; then
      (
        umask 077
        "${dc[@]}" ps -a --format json > "$diagnostics/containers.jsonl"
        if [[ "${#compose_services[@]}" -gt 0 ]]; then
          if [[ "$gateway_selected" == 1 ]]; then
            "${dc[@]}" logs --no-color --tail 100 litellm "${compose_services[@]}" > "$diagnostics/containers.log" 2>&1
          else
            "${dc[@]}" logs --no-color --tail 100 "${compose_services[@]}" > "$diagnostics/containers.log" 2>&1
          fi
        fi
      )
      echo "Deployment diagnostics: $diagnostics (root only)." >&2
    fi
    if [[ "$migration_started" == 1 ]]; then
      block "Deployment failed after database migration started; inspect before recovery."
    elif [[ "$phase" == promoting ]]; then
      block 'Deployment failed while promoting state; reconcile receipts and services before recovery.'
    elif [[ -s "$old_env" && "${#compose_services[@]}" -gt 0 ]]; then
      gateway_recovered=1
      if [[ "$gateway_selected" == 1 ]]; then
        if [[ -n "$old_gateway_image" ]]; then
          docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml up -d --no-deps --wait --wait-timeout 300 litellm >/dev/null 2>&1 || gateway_recovered=0
        else
          "${dc[@]}" stop litellm >/dev/null 2>&1 || true
        fi
      fi
      services_recovered=1
      docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml up -d --no-deps --wait --wait-timeout 300 "${compose_services[@]}" >/dev/null 2>&1 || services_recovered=0
      if [[ "$gateway_recovered" == 1 && "$services_recovered" == 1 ]]; then
        if [[ "$had_block" == 1 ]]; then
          install -o root -g root -m 0600 "$old_block" "$block_path" || block 'Failed to restore the previous deployment block.'
        else
          rm -f "$block_path"
        fi
        echo 'Previous services recovered; this deployment still failed.' >&2
      else
        block 'Failed to recover selected services after deployment failure.'
      fi
    else
      block 'Deployment failed without a previous composition to recover.'
    fi
  fi
  cleanup
  exit "$result"
}
trap on_exit EXIT

"${dc[@]}" config --quiet
if grep -qx caddy "$targets_file"; then
  "${dc[@]}" run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
fi
ghcr_user="$(cat /etc/inspection/secrets.d/GHCR_USERNAME)"
ghcr_token="$(cat /etc/inspection/secrets.d/GHCR_READ_TOKEN)"
printf '%s' "$ghcr_token" | docker login ghcr.io --username "$ghcr_user" --password-stdin >/dev/null
unset ghcr_token
for image in "${changed_images[@]}"; do docker pull "$image"; done

if [[ "$gateway_selected" == 1 ]]; then
  phase=services
  block 'LiteLLM gateway is updating.'
  "${dc[@]}" up -d --no-deps --wait --wait-timeout 300 litellm
fi

migration="$(python3 -c 'import json,sys; print(str(json.load(open(sys.argv[1]))["operations"]["runMigration"]).lower())' "$plan_path")"
if [[ "$migration" == true ]]; then
  if [[ "${#compose_services[@]}" -gt 0 ]]; then
    phase=services
    block 'Stopping the coordinated Go services before database migration.'
    if [[ -s "$old_env" ]]; then
      docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml stop "${compose_services[@]}"
    fi
    services_stopped=1
  fi
  phase=migration
  block 'Coordinated migration is running.'
  migration_started=1
  "${dc[@]}" run --rm --no-deps inspection-migrate
  "${dc[@]}" run --rm --no-deps inspection-prompt-seed
  "${dc[@]}" run --rm --no-deps inspection-schema-check
fi
if [[ "${#compose_services[@]}" -gt 0 ]]; then
  phase=services
  [[ "$migration_started" == 1 ]] || block 'Selected services are updating.'
  if [[ "$services_stopped" == 0 && -s "$old_env" ]]; then
    docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml stop "${compose_services[@]}"
  fi
  "${dc[@]}" up -d --no-deps --wait --wait-timeout 300 "${compose_services[@]}"
  if [[ "$(python3 -c 'import json,sys; print("keycloak" in json.load(open(sys.argv[1]))["targets"])' "$plan_path")" == True ]]; then
    "${dc[@]}" run --rm --no-deps inspection-keycloak-check
    "${dc[@]}" run --rm --no-deps inspection-keycloak-configure
  fi
  phase=smoke
  INSPECTION_COMPOSE_FILE="$release_dir/deploy/oci/compose.yaml" INSPECTION_COMPOSE_ENV="$stage_env" /usr/local/sbin/inspection-smoke
fi

phase=promoting
mkdir -p /etc/inspection/releases
install -o root -g root -m 0600 "$stage_env" /etc/inspection/.compose.env.new
mv -f /etc/inspection/.compose.env.new /etc/inspection/compose.env
receipt="/etc/inspection/releases/.current.env.new"
python3 - "$plan_path" "$receipt" <<'PY'
import json, os, sys
plan=json.load(open(sys.argv[1], encoding="utf-8"))
with open(sys.argv[2], "w", encoding="utf-8") as out:
    out.write(f"DEPLOYMENT_ID={plan['deploymentId']}\nPLAN_SHA256={plan['planSha256']}\nSOURCE_SHA={plan.get('sourceSha', '')}\n")
    for key, value in sorted(plan["desiredImages"].items()): out.write(f"{key}={value}\n")
os.chmod(sys.argv[2], 0o600)
PY
if [[ -s /etc/inspection/releases/current.env ]]; then install -m 0600 /etc/inspection/releases/current.env /etc/inspection/releases/previous.env; fi
mv -f "$receipt" /etc/inspection/releases/current.env
current_link=/opt/inspection/current
temporary_link="${current_link}.new.$$"
ln -s "$release_dir/deploy/oci" "$temporary_link"
mv -Tf "$temporary_link" "$current_link"
rm -f "$block_path"
echo "Deployment $deployment_id applied; plan $plan_hash."
