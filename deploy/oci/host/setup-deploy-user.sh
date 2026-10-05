#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -u)" == 0 ]] || { echo 'Run deployment account setup as root.' >&2; exit 2; }
public_key=/etc/inspection/deploy-authorized-key.pub
[[ -s "$public_key" ]] || { echo "Install the GitHub deploy public key at $public_key using a secure channel first." >&2; exit 2; }
[[ "$(stat -c '%a' "$public_key")" == 600 && "$(stat -c '%U:%G' "$public_key")" == root:root ]] || { echo 'Deployment public key must be root-owned mode 0600.' >&2; exit 1; }
[[ "$(wc -l < "$public_key")" -eq 1 ]] || { echo 'Exactly one deployment public key is required.' >&2; exit 1; }
read -r key_type key_data key_comment < "$public_key"
[[ "$key_type" == ssh-ed25519 && "$key_data" =~ ^[A-Za-z0-9+/]+={0,2}$ ]] || { echo 'Only a valid ed25519 public key is accepted.' >&2; exit 1; }
ssh-keygen -lf "$public_key" >/dev/null
id inspection-deploy >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/inspection-deploy --shell /bin/bash inspection-deploy
install -d -o inspection-deploy -g inspection-deploy -m 0700 /var/lib/inspection-deploy/.ssh
printf 'command="/usr/local/sbin/inspection-ssh-command",restrict %s %s %s\n' "$key_type" "$key_data" "${key_comment:-github-actions}" > /var/lib/inspection-deploy/.ssh/authorized_keys
chown inspection-deploy:inspection-deploy /var/lib/inspection-deploy/.ssh/authorized_keys
chmod 0600 /var/lib/inspection-deploy/.ssh/authorized_keys
cat > /etc/sudoers.d/inspection-deploy <<'SUDOERS'
inspection-deploy ALL=(root) NOPASSWD: /usr/local/sbin/inspection-stage-and-apply *, /usr/local/sbin/inspection-export-state *, /usr/local/sbin/inspection-deployment-status *
SUDOERS
chmod 0440 /etc/sudoers.d/inspection-deploy
visudo -cf /etc/sudoers.d/inspection-deploy
echo 'Restricted inspection-deploy SSH account configured.'
