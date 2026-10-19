#!/usr/bin/env bash
set -euo pipefail

plan_path="${1:-}"
release_dir="${2:-}"
approved_hash="${INSPECTION_APPROVED_PLAN_SHA256:-}"
[[ -s "$plan_path" && -d "$release_dir/deploy/oci" ]] || { echo 'Plan or candidate bundle is missing.' >&2; exit 2; }
[[ "$(id -u)" == 0 ]] || { echo 'Run the deployment executor with sudo.' >&2; exit 2; }
catalog_helper="$release_dir/deploy/oci/host/component-catalog.sh"
[[ -s "$catalog_helper" ]] || { echo 'Component catalog resolver is missing from the candidate release.' >&2; exit 1; }
# shellcheck source=deploy/oci/host/component-catalog.sh
source "$catalog_helper"
INSPECTION_COMPONENT_CATALOG="${INSPECTION_COMPONENT_CATALOG:-$release_dir/deploy/oci/components.json}"
inspection_resolve_component_catalog "$(readlink -f "${BASH_SOURCE[0]}")"
catalog_path="$INSPECTION_RESOLVED_COMPONENT_CATALOG"

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

operation="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["operation"])' "$plan_path")"
block_path=/etc/inspection/deployment-blocked
if [[ -e "$block_path" ]]; then
  [[ "$operation" == recovery ]] || { echo 'Deployment is blocked; only a matching recovery plan may run.' >&2; exit 1; }
  python3 - "$plan_path" "$block_path" <<'PY'
import json, sys
plan=json.load(open(sys.argv[1], encoding="utf-8"))
blocked={}
for line in open(sys.argv[2], encoding="utf-8"):
    key, sep, value=line.rstrip("\n").partition("=")
    if sep: blocked[key]=value
if plan.get("recoveryOf") != {"deploymentId":blocked.get("DEPLOYMENT_ID"), "planSha256":blocked.get("PLAN_SHA256")}:
    raise SystemExit("Recovery reference does not match the active deployment block")
PY
elif [[ "$operation" == recovery ]]; then
  echo 'Recovery requires an active deployment block.' >&2; exit 1
fi

deployment_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["deploymentId"])' "$plan_path")"
[[ "$deployment_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,100}$ ]] || { echo 'Invalid deployment ID.' >&2; exit 2; }
update_recovery_preflight_block() {
  result=$?
  trap - EXIT
  set +e
  rm -f "${targets_file:-}" "${desired_file:-}" "${stage_env:-}" "${old_env:-}" "${old_receipt:-}"
  if [[ "$result" -ne 0 && "$operation" == recovery && -s "$block_path" ]]; then
    blocked_id="$(sed -n 's/^DEPLOYMENT_ID=//p' "$block_path" | head -n 1)"
    blocked_hash="$(sed -n 's/^PLAN_SHA256=//p' "$block_path" | head -n 1)"
    printf 'DEPLOYMENT_ID=%s\nPLAN_SHA256=%s\nPHASE=preflight\nREASON=Recovery preflight failed; inspect the recovery plan and diagnostics.\n' "$blocked_id" "$blocked_hash" > "$block_path"
    chmod 0600 "$block_path"
    chown root:root "$block_path"
  fi
  exit "$result"
}
if [[ "$operation" == recovery ]]; then trap update_recovery_preflight_block EXIT; fi
targets_file="$(mktemp /run/inspection-targets.XXXXXX)"
desired_file="$(mktemp /run/inspection-images.XXXXXX)"
stage_env="$(mktemp /etc/inspection/.compose.env.plan.XXXXXX)"
old_env="$(mktemp /run/inspection-compose-env.XXXXXX)"
old_receipt="$(mktemp /run/inspection-active-state.XXXXXX)"
cleanup() { rm -f "$targets_file" "$desired_file" "$stage_env" "$old_env" "$old_receipt"; }
trap cleanup EXIT

python3 - "$plan_path" "$targets_file" "$desired_file" "$catalog_path" <<'PY'
import json, re, sys
plan = json.load(open(sys.argv[1], encoding="utf-8"))
catalog = json.load(open(sys.argv[4], encoding="utf-8"))["components"]
names = {name: item["service"] for name, item in catalog.items() if item.get("service")}
image_keys = {item["image"] for item in catalog.values()} | {"POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE", "LITELLM_IMAGE"}
if plan.get("schemaVersion") != 2:
    raise SystemExit("Plan schema v1 is unsupported; regenerate the plan with the current planner")
if plan.get("operation") not in {"deploy", "rollback", "recovery"}:
    raise SystemExit("Unsupported deployment operation")
targets = plan.get("targets")
if not isinstance(targets, list) or not targets or len(set(targets)) != len(targets):
    raise SystemExit("Invalid target list")
if set(targets) - set(names) - {"operations"}:
    raise SystemExit("Unknown deployment target")
images = plan.get("desiredImages")
if not isinstance(images, dict) or set(images) != image_keys:
    raise SystemExit("Desired composition is incomplete")
for key, value in images.items():
    if not isinstance(value, str) or not re.fullmatch(r"[^\s@]+@sha256:[a-f0-9]{64}", value):
        raise SystemExit("Every desired image must be pinned by digest")
with open(sys.argv[2], "w", encoding="ascii") as out:
    out.write("\n".join(names[name] for name in targets if name in names) + "\n")
with open(sys.argv[3], "w", encoding="ascii") as out:
    for key in sorted(image_keys): out.write(f"{key}={images[key]}\n")
PY

if [[ "$operation" == recovery ]]; then
  python3 - "$plan_path" "$release_dir/deploy/oci/components.json" <<'PY'
import json,sys
plan=json.load(open(sys.argv[1], encoding="utf-8"))
components=set(json.load(open(sys.argv[2], encoding="utf-8"))["components"])
if set(plan["targets"]) != components:
    raise SystemExit("Recovery requires all ten components")
PY
fi

# Caddy bind-mounts the Caddyfile from its release directory. Recreate it when
# the candidate changes that file so the new configuration actually takes effect.
if ! cmp -s /opt/inspection/current/Caddyfile "$release_dir/deploy/oci/Caddyfile"; then
  printf 'caddy\n' >> "$targets_file"
fi

if [[ -s /etc/inspection/compose.env ]]; then install -m 0600 /etc/inspection/compose.env "$old_env"; else : > "$old_env"; fi
if [[ -s /etc/inspection/releases/current.env ]]; then install -m 0600 /etc/inspection/releases/current.env "$old_receipt"; else : > "$old_receipt"; fi
runtime_hash="$(PYTHONPATH="$release_dir/deploy/oci" python3 - "$release_dir" <<'PY'
import pathlib, sys
from planner import runtime_config_sha256
print(runtime_config_sha256(pathlib.Path(sys.argv[1])))
PY
)"
planned_runtime_hash="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("runtimeConfigSha256", ""))' "$plan_path")"
if [[ -n "$planned_runtime_hash" && "$runtime_hash" != "$planned_runtime_hash" ]]; then
  echo 'Runtime configuration hash does not match the approved plan.' >&2; exit 1
fi
if [[ -z "$planned_runtime_hash" ]]; then
  echo 'Plan is missing runtimeConfigSha256; regenerate the plan with the current planner.' >&2; exit 1
fi
if [[ "$operation" == rollback ]]; then
  active_runtime_hash="$(sed -n 's/^RUNTIME_CONFIG_SHA256=//p' "$old_receipt" | head -n 1)"
  [[ -n "$active_runtime_hash" && "$active_runtime_hash" == "$planned_runtime_hash" ]] || {
    echo 'Automatic rollback is unavailable because the executor runtime configuration does not match the active receipt; create a corrective plan.' >&2
    exit 1
  }
fi
actual_base="$(python3 - "$old_receipt" "$catalog_path" <<'PY'
import hashlib, json, sys
catalog=json.load(open(sys.argv[2], encoding="utf-8"))["components"]
image_keys={item["image"] for item in catalog.values()} | {"POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE", "LITELLM_IMAGE"}
values = {}
for line in open(sys.argv[1], encoding="utf-8"):
    key, sep, value = line.rstrip("\n").partition("=")
    if sep and key.endswith("_IMAGE"):
        if key in values: raise SystemExit("duplicate image key in active receipt")
        values[key] = value
legacy = {"API_IMAGE", "KEYCLOAK_IMAGE", "ADMIN_IMAGE", "DASHBOARD_IMAGE", "CAPTURE_IMAGE", "ONBOARDING_IMAGE", "POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE"}
if set(values) == legacy:
    for key in ("WORKER_IMAGE", "SCHEDULER_IMAGE", "OPERATIONS_IMAGE"): values[key] = values["API_IMAGE"]
if values and set(values) not in (image_keys, image_keys-{"VIDEO_WORKER_IMAGE"}, image_keys-{"VIDEO_WORKER_IMAGE", "LITELLM_IMAGE"}): raise SystemExit("active image receipt is incomplete; synchronize state")
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
if ! grep -q '^KEYCLOAK_SMTP_SERVER_JSON=' "$stage_env"; then
  [[ -n "${SMTP_ADDRESS:-}" && -n "${SMTP_FROM:-}" && -s /etc/inspection/secrets.d/SMTP_USERNAME && -s /etc/inspection/secrets.d/SMTP_PASSWORD ]] || {
    echo 'Existing SMTP settings are required to configure Keycloak.' >&2
    exit 1
  }
  keycloak_smtp_server="$(python3 - "$SMTP_ADDRESS" "$SMTP_FROM" /etc/inspection/secrets.d/SMTP_USERNAME /etc/inspection/secrets.d/SMTP_PASSWORD <<'PY'
import json
import pathlib
import sys

address, sender, username_path, password_path = sys.argv[1:]
host, separator, port = address.rpartition(":")
if not separator or not host or not port:
    raise SystemExit("SMTP_ADDRESS must include a host and port")
print(json.dumps({
    "host": host,
    "port": port,
    "from": sender,
    "auth": "true",
    "user": pathlib.Path(username_path).read_text(),
    "password": pathlib.Path(password_path).read_text(),
    "ssl": "false",
    "starttls": "true",
}, separators=(",", ":")))
PY
)"
  printf "KEYCLOAK_SMTP_SERVER_JSON='%s'\n" "$keycloak_smtp_server" >> "$stage_env"
  unset keycloak_smtp_server
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
  key="$(printf '%s_IMAGE' "$component" | tr '[:lower:]-' '[:upper:]_')"
  value="$(sed -n "s/^${key}=//p" "$desired_file")"
  [[ -n "$value" ]] && changed_images+=("$value")
done < <(python3 -c 'import json,sys; print("\n".join(json.load(open(sys.argv[1]))["targets"]))' "$plan_path")
go_selected=0
for component in api worker scheduler video-worker operations; do
  if python3 - "$plan_path" "$component" <<'PY'
import json,sys
raise SystemExit(0 if sys.argv[2] in json.load(open(sys.argv[1]))["targets"] else 1)
PY
  then go_selected=1; fi
done
if [[ "$operation" == recovery ]]; then go_selected=1; fi
if [[ "$operation" == rollback && "$go_selected" == 1 && -s "$old_receipt" && "$(grep -c '^VIDEO_WORKER_IMAGE=' "$old_receipt")" -gt 0 && "$(python3 -c 'import json,sys; print(str(json.load(open(sys.argv[1])).get("rollbackTargetHasVideoWorker", False)).lower())' "$plan_path")" != true ]]; then
  echo 'Rollback target has no video-worker image; refusing to leave accepted video work without a worker.' >&2; exit 1
fi
migration=0
if [[ "$operation" != rollback && "$go_selected" == 1 ]]; then migration=1; fi
if [[ "$migration" == 1 || "$operation" == rollback ]]; then
  changed_images+=("$(sed -n 's/^OPERATIONS_IMAGE=//p' "$desired_file")")
fi
if [[ "$gateway_selected" == 1 ]]; then
  gateway_image="$(sed -n 's/^LITELLM_IMAGE=//p' "$desired_file")"
  old_gateway_image="$(sed -n 's/^LITELLM_IMAGE=//p' "$old_env")"
  [[ "$gateway_image" == "$old_gateway_image" ]] || changed_images+=("$gateway_image")
fi
if [[ ! -s "$old_env" ]]; then
  for key in POSTGRES_IMAGE RABBITMQ_IMAGE DRAGONFLY_IMAGE KEYCLOAK_IMAGE LITELLM_IMAGE; do
    changed_images+=("$(sed -n "s/^${key}=//p" "$desired_file")")
  done
fi
block() {
  local reason="$1"
  local blocked_id="$deployment_id" blocked_hash="$plan_hash"
  if [[ "$operation" == recovery && -s "$block_path" ]]; then
    blocked_id="$(sed -n 's/^DEPLOYMENT_ID=//p' "$block_path" | head -n 1)"
    blocked_hash="$(sed -n 's/^PLAN_SHA256=//p' "$block_path" | head -n 1)"
  fi
  reason="${reason//$'\n'/ }"
  printf 'DEPLOYMENT_ID=%s\nPLAN_SHA256=%s\nPHASE=%s\nREASON=%s\n' "$blocked_id" "$blocked_hash" "$phase" "$reason" > "$block_path"
  chmod 0600 "$block_path"; chown root:root "$block_path"
}
on_exit() {
  result=$?
  trap - EXIT
  set +e
  docker logout ghcr.io >/dev/null 2>&1 || true
  if [[ "$result" -ne 0 && ( "$phase" != preflight || "$operation" == recovery ) ]]; then
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
    if [[ "$operation" == recovery ]]; then
      if [[ "$migration_started" == 0 && "$phase" != promoting && -s "$old_env" && "${#compose_services[@]}" -gt 0 ]]; then
        old_services="$(docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml config --services 2>/dev/null)"
        recover_services=()
        for service in "${compose_services[@]}"; do grep -Fxq "$service" <<< "$old_services" && recover_services+=("$service"); done
        if [[ "${#recover_services[@]}" -gt 0 ]] && docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml up -d --no-deps --wait --wait-timeout 300 "${recover_services[@]}" >/dev/null 2>&1; then
          block "Recovery failed before migrations; previous services were restored; inspect diagnostics."
        else
          block "Recovery failed before migrations and previous services could not be restored; inspect diagnostics."
        fi
      else
        block "Recovery failed during $phase; original deployment reference preserved."
      fi
    elif [[ "$migration_started" == 1 ]]; then
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
      old_services="$(docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml config --services 2>/dev/null)"
      recover_services=()
      for service in "${compose_services[@]}"; do grep -Fxq "$service" <<< "$old_services" && recover_services+=("$service"); done
      if [[ "${#recover_services[@]}" -gt 0 ]]; then
        docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml up -d --no-deps --wait --wait-timeout 300 "${recover_services[@]}" >/dev/null 2>&1 || services_recovered=0
      fi
      if [[ "$gateway_recovered" == 1 && "$services_recovered" == 1 ]]; then
        rm -f "$block_path"
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
  changed_images+=("$(sed -n 's/^CADDY_IMAGE=//p' "$desired_file")")
  "${dc[@]}" run --rm --no-deps caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
fi
ghcr_user="$(cat /etc/inspection/secrets.d/GHCR_USERNAME)"
ghcr_token="$(cat /etc/inspection/secrets.d/GHCR_READ_TOKEN)"
printf '%s' "$ghcr_token" | docker login ghcr.io --username "$ghcr_user" --password-stdin >/dev/null
unset ghcr_token
for image in "${changed_images[@]}"; do docker pull "$image"; done
video_image="$(sed -n 's/^VIDEO_WORKER_IMAGE=//p' "$desired_file")"
if [[ -n "$video_image" ]]; then
  docker image inspect "$video_image" >/dev/null 2>&1 || docker pull "$video_image"
  prepare_upgrade="$release_dir/deploy/oci/host/prepare-upgrade.sh"
  [[ -s "$prepare_upgrade" ]] || { echo 'Video host upgrade preparation script is missing from the candidate release.' >&2; exit 1; }
  bash "$prepare_upgrade" "$video_image"
fi

if [[ -s "$old_env" ]]; then
  "${dc[@]}" up -d --no-recreate --wait --wait-timeout 300 postgres rabbitmq dragonfly keycloak litellm
elif [[ "$go_selected" == 1 ]]; then
  phase=dependencies
  "${dc[@]}" up -d --wait --wait-timeout 300 postgres rabbitmq dragonfly keycloak litellm
fi

if [[ "$operation" == rollback ]]; then
  "${dc[@]}" run --rm --no-deps inspection-schema-check || {
    echo 'target images do not support the current database schema; this upgrade is one-way, recover by deploying forward' >&2
    exit 1
  }
fi

if [[ "$gateway_selected" == 1 ]]; then
  phase=services
  block 'LiteLLM gateway is updating.'
  "${dc[@]}" up -d --no-deps --wait --wait-timeout 300 litellm
fi

if [[ "$migration" == 1 ]]; then
  if [[ "${#compose_services[@]}" -gt 0 ]]; then
    phase=services
    block 'Stopping the coordinated Go services before database migration.'
    if [[ -s "$old_env" ]]; then
      old_services="$(docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml config --services)"
      stop_services=()
      for service in "${compose_services[@]}"; do
        grep -Fxq "$service" <<< "$old_services" && stop_services+=("$service")
      done
      if [[ "${#stop_services[@]}" -gt 0 ]]; then
        docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml stop "${stop_services[@]}"
        services_stopped=1
      fi
    fi
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
    old_services="$(docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml config --services)"
    stop_services=()
    for service in "${compose_services[@]}"; do grep -Fxq "$service" <<< "$old_services" && stop_services+=("$service"); done
    if [[ "${#stop_services[@]}" -gt 0 ]]; then docker compose --env-file "$old_env" -f /opt/inspection/current/compose.yaml stop "${stop_services[@]}"; fi
  fi
  "${dc[@]}" up -d --no-deps --wait --wait-timeout 300 "${compose_services[@]}"
  phase=smoke
  if [[ "$operation" == recovery || "$(python3 -c 'import json,sys; print("keycloak" in json.load(open(sys.argv[1]))["targets"])' "$plan_path")" == True ]]; then
    "${dc[@]}" run --rm --no-deps inspection-keycloak-check
    "${dc[@]}" run --rm --no-deps inspection-keycloak-configure
  fi
  INSPECTION_COMPONENT_CATALOG="$catalog_path" INSPECTION_COMPOSE_FILE="$release_dir/deploy/oci/compose.yaml" INSPECTION_COMPOSE_ENV="$stage_env" bash "$release_dir/deploy/oci/host/smoke.sh"
fi

for service in "${compose_services[@]}"; do
  container_id="$("${dc[@]}" ps -q "$service")"
  [[ -n "$container_id" ]] || { echo "Updated service has no running container: $service" >&2; exit 1; }
  image_id="$(docker inspect --format '{{.Image}}' "$container_id")"
  repo_digests="$(docker image inspect --format '{{join .RepoDigests "\n"}}' "$image_id")"
  approved_image="$(python3 - "$catalog_path" "$plan_path" "$service" <<'PY'
import json,sys
catalog=json.load(open(sys.argv[1], encoding="utf-8"))["components"]
plan=json.load(open(sys.argv[2], encoding="utf-8"))
name=next((name for name,item in catalog.items() if item.get("service")==sys.argv[3]), None)
key=catalog[name]["image"] if name else ("CADDY_IMAGE" if sys.argv[3] == "caddy" else None)
if key is None: raise SystemExit("service image is missing from the approved catalog")
print(plan["desiredImages"][key])
PY
)"
  grep -Fxq "$approved_image" <<< "$repo_digests" || { echo "Container digest differs from the approved image for $service." >&2; exit 1; }
done

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
    out.write(f"RUNTIME_CONFIG_SHA256={plan.get('runtimeConfigSha256', '')}\n")
    for key, value in sorted(plan["desiredImages"].items()): out.write(f"{key}={value}\n")
    for name, value in sorted(plan.get("componentSources", {}).items()): out.write(f"COMPONENT_SOURCE_{name.upper().replace('-', '_')}={value}\n")
os.chmod(sys.argv[2], 0o600)
PY
if [[ -s /etc/inspection/releases/current.env ]]; then install -m 0600 /etc/inspection/releases/current.env /etc/inspection/releases/previous.env; fi
mv -f "$receipt" /etc/inspection/releases/current.env
current_link=/opt/inspection/current
temporary_link="${current_link}.new.$$"
ln -s "$release_dir/deploy/oci" "$temporary_link"
mv -Tf "$temporary_link" "$current_link"
metrics_temporary="/usr/local/sbin/.inspection-publish-metrics.new.$$"
install -o root -g root -m 0755 "$release_dir/deploy/oci/host/publish-metrics.py" "$metrics_temporary"
mv -f "$metrics_temporary" /usr/local/sbin/inspection-publish-metrics
rm -f "$block_path"
echo "Deployment $deployment_id applied; plan $plan_hash."
