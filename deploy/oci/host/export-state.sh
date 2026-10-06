#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -u)" == 0 ]] || { echo 'Must run as root.' >&2; exit 2; }
kind="${1:-current}"
[[ "$kind" == current || "$kind" == previous ]] || { echo 'Choose current or previous.' >&2; exit 2; }
exec 9>/run/lock/inspection-deploy.lock
flock -s 9
[[ ! -e /etc/inspection/deployment-blocked ]] || { echo 'Deployment is blocked; refusing to publish stale state.' >&2; exit 1; }
receipt="/etc/inspection/releases/$kind.env"
[[ -s "$receipt" ]] || { echo "No $kind deployment receipt is available." >&2; exit 1; }
python3 - "$receipt" <<'PY'
import re, sys
images={"API_IMAGE","WORKER_IMAGE","SCHEDULER_IMAGE","OPERATIONS_IMAGE","ADMIN_IMAGE","DASHBOARD_IMAGE","CAPTURE_IMAGE","ONBOARDING_IMAGE","KEYCLOAK_IMAGE","POSTGRES_IMAGE","DRAGONFLY_IMAGE","RABBITMQ_IMAGE","CADDY_IMAGE","LITELLM_IMAGE"}
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
if set(values) not in (images, images-{"LITELLM_IMAGE"}): raise SystemExit("deployment receipt is incomplete; synchronize manually")
for name in ("DEPLOYMENT_ID","PLAN_SHA256","SOURCE_SHA"):
    with open(sys.argv[1], encoding="utf-8") as receipt:
        value=next((line.partition("=")[2].strip() for line in receipt if line.startswith(name+"=")), "")
    if value: print(f"{name}={value}")
for key in sorted(values): print(f"{key}={values[key]}")
PY
