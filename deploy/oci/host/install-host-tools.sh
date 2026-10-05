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
install -d -o root -g root -m 0750 "$root"
rm -rf "$staging"
install -d -o root -g root -m 0750 "$staging"
for name in deploy-release.sh secrets-refresh.sh smoke.sh; do
  install -o root -g root -m 0755 "$source_dir/$name" "$staging/$name"
  bash -n "$staging/$name"
done
if [[ -e "$target" ]]; then
  for name in deploy-release.sh secrets-refresh.sh smoke.sh; do
    cmp -s "$staging/$name" "$target/$name" || { echo "Host-tools version already exists with different content: $version" >&2; exit 1; }
  done
  rm -rf "$staging"
else
  mv "$staging" "$target"
fi

for pair in \
  "inspection-deploy deploy-release.sh" \
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
