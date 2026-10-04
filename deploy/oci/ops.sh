#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
runtime_host="${INSPECTION_RUNTIME_HOST:-}"
package_dir="${INSPECTION_PACKAGE_DIR:-/tmp/inspection-oci-packages}"
require_host() { [[ -n "$runtime_host" ]] || { echo "Set INSPECTION_RUNTIME_HOST to the VM public IP or hostname." >&2; exit 2; }; }
ssh_target() { require_host; if [[ "$runtime_host" == *@* ]]; then printf '%s' "$runtime_host"; else printf 'ubuntu@%s' "$runtime_host"; fi; }
ssh_host() { ssh -o StrictHostKeyChecking=yes "$(ssh_target)" "$@"; }
safe_release() { [[ "${1:-}" =~ ^[a-f0-9]{40}$ ]] || { echo "Release must be a 40-character commit SHA." >&2; exit 2; }; }
usage() { echo "Usage: $0 {validate|package|preflight|bootstrap|secrets refresh|deploy RELEASE|rollback RELEASE|status|logs SERVICE|restart SERVICE|smoke|stop}"; }

case "${1:-}" in
  validate)
    command -v terraform >/dev/null || { echo "Install Terraform 1.5.7 before validation." >&2; exit 1; }
    terraform_version="$(terraform version -json | jq -r '.terraform_version')"
    [[ "$terraform_version" == 1.5.7 ]] || { echo "Expected Terraform 1.5.7, found $terraform_version." >&2; exit 1; }
    for stack in foundation runtime; do
      test -f "$root/$stack/main.tf"
      test -f "$root/$stack/schema.yaml"
      (cd "$root/$stack" && terraform fmt -check -recursive && terraform init -backend=false -input=false && terraform validate)
    done
    bash -n "$root/ops.sh" "$root"/host/*.sh
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
    rm -rf "$staging/deploy/oci/foundation/.terraform" "$staging/deploy/oci/runtime/.terraform" "$staging/deploy/oci/releases"
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
  deploy|rollback)
    release="${2:-}"
    safe_release "$release"
    if [[ "$1" == rollback && "${INSPECTION_ROLLBACK_SCHEMA_APPROVED:-}" != yes ]]; then
      echo "Set INSPECTION_ROLLBACK_SCHEMA_APPROVED=yes only after checking that this image release supports the current database schema." >&2
      exit 2
    fi
    require_host
    bundle="$root/releases/$release-bundle.tgz"
    manifest="$root/releases/$release.env"
    test -s "$bundle" && test -s "$manifest" || { echo "Expected release bundle and manifest under deploy/oci/releases/." >&2; exit 1; }
    grep -qx "RELEASE_SHA=$release" "$manifest" || { echo "Manifest does not match the requested release." >&2; exit 1; }
    scp -o StrictHostKeyChecking=yes "$bundle" "$(ssh_target):/tmp/$release-bundle.tgz"
    scp -o StrictHostKeyChecking=yes "$manifest" "$(ssh_target):/tmp/$release.env"
    mode_flag=""
    [[ "$1" == rollback ]] && mode_flag=--rollback
    ssh_host "sudo mkdir -p /opt/inspection/releases/$release /etc/inspection/releases && sudo tar -xzf /tmp/$release-bundle.tgz -C /opt/inspection/releases/$release && sudo install -m 0600 /tmp/$release.env /etc/inspection/releases/$release.env && sudo /usr/local/sbin/inspection-deploy $release $mode_flag"
    ;;
  status) ssh_host 'sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml ps; sudo systemctl --no-pager status inspection-secrets-refresh.service' ;;
  logs) service="${2:-}"; [[ "$service" =~ ^[a-z0-9_-]+$ ]] || { usage; exit 2; }; ssh_host "sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml logs --tail=200 $service" ;;
  restart) service="${2:-}"; [[ "$service" =~ ^(caddy|inspection-api|inspection-worker|inspection-scheduler|admin|dashboard|capture|onboarding|keycloak|postgres|rabbitmq|dragonfly)$ ]] || { usage; exit 2; }; ssh_host "sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml restart $service" ;;
  smoke) ssh_host 'sudo /usr/local/sbin/inspection-smoke' ;;
  stop) ssh_host 'sudo docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml stop' ;;
  *) usage; exit 2 ;;
esac
