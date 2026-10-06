#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -u)" == 0 ]] || { echo 'Must run as root.' >&2; exit 2; }
if [[ -n "${1:-}" ]]; then
  deployment_id="$1"
  [[ "$deployment_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,100}$ ]] || { echo 'Invalid deployment ID.' >&2; exit 2; }
  unit="inspection-deployment-$deployment_id.service"
  properties="$(systemctl show "$unit" --no-pager -p LoadState -p ActiveState -p SubState -p Result -p ExecMainStatus -p ExecMainStartTimestamp -p ExecMainExitTimestamp 2>/dev/null || true)"
  [[ -n "$properties" ]] || { echo '{"status":"unknown"}'; exit 0; }
  python3 - "$deployment_id" "$properties" <<'PY'
import json, sys
values=dict(line.split("=",1) for line in sys.argv[2].splitlines() if "=" in line)
status="unknown"
if values.get("LoadState")=="loaded":
    active, sub = values.get("ActiveState"), values.get("SubState")
    if active in ("activating", "deactivating") or (active=="active" and sub=="running"):
        status="running"
    elif active=="failed" or values.get("Result") not in (None,"success",""):
        status="failed"
    elif active=="active" and sub=="exited" and values.get("Result")=="success" and values.get("ExecMainStatus")=="0" and values.get("ExecMainExitTimestamp"):
        status="succeeded"
print(json.dumps({"deploymentId":sys.argv[1],"status":status,"result":values.get("Result"),"exitCode":values.get("ExecMainStatus"),"startedAt":values.get("ExecMainStartTimestamp")},sort_keys=True))
PY
  exit 0
fi
exec 9>/run/lock/inspection-deploy.lock
flock -s 9
python3 - <<'PY'
import json, pathlib
path=pathlib.Path("/etc/inspection/releases/current.env")
values={}
if path.is_file():
    for line in path.read_text().splitlines():
        key, sep, value=line.partition("=")
        if sep and key in {"DEPLOYMENT_ID","PLAN_SHA256","SOURCE_SHA"}: values[key]=value
blocked=pathlib.Path("/etc/inspection/deployment-blocked")
values["blocked"]=blocked.read_text().strip() if blocked.is_file() else None
print(json.dumps(values, sort_keys=True))
PY
docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml ps --format json | python3 -c 'import json,sys; print(json.dumps([json.loads(line) for line in sys.stdin if line.strip()], sort_keys=True))'
