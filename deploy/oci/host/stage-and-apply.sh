#!/usr/bin/env bash
set -euo pipefail

deployment_id="${1:-}"
approved_plan_sha="${2:-}"
[[ "$deployment_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,100}$ ]] || { echo 'Invalid deployment ID.' >&2; exit 2; }
[[ "$approved_plan_sha" =~ ^[a-f0-9]{64}$ ]] || { echo 'Invalid approved plan hash.' >&2; exit 2; }
[[ "$(id -u)" == 0 ]] || { echo 'Must run as root.' >&2; exit 2; }

stage="$(mktemp -d "/run/inspection-stage-$deployment_id.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
outer="$stage/upload.tgz"
cat > "$outer"

python3 - "$outer" "$stage" "$deployment_id" "$approved_plan_sha" <<'PY'
import hashlib, json, pathlib, re, sys, tarfile
archive_path, root, expected_id, expected_hash = sys.argv[1:]
root = pathlib.Path(root)
def copy_archive(archive, destination, allowed_prefixes):
    seen=set()
    for member in archive.getmembers():
        path=pathlib.PurePosixPath(member.name)
        if path.is_absolute() or ".." in path.parts or member.name in seen or not (member.isfile() or member.isdir()):
            raise SystemExit("unsafe or duplicate archive member")
        seen.add(member.name)
        if member.isdir():
            continue
        if not any(member.name.startswith(prefix) for prefix in allowed_prefixes):
            raise SystemExit("archive contains a file outside the expected bundle paths")
        target=destination.joinpath(*path.parts)
        target.parent.mkdir(parents=True, exist_ok=True)
        source=archive.extractfile(member)
        if source is None: raise SystemExit("archive member has no file data")
        target.write_bytes(source.read())
        target.chmod(0o600)
with tarfile.open(archive_path, "r:gz") as outer:
    copy_archive(outer, root, ("plan.json", "bundle.tgz"))
plan_path=root/"plan.json"; bundle_path=root/"bundle.tgz"
plan=json.loads(plan_path.read_text(encoding="utf-8"))
claimed=plan.pop("planSha256", None)
actual=hashlib.sha256(json.dumps(plan,sort_keys=True,separators=(",",":")).encode()).hexdigest()
if claimed != actual or claimed != expected_hash or plan.get("deploymentId") != expected_id:
    raise SystemExit("deployment plan identity or approval digest does not match")
source=plan.get("sourceSha")
if source and not re.fullmatch(r"[a-f0-9]{40}", source): raise SystemExit("invalid source SHA")
bundle_hash=plan.get("bundleSha256")
if not re.fullmatch(r"[a-f0-9]{64}", str(bundle_hash or "")): raise SystemExit("source bundle digest is missing")
if hashlib.sha256(bundle_path.read_bytes()).hexdigest() != bundle_hash: raise SystemExit("source bundle digest does not match the approved plan")
with tarfile.open(bundle_path, "r:gz") as bundle:
    copy_archive(bundle, root/"source", ("deploy/oci/", "deploy/keycloak/"))
PY

plan_source="$stage/source/deploy/oci"
[[ -s "$plan_source/compose.yaml" && -s "$plan_source/host/apply-plan.sh" ]] || { echo 'Candidate bundle is incomplete.' >&2; exit 1; }
source_sha="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["sourceSha"])' "$stage/plan.json")"
[[ "$source_sha" =~ ^[a-f0-9]{40}$ ]] || { echo 'Deployment requires a source SHA.' >&2; exit 1; }
release_root=/opt/inspection/releases
install -d -o root -g root -m 0750 "$release_root"
release_stage="$release_root/.staging-$deployment_id-$$"
install -d -o root -g root -m 0750 "$release_stage"
cp -a "$stage/source/." "$release_stage/"
release_target="$release_root/$source_sha"
if [[ -e "$release_target" ]]; then
  diff -qr "$release_stage" "$release_target" >/dev/null || { echo 'Source SHA already exists with a different release bundle.' >&2; exit 1; }
  rm -rf "$release_stage"
else
  mv "$release_stage" "$release_target"
fi

job_root=/var/lib/inspection-deploy/jobs
exec 9>/run/lock/inspection-deploy.lock
flock -n 9 || { echo 'Another deployment operation is running.' >&2; exit 1; }
install -d -o root -g root -m 0700 "$job_root"
job_dir="$job_root/$deployment_id"
[[ ! -e "$job_dir" ]] || { echo 'Deployment ID already exists; refusing to replace an operation.' >&2; exit 1; }
install -d -o root -g root -m 0700 "$job_dir"
install -o root -g root -m 0600 "$stage/plan.json" "$job_dir/plan.json"
/usr/local/sbin/inspection-install-host-tools "$source_sha" "$release_target/deploy/oci/host"
# The supervised unit takes the same lock; release it before queueing that unit.
flock -u 9
systemd-run --quiet --no-block --unit="inspection-deployment-$deployment_id" --property=Type=oneshot --property=RemainAfterExit=yes \
  /usr/local/sbin/inspection-run-deployment "$deployment_id" "$source_sha" "$approved_plan_sha"
echo "Deployment $deployment_id accepted for supervised execution."
