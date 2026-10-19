#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -u)" == 0 ]] || { echo 'Must run as root.' >&2; exit 2; }
kind="${1:-current}"
[[ "$kind" == current || "$kind" == previous || "$kind" == blocked ]] || { echo 'Choose current, previous, or blocked.' >&2; exit 2; }
exec 9>/run/lock/inspection-deploy.lock
flock -s 9
block=/etc/inspection/deployment-blocked
if [[ "$kind" == blocked ]]; then
  [[ -s "$block" ]] || { echo 'No active deployment block is available.' >&2; exit 1; }
else
  [[ ! -e "$block" ]] || { echo 'Deployment is blocked; refusing to publish stale state.' >&2; exit 1; }
fi
receipt="/etc/inspection/releases/${kind/blocked/current}.env"
[[ -s "$receipt" ]] || { echo "No $kind deployment receipt is available." >&2; exit 1; }
script_path="$(readlink -f "${BASH_SOURCE[0]}")"
catalog="$(dirname "$script_path")/components.json"
python3 - "$receipt" "$catalog" "$kind" "$block" <<'PY'
import json, re, sys
kind=sys.argv[3]
try:
    components=json.load(open(sys.argv[2], encoding="utf-8"))["components"]
except (OSError, ValueError, KeyError, TypeError) as exc:
    raise SystemExit(f"invalid or missing component catalog: {exc}")
if not isinstance(components, dict) or not components or any(not isinstance(item, dict) or not isinstance(item.get("image"), str) or not isinstance(item.get("service"), (str, type(None))) for item in components.values()):
    raise SystemExit("component catalog must define image and service for every component")
images={item["image"] for item in components.values()} | {"POSTGRES_IMAGE","DRAGONFLY_IMAGE","RABBITMQ_IMAGE","CADDY_IMAGE","LITELLM_IMAGE"}
values={}
for line in open(sys.argv[1], encoding="utf-8"):
    key, sep, value=line.rstrip("\n").partition("=")
    if sep and key in images:
        if key in values: raise SystemExit("duplicate image in deployment receipt")
        if not re.fullmatch(r"[^\s@]+@sha256:[a-f0-9]{64}", value): raise SystemExit("invalid pinned image in deployment receipt")
        values[key]=value
legacy={"API_IMAGE","KEYCLOAK_IMAGE","ADMIN_IMAGE","DASHBOARD_IMAGE","CAPTURE_IMAGE","ONBOARDING_IMAGE","POSTGRES_IMAGE","DRAGONFLY_IMAGE","RABBITMQ_IMAGE","CADDY_IMAGE"}
if set(values)==legacy:
    for name in ("WORKER_IMAGE","SCHEDULER_IMAGE","OPERATIONS_IMAGE"): values[name]=values["API_IMAGE"]
if set(values) not in (images, images-{"VIDEO_WORKER_IMAGE"}, images-{"LITELLM_IMAGE","VIDEO_WORKER_IMAGE"}): raise SystemExit("deployment receipt is incomplete; synchronize manually")
sources={}
for line in open(sys.argv[1], encoding="utf-8"):
    key, sep, value=line.rstrip("\n").partition("=")
    if sep and key.startswith("COMPONENT_SOURCE_"):
        name=key.removeprefix("COMPONENT_SOURCE_").lower().replace("_", "-")
        if name not in components or not re.fullmatch(r"[a-f0-9]{40}", value): raise SystemExit("invalid component source in deployment receipt")
        if name in sources: raise SystemExit("duplicate component source in deployment receipt")
        sources[name]=value
if sources and set(sources) != set(components): raise SystemExit("component source receipt is incomplete")
for name in ("DEPLOYMENT_ID","PLAN_SHA256","SOURCE_SHA"):
    with open(sys.argv[1], encoding="utf-8") as receipt:
        value=next((line.partition("=")[2].strip() for line in receipt if line.startswith(name+"=")), "")
    if value: print(f"{name}={value}")
with open(sys.argv[1], encoding="utf-8") as receipt:
    runtime=next((line.partition("=")[2].strip() for line in receipt if line.startswith("RUNTIME_CONFIG_SHA256=")), "")
if runtime: print(f"RUNTIME_CONFIG_SHA256={runtime}")
for key in sorted(values): print(f"{key}={values[key]}")
for name in sorted(sources): print(f"COMPONENT_SOURCE_{name.upper().replace('-', '_')}={sources[name]}")
if kind == "blocked":
    values={}
    for line in open(sys.argv[4], encoding="utf-8"):
        key, sep, value=line.rstrip("\n").partition("=")
        if sep: values[key]=value
    print(f"BLOCKED_DEPLOYMENT_ID={values.get('DEPLOYMENT_ID', '')}")
    print(f"BLOCKED_PLAN_SHA256={values.get('PLAN_SHA256', '')}")
    print(f"BLOCKED_PHASE={values.get('PHASE', '').replace(chr(10), ' ').replace(chr(13), ' ')}")
PY
