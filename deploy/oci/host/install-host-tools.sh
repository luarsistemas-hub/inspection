#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
source_dir="${2:-}"
[[ "$version" == bootstrap || "$version" =~ ^[a-f0-9]{40}$ ]] || { echo "Invalid host-tools version" >&2; exit 2; }
[[ -d "$source_dir" ]] || { echo "Host-tools source directory is missing" >&2; exit 1; }
[[ "$(id -u)" == 0 ]] || { echo "Run this installer with sudo." >&2; exit 1; }

root=/opt/inspection/host-tools
staging="$root/.staging-$version-$$"
target="$root/$version"
install -d -o root -g root -m 0755 "$root"
rm -rf "$staging"
install -d -o root -g root -m 0755 "$staging"
for name in deploy-release.sh apply-plan.sh stage-and-apply.sh run-deployment.sh export-state.sh deployment-status.sh secrets-refresh.sh smoke.sh prepare-upgrade.sh publish-metrics.py ssh-command.sh; do
  install -o root -g root -m 0755 "$source_dir/$name" "$staging/$name"
  case "$name" in
    *.sh) bash -n "$staging/$name" ;;
  esac
done
install -o root -g root -m 0644 "$source_dir/../components.json" "$staging/components.json"
install -o root -g root -m 0644 "$source_dir/component-catalog.sh" "$staging/component-catalog.sh"
chmod 0755 "$staging/component-catalog.sh"
if [[ -e "$target" ]]; then
  # The restricted SSH account executes launchers through this versioned tree.
  install -d -o root -g root -m 0755 "$target"
  for name in deploy-release.sh apply-plan.sh stage-and-apply.sh run-deployment.sh export-state.sh deployment-status.sh secrets-refresh.sh smoke.sh prepare-upgrade.sh publish-metrics.py ssh-command.sh; do
    if [[ -e "$target/$name" ]]; then
      cmp -s "$staging/$name" "$target/$name" || { echo "Host-tools version already exists with different content: $version" >&2; exit 1; }
    else
      install -o root -g root -m 0755 "$staging/$name" "$target/$name"
    fi
  done
  if [[ -e "$target/components.json" ]]; then
    cmp -s "$staging/components.json" "$target/components.json" || { echo "Host-tools version already exists with different content: $version" >&2; exit 1; }
  else
    install -o root -g root -m 0644 "$staging/components.json" "$target/components.json"
  fi
  if [[ -e "$target/component-catalog.sh" ]]; then
    cmp -s "$staging/component-catalog.sh" "$target/component-catalog.sh" || { echo "Host-tools version already exists with different content: $version" >&2; exit 1; }
  else
    install -o root -g root -m 0755 "$staging/component-catalog.sh" "$target/component-catalog.sh"
  fi
  rm -rf "$staging"
else
  mv "$staging" "$target"
fi

for pair in \
  "inspection-deploy deploy-release.sh" \
  "inspection-apply-plan apply-plan.sh" \
  "inspection-stage-and-apply stage-and-apply.sh" \
  "inspection-run-deployment run-deployment.sh" \
  "inspection-export-state export-state.sh" \
  "inspection-deployment-status deployment-status.sh" \
  "inspection-ssh-command ssh-command.sh" \
  "inspection-secrets-refresh secrets-refresh.sh" \
  "inspection-smoke smoke.sh"; do
  read -r launcher script <<< "$pair"
  link="/usr/local/sbin/$launcher"
  temporary="$link.new.$$"
  ln -s "/opt/inspection/host-tools/current/$script" "$temporary"
  mv -Tf "$temporary" "$link"
done
temporary="$root/.current.new.$$"
ln -s "$target" "$temporary"
mv -Tf "$temporary" "$root/current"
chown -h root:root "$root/current"
echo "Activated host-tools version $version."
