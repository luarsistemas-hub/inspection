#!/usr/bin/env bash
set -euo pipefail
deployment_id="${1:-}" source_sha="${2:-}" plan_hash="${3:-}"
[[ "$deployment_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,100}$ && "$source_sha" =~ ^[a-f0-9]{40}$ && "$plan_hash" =~ ^[a-f0-9]{64}$ ]] || { echo 'Invalid supervised deployment identity.' >&2; exit 2; }
[[ "$(id -u)" == 0 ]] || { echo 'Must run as root.' >&2; exit 2; }
exec 9>/run/lock/inspection-deploy.lock
flock -w 1800 9 || { echo 'Timed out waiting for the deployment lock.' >&2; exit 1; }
job_dir="/var/lib/inspection-deploy/jobs/$deployment_id"
release_dir="/opt/inspection/releases/$source_sha"
[[ -s "$job_dir/plan.json" && -s "$release_dir/deploy/oci/host/install-host-tools.sh" ]] || { echo 'Deployment inputs are missing.' >&2; exit 1; }
actual="$(python3 - "$job_dir/plan.json" <<'PY'
import hashlib, json, sys
plan=json.load(open(sys.argv[1], encoding="utf-8")); claimed=plan.pop("planSha256", None)
actual=hashlib.sha256(json.dumps(plan,sort_keys=True,separators=(",",":")).encode()).hexdigest()
if claimed != actual: raise SystemExit("Invalid plan hash")
print(actual)
PY
)"
[[ "$actual" == "$plan_hash" ]] || { echo 'Supervised plan hash mismatch.' >&2; exit 1; }
export INSPECTION_APPROVED_PLAN_SHA256="$plan_hash"
/usr/local/sbin/inspection-apply-plan "$job_dir/plan.json" "$release_dir"
