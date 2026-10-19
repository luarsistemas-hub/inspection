#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
runtime_host="${INSPECTION_RUNTIME_HOST:-}"
package_dir="${INSPECTION_PACKAGE_DIR:-/tmp/inspection-oci-packages}"
catalog_image_keys="$(python3 - "$root/components.json" <<'PY'
import json, sys
catalog=json.load(open(sys.argv[1], encoding="utf-8"))["components"]
keys={item["image"] for item in catalog.values()} | {"POSTGRES_IMAGE", "DRAGONFLY_IMAGE", "RABBITMQ_IMAGE", "CADDY_IMAGE", "LITELLM_IMAGE"}
print(" ".join(sorted(key for key in keys if key.endswith("_IMAGE"))))
PY
)"
catalog_services="$(python3 - "$root/components.json" <<'PY'
import json, sys
catalog=json.load(open(sys.argv[1], encoding="utf-8"))["components"]
print("|".join(sorted({item["service"] for item in catalog.values() if item.get("service")} | {"caddy", "postgres", "rabbitmq", "dragonfly", "litellm"})))
PY
)"
require_host() { [[ -n "$runtime_host" ]] || { echo "Set INSPECTION_RUNTIME_HOST to the VM public IP or hostname." >&2; exit 2; }; }
ssh_target() { require_host; if [[ "$runtime_host" == *@* ]]; then printf '%s' "$runtime_host"; else printf 'ubuntu@%s' "$runtime_host"; fi; }
ssh_host() { ssh -o StrictHostKeyChecking=yes "$(ssh_target)" "$@"; }
safe_release() { [[ "${1:-}" =~ ^[a-f0-9]{40}$ ]] || { echo "Release must be a 40-character commit SHA." >&2; exit 2; }; }
usage() { echo "Usage: $0 {validate|package|preflight|bootstrap|secrets refresh|host-tools RELEASE|deploy RELEASE|rollback RELEASE|plan ACTIVE.env CANDIDATE.json TARGETS [PLAN.json]|rollback-plan ACTIVE.env PREVIOUS.env TARGETS PLAN.json|recovery-plan BLOCKED.env CANDIDATE.json PLAN.json|apply PLAN.json SOURCE-BUNDLE.tgz|deployment-status|sync-state [current|previous|blocked]|status|cleanup-images|logs SERVICE|restart SERVICE|smoke|stop}"; }
verify_release() {
  local release="$1" bundle="$2" manifest="$3" checksum="$4"
  python3 "$root/verify-release.py" --release "$release" --bundle "$bundle" --manifest "$manifest" --checksum "$checksum"
}
transfer_release() {
  local release="$1" bundle="$2" manifest="$3" checksum="$4"
  scp -o StrictHostKeyChecking=yes "$bundle" "$manifest" "$checksum" "$(ssh_target):/tmp/"
  ssh_host "set -euo pipefail
    release='$release'
    bundle='/tmp/$release-bundle.tgz'
    manifest='/tmp/$release.env'
    checksum='/tmp/$release-bundle.sha256'
    cd /tmp
    sha256sum --check '$release-bundle.sha256'
    grep -qx \"RELEASE_SHA=$release\" \"\$manifest\"
    grep -qx \"BUNDLE_SHA256=\$(awk 'NR==1 {print \$1}' \"\$checksum\")\" \"\$manifest\"
    for key in $catalog_image_keys; do
      if [[ \$key == WORKER_IMAGE || \$key == SCHEDULER_IMAGE || \$key == OPERATIONS_IMAGE || \$key == LITELLM_IMAGE || \$key == VIDEO_WORKER_IMAGE ]] && ! grep -q ^\$key= \$manifest; then continue; fi
      value=\$(grep -m1 \"^\$key=\" \"\$manifest\" | cut -d= -f2-)
      [[ \"\$value\" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]] || { echo \"Invalid digest-pinned image reference: \$key\" >&2; exit 1; }
      [[ \$(grep -c \"^\$key=\" \"\$manifest\") == 1 ]] || { echo \"Duplicate or missing image reference: \$key\" >&2; exit 1; }
    done
    root='/opt/inspection/releases'
    sudo mkdir -p \"\$root\" /etc/inspection/releases
    staging=\$(sudo mktemp -d \"\$root/.staging-$release.XXXXXX\")
    trap 'sudo rm -rf \"\$staging\"' EXIT
    if [[ -e \"\$root/$release\" ]]; then
      sudo test -s \"\$root/$release/bundle.sha256\" || { echo 'Existing release has no verified bundle checksum' >&2; exit 1; }
      sudo cmp -s \"\$checksum\" \"\$root/$release/bundle.sha256\" || { echo 'Refusing to overwrite an existing release with a different bundle' >&2; exit 1; }
      sudo cmp -s \"\$manifest\" /etc/inspection/releases/$release.env || { echo 'Refusing to overwrite an immutable release manifest' >&2; exit 1; }
      sudo tar -xzf \"\$bundle\" -C \"\$staging\"
      sudo diff -qr \"\$staging/deploy\" \"\$root/$release/deploy\" || { echo 'Existing release files differ from the verified bundle' >&2; exit 1; }
    else
      sudo tar -xzf \"\$bundle\" -C \"\$staging\"
      sudo install -m 0600 \"\$checksum\" \"\$staging/bundle.sha256\"
      sudo mv \"\$staging\" \"\$root/$release\"
      sudo install -m 0600 \"\$manifest\" /etc/inspection/releases/.$release.env.new
      sudo mv -f /etc/inspection/releases/.$release.env.new /etc/inspection/releases/$release.env
    fi
    rm -f \"\$bundle\" \"\$manifest\" \"\$checksum\""
}

case "${1:-}" in
  plan)
    active="${2:-}" candidate="${3:-}" targets="${4:-}" output="${5:-plan.json}"
    [[ -n "$active" && -n "$candidate" && -n "$targets" ]] || { usage; exit 2; }
    source_sha="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["sourceSha"])' "$candidate")"
    bundle="${output%.json}.source-bundle.tgz"
    mkdir -p "$(dirname "$output")"
    git archive --format=tar.gz --output="$bundle" "$source_sha" deploy/oci deploy/keycloak
    bundle_sha="$(sha256sum "$bundle" | awk '{print $1}')"
    scope=selected; [[ "$targets" != all && "$targets" != changed ]] || { scope="$targets"; targets=""; }
    python3 "$root/planner.py" --active "$active" --candidate "$candidate" --targets "$targets" --scope "$scope" --repo "$(git rev-parse --show-toplevel)" --bundle-sha256 "$bundle_sha" --output "$output"
    ;;
  rollback-plan)
    active="${2:-}" previous="${3:-}" targets="${4:-}" output="${5:-}"
    [[ -n "$active" && -n "$previous" && -n "$targets" && -n "$output" ]] || { usage; exit 2; }
    source_sha="$(sed -n 's/^SOURCE_SHA=//p' "$active" | head -n 1)"
    [[ "$source_sha" =~ ^[a-f0-9]{40}$ ]] || { echo 'Active receipt has no valid SOURCE_SHA; automatic rollback is unavailable.' >&2; exit 1; }
    bundle="${output%.json}.source-bundle.tgz"
    mkdir -p "$(dirname "$output")"
    git archive --format=tar.gz --output="$bundle" "$source_sha" deploy/oci deploy/keycloak
    bundle_sha="$(sha256sum "$bundle" | awk '{print $1}')"
    python3 "$root/planner.py" --active "$active" --rollback-from "$previous" --targets "$targets" --source-sha "$source_sha" --repo "$(git rev-parse --show-toplevel)" --bundle-sha256 "$bundle_sha" --output "$output"
    ;;
  recovery-plan)
    blocked="${2:-}" candidate="${3:-}" output="${4:-}"
    [[ -s "$blocked" && -s "$candidate" && -n "$output" ]] || { usage; exit 2; }
    blocked_id="$(sed -n 's/^BLOCKED_DEPLOYMENT_ID=//p' "$blocked" | head -n 1)"
    blocked_hash="$(sed -n 's/^BLOCKED_PLAN_SHA256=//p' "$blocked" | head -n 1)"
    [[ "$blocked_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,100}$ && "$blocked_hash" =~ ^[a-f0-9]{64}$ ]] || { echo 'Blocked state is missing its recovery reference.' >&2; exit 1; }
    source_sha="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["sourceSha"])' "$candidate")"
    bundle="${output%.json}.source-bundle.tgz"
    mkdir -p "$(dirname "$output")"
    git archive --format=tar.gz --output="$bundle" "$source_sha" deploy/oci deploy/keycloak
    bundle_sha="$(sha256sum "$bundle" | awk '{print $1}')"
    python3 "$root/planner.py" --active "$blocked" --candidate "$candidate" --targets '' --scope all --repo "$(git rev-parse --show-toplevel)" --recovery-of "$blocked_id,$blocked_hash" --deployment-id "recovery-$(date -u +%Y%m%d%H%M%S)" --bundle-sha256 "$bundle_sha" --output "$output"
    ;;
  apply)
    plan="${2:-}" bundle="${3:-}"
    [[ -s "$plan" && -s "$bundle" ]] || { usage; exit 2; }
    require_host
    approved="${INSPECTION_APPROVED_PLAN_SHA256:-}"
    plan_meta="$(python3 - "$plan" <<'PY'
import hashlib, json, sys
plan=json.load(open(sys.argv[1], encoding="utf-8")); claimed=plan.pop("planSha256", None)
actual=hashlib.sha256(json.dumps(plan,sort_keys=True,separators=(",",":")).encode()).hexdigest()
if claimed != actual: raise SystemExit("Invalid plan hash")
print(plan["deploymentId"]+" "+actual)
PY
)"
    read -r deployment_id plan_hash <<< "$plan_meta"
    [[ "$approved" == "$plan_hash" ]] || { echo "Set INSPECTION_APPROVED_PLAN_SHA256=$plan_hash only after reviewing this exact plan." >&2; exit 2; }
    staging="$(mktemp -d /tmp/inspection-plan.XXXXXX)"
    trap 'rm -rf "$staging"' EXIT
    install -m 0600 "$plan" "$staging/plan.json"
    install -m 0600 "$bundle" "$staging/bundle.tgz"
    accepted=0
    COPYFILE_DISABLE=1 tar -czf - -C "$staging" plan.json bundle.tgz | ssh -o StrictHostKeyChecking=yes "$(ssh_target)" "apply-plan $deployment_id $plan_hash" || accepted=$?
    unknown_count=0
    for attempt in $(seq 1 360); do
      status_json="$(ssh -o StrictHostKeyChecking=yes "$(ssh_target)" "deployment-status $deployment_id" 2>/dev/null || true)"
      status="$(jq -r '.status // "unknown"' <<< "$status_json" 2>/dev/null || echo unknown)"
      case "$status" in
        succeeded) echo "$status_json"; break ;;
        failed) echo "Supervised deployment failed: $status_json" >&2; exit 1 ;;
        unknown)
          unknown_count=$((unknown_count + 1))
          if [[ ( "$accepted" -ne 0 && "$unknown_count" -ge 3 ) || "$unknown_count" -ge 7 ]]; then echo 'Remote executor did not report this operation; inspect deployment status before retrying.' >&2; exit 1; fi
          ;;
      esac
      [[ "$attempt" -lt 360 ]] || { echo 'Timed out waiting for the supervised deployment; inspect deployment-status before retrying.' >&2; exit 1; }
      sleep 10
    done
    ;;
  deployment-status)
    if [[ -n "${2:-}" ]]; then ssh -o StrictHostKeyChecking=yes "$(ssh_target)" "deployment-status $2"; else ssh -o StrictHostKeyChecking=yes "$(ssh_target)" deployment-status; fi
    ;;
  sync-state)
    kind="${2:-current}" output="${3:-}"
    [[ "$kind" == current || "$kind" == previous || "$kind" == blocked ]] || { usage; exit 2; }
    if [[ -n "$output" ]]; then ssh -o StrictHostKeyChecking=yes "$(ssh_target)" "export-state $kind" > "$output"; else ssh -o StrictHostKeyChecking=yes "$(ssh_target)" "export-state $kind"; fi
    ;;
  validate)
    command -v terraform >/dev/null || { echo "Install Terraform 1.5.7 before validation." >&2; exit 1; }
    terraform_version="$(terraform version -json | jq -r '.terraform_version')"
    [[ "$terraform_version" == 1.5.7 ]] || { echo "Expected Terraform 1.5.7, found $terraform_version." >&2; exit 1; }
    for stack in foundation runtime; do
      test -f "$root/$stack/main.tf"
      test -f "$root/$stack/schema.yaml"
      (cd "$root/$stack" && terraform fmt -check -recursive && terraform init -backend=false -input=false && terraform validate)
    done
    for script in "$root/ops.sh" "$root"/host/*.sh; do bash -n "$script"; done
    python3 -m py_compile "$root"/*.py "$root"/host/*.py
    docker compose --env-file "$root/compose.env.example" -f "$root/compose.yaml" config --quiet
    ;;
  package)
    command -v zip >/dev/null
    mkdir -p "$package_dir"
    for stack in foundation runtime; do
      rm -f "$package_dir/$stack.zip"
      (cd "$root/$stack" && zip -q -r "$package_dir/$stack.zip" . -x '.terraform/*' '*.tfstate*' '*.tfvars' '*.tfvars.json' '*.env' '.env' '*.plan' '*.zip')
      echo "Created $package_dir/$stack.zip"
    done
    staging="$(mktemp -d /tmp/inspection-oci-bundle.XXXXXX)"
    trap 'rm -rf "$staging"' EXIT
    mkdir -p "$staging/deploy"
    cp -R "$root" "$staging/deploy/oci"
    rm -rf "$staging/deploy/oci/foundation/.terraform" "$staging/deploy/oci/runtime/.terraform" "$staging/deploy/oci/releases" "$staging/deploy/oci/.build"
    cp -R "$root/../../deploy/keycloak" "$staging/deploy/keycloak"
    find "$staging" -type d -name __pycache__ -prune -exec rm -rf {} +
    find "$staging" -type f -name '*.pyc' -delete
    find "$staging/deploy/oci" -type f \( -name '*.tfstate' -o -name '*.tfstate.*' -o -name '*.tfvars' -o -name '*.tfvars.json' -o -name '*.plan' -o -name '*.env' -o -name '.env' -o -name 'secrets.map' \) -delete
    rm -f "$package_dir/runtime-bundle.tgz"
    tar -czf "$package_dir/runtime-bundle.tgz" -C "$staging" deploy
    echo "Created $package_dir/runtime-bundle.tgz"
    ;;
  preflight)
    : "${INSPECTION_RUNTIME_HOST:?Set the OCI VM IP or hostname}"
    : "${INSPECTION_DOMAIN:?Set the project domain}"
    command -v ssh >/dev/null
    command -v python3 >/dev/null
    python3 - "$INSPECTION_DOMAIN" "${INSPECTION_RUNTIME_HOST##*@}" <<'PY'
import ipaddress
import socket
import sys

domain, host = sys.argv[1:]
def _is_ip(value):
    try:
        ipaddress.ip_address(value)
        return True
    except ValueError:
        return False

expected = str(ipaddress.ip_address(host)) if _is_ip(host) else socket.gethostbyname(host)
for label in ("admin", "dashboard", "capture", "onboarding", "api", "auth", "storage"):
    name = f"{label}.{domain}"
    addresses = {item[4][0] for item in socket.getaddrinfo(name, None, socket.AF_INET)}
    if expected not in addresses:
        raise SystemExit(f"{name} does not resolve to runtime address {expected}: {sorted(addresses)}")
PY
    command -v gh >/dev/null || { echo "Install GitHub CLI to confirm your authenticated release account." >&2; exit 1; }
    gh auth status
    echo "Confirm home region, A1 capacity, aggregate quotas, approved budget, seven public DNS records and GHCR read access before Resource Manager Apply."
    ;;
  bootstrap)
    require_host
    format_empty_volume="${INSPECTION_FORMAT_EMPTY_DATA_VOLUME:-no}"
    [[ "$format_empty_volume" == yes || "$format_empty_volume" == no ]] || { echo "INSPECTION_FORMAT_EMPTY_DATA_VOLUME must be yes or omitted." >&2; exit 2; }
    bundle="$package_dir/runtime-bundle.tgz"
    test -s "$bundle" || { echo "Run ops.sh package first." >&2; exit 1; }
    scp -o StrictHostKeyChecking=yes "$bundle" "$(ssh_target):/tmp/inspection-runtime-bundle.tgz"
    ssh_host "sudo mkdir -p /opt/inspection/releases/bootstrap /etc/inspection/releases && sudo tar -xzf /tmp/inspection-runtime-bundle.tgz -C /opt/inspection/releases/bootstrap && sudo ln -sfn /opt/inspection/releases/bootstrap/deploy/oci /opt/inspection/current && sudo env OCI_CLI_VERSION=3.94.1 INSPECTION_FORMAT_EMPTY_DATA_VOLUME='$format_empty_volume' INSPECTION_BUNDLE_DIR=/opt/inspection/releases/bootstrap bash /opt/inspection/releases/bootstrap/deploy/oci/host/install-host.sh"
    ;;
  secrets)
    [[ "${2:-}" == refresh ]] || { usage; exit 2; }
    ssh_host 'sudo systemctl start inspection-secrets-refresh.service && sudo systemctl --no-pager --full status inspection-secrets-refresh.service'
    ;;
  host-tools|deploy|rollback)
    release="${2:-}"
    safe_release "$release"
    if [[ "$1" == rollback && "${INSPECTION_ROLLBACK_SCHEMA_APPROVED:-}" != yes ]]; then
      echo "Set INSPECTION_ROLLBACK_SCHEMA_APPROVED=yes only after checking that this image release supports the current database schema." >&2
      exit 2
    fi
    require_host
    if [[ "$1" == deploy || "$1" == rollback ]]; then
      if ssh_host "sudo grep -q '^VIDEO_WORKER_IMAGE=' /etc/inspection/releases/current.env"; then
        echo 'Legacy deploy/rollback is disabled for video-enabled receipts; use plan/apply.' >&2
        exit 1
      fi
    fi
    bundle="$root/releases/$release-bundle.tgz"
    manifest="$root/releases/$release.env"
    checksum="$root/releases/$release-bundle.sha256"
    test -s "$bundle" && test -s "$manifest" && test -s "$checksum" || { echo "Expected verified bundle, checksum and manifest under deploy/oci/releases/." >&2; exit 1; }
    verify_release "$release" "$bundle" "$manifest" "$checksum"
    require_host
    transfer_release "$release" "$bundle" "$manifest" "$checksum"
    if [[ "$1" == host-tools ]]; then
      ssh_host "sudo /usr/local/sbin/inspection-install-host-tools '$release' '/opt/inspection/releases/$release/deploy/oci/host'"
    elif [[ "$1" == deploy ]]; then
      ssh_host "sudo /usr/local/sbin/inspection-install-host-tools '$release' '/opt/inspection/releases/$release/deploy/oci/host' && sudo /usr/local/sbin/inspection-deploy '$release'"
    else
      ssh_host "sudo /usr/local/sbin/inspection-deploy '$release' --rollback"
    fi
    ;;
  status) ssh_host 'sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml ps; sudo df -h / /srv/inspection; sudo docker system df; sudo test ! -e /etc/inspection/deployment-blocked || sudo cat /etc/inspection/deployment-blocked; sudo systemctl --no-pager status inspection-secrets-refresh.service' ;;
  cleanup-images) ssh_host "sudo bash -s -- $catalog_image_keys" <<'REMOTE'
set -euo pipefail
keep=("")
image_keys=("")
for key in "$@"; do [[ "$key" == *_IMAGE ]] && image_keys+=("$key"); done
known=("${image_keys[@]}")
contains() {
  local needle="$1" item
  shift
  for item in "$@"; do [[ -n "$item" && "$item" == "$needle" ]] && return 0; done
  return 1
}
component_keys=("")
infra_keys=(POSTGRES_IMAGE DRAGONFLY_IMAGE RABBITMQ_IMAGE CADDY_IMAGE LITELLM_IMAGE)
for key in "${image_keys[@]}"; do
  [[ -n "$key" ]] || continue
  case "$key" in POSTGRES_IMAGE|DRAGONFLY_IMAGE|RABBITMQ_IMAGE|CADDY_IMAGE|LITELLM_IMAGE) ;; *) component_keys+=("$key") ;; esac
done
full_count=$((${#component_keys[@]} - 1 + ${#infra_keys[@]}))
pre_video_count=$((full_count - 1))
pre_litellm_count=$((full_count - 2))
legacy_count=10
for manifest in /etc/inspection/releases/current.env /etc/inspection/releases/previous.env; do
  [[ -s "$manifest" ]] || continue
  seen=("")
  while IFS='=' read -r key reference; do
    [[ "$key" == *_IMAGE ]] || continue
    contains "$key" "${known[@]}" || { echo "Unknown image key in $manifest: $key" >&2; exit 1; }
    [[ "$reference" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]] || { echo "Invalid image reference in $manifest: $key" >&2; exit 1; }
    contains "$key" "${seen[@]}" && { echo "Duplicate image reference in $manifest: $key" >&2; exit 1; }
    seen+=("$key")
    image_id="$(docker image inspect --format '{{.Id}}' "$reference")" || { echo "Required current/previous image is missing: $reference" >&2; exit 1; }
    contains "$image_id" "${keep[@]}" || keep+=("$image_id")
  done < "$manifest"
  image_count=$((${#seen[@]} - 1))
  valid=0
  for omitted in '' VIDEO_WORKER_IMAGE 'VIDEO_WORKER_IMAGE LITELLM_IMAGE'; do
    expected=("")
    for key in "${image_keys[@]}"; do
      [[ -n "$key" ]] || continue
      [[ " $omitted " == *" $key "* ]] || expected+=("$key")
    done
    if (( image_count == ${#expected[@]} - 1 )); then
      matches=1
      for key in "${expected[@]}"; do [[ -n "$key" ]] || continue; contains "$key" "${seen[@]}" || matches=0; done
      (( matches == 1 )) && valid=1
    fi
  done
  if (( image_count == legacy_count )); then
    legacy=(API_IMAGE KEYCLOAK_IMAGE ADMIN_IMAGE DASHBOARD_IMAGE CAPTURE_IMAGE ONBOARDING_IMAGE POSTGRES_IMAGE DRAGONFLY_IMAGE RABBITMQ_IMAGE CADDY_IMAGE)
    matches=1
    for key in "${legacy[@]}"; do contains "$key" "${seen[@]}" || matches=0; done
    (( matches == 1 )) && valid=1
  fi
  (( valid == 1 )) || { echo "Image manifest is incomplete: $manifest" >&2; exit 1; }
  unset seen
done
(( ${#keep[@]} > 1 )) || { echo 'No current release manifest found; refusing image cleanup.' >&2; exit 1; }
image_ids=()
while IFS= read -r image_id; do [[ -n "$image_id" ]] && image_ids+=("$image_id"); done < <(docker image ls --no-trunc --quiet | sort -u)
for image_id in "${image_ids[@]}"; do
  contains "$image_id" "${keep[@]}" && continue
  if [[ -n "$(docker ps --all --quiet --filter "ancestor=$image_id")" ]]; then continue; fi
  docker image rm "$image_id"
done
REMOTE
    ;;
  logs) service="${2:-}"; [[ "$service" =~ ^[a-z0-9_-]+$ ]] || { usage; exit 2; }; ssh_host "sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml logs --tail=200 $service" ;;
  restart) service="${2:-}"; [[ "$service" =~ ^($catalog_services)$ ]] || { usage; exit 2; }; ssh_host "sudo test ! -e /etc/inspection/deployment-blocked || { echo 'Deployment is blocked; verify schema compatibility and deploy forward.' >&2; exit 1; }; sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml restart $service" ;;
  smoke) ssh_host 'sudo /usr/local/sbin/inspection-smoke' ;;
  stop) ssh_host 'sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml stop' ;;
  *) usage; exit 2 ;;
esac
