#!/usr/bin/env bash
set -euo pipefail

[[ "$(id -u)" == 0 ]] || { echo "Run this bootstrap script with sudo." >&2; exit 1; }
source /etc/os-release
[[ "${ID:-}" == ubuntu && "${VERSION_ID:-}" == 24.04 ]] || { echo "OCI runtime host must be Ubuntu 24.04." >&2; exit 1; }
# INSPECTION_BUNDLE_DIR is the extracted bundle root (it contains deploy/oci); /opt/inspection/current already points at its deploy/oci.
: "${INSPECTION_BUNDLE_DIR:?Set INSPECTION_BUNDLE_DIR to the extracted bundle root}"
host_dir="$INSPECTION_BUNDLE_DIR/deploy/oci/host"
: "${OCI_CLI_VERSION:?Set the OCI CLI version; the validated default is 3.94.1}"
docker_ce_version='5:29.8.2-1~ubuntu.24.04~noble'
docker_buildx_version='0.37.1-1~ubuntu.24.04~noble'
docker_compose_version='5.6.0-1~ubuntu.24.04~noble'
containerd_version='2.3.6-1~ubuntu.24.04~noble'

apt-get update
apt-get install -y ca-certificates curl gnupg jq python3 python3-venv xfsprogs e2fsprogs
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu noble stable" > /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y "docker-ce=${docker_ce_version}" "docker-ce-cli=${docker_ce_version}" "containerd.io=${containerd_version}" "docker-buildx-plugin=${docker_buildx_version}" "docker-compose-plugin=${docker_compose_version}"
systemctl enable --now docker
for expected in "docker-ce=$docker_ce_version" "docker-ce-cli=$docker_ce_version" "containerd.io=$containerd_version" "docker-buildx-plugin=$docker_buildx_version" "docker-compose-plugin=$docker_compose_version"; do
  package="${expected%%=*}"
  version="${expected#*=}"
  actual="$(dpkg-query -W -f='${Version}' "$package")"
  [[ "$actual" == "$version" ]] || { echo "Unexpected package version for $package" >&2; exit 1; }
done

python3 -m venv /opt/inspection/oci-cli
/opt/inspection/oci-cli/bin/pip install --disable-pip-version-check "oci-cli==${OCI_CLI_VERSION}"
install -d -m 0700 /etc/inspection /etc/inspection/secrets.d /etc/inspection/releases
install -d -m 0755 /opt/inspection/releases
install -m 0755 "$host_dir/prepare-host.sh" /usr/local/sbin/inspection-prepare-host
install -m 0755 "$host_dir/install-host-tools.sh" /usr/local/sbin/inspection-install-host-tools
/usr/local/sbin/inspection-install-host-tools bootstrap "$host_dir"
install -m 0755 "$host_dir/publish-metrics.py" /usr/local/sbin/inspection-publish-metrics
install -m 0755 "$host_dir/assert-data-mount.sh" /usr/local/sbin/inspection-assert-data-mount
install -m 0755 "$host_dir/remove-bootstrap-admin.sh" /usr/local/sbin/inspection-remove-bootstrap-admin
INSPECTION_DATA_DEVICE="${INSPECTION_DATA_DEVICE:-/dev/oracleoci/oraclevdb}" INSPECTION_FORMAT_EMPTY_DATA_VOLUME="${INSPECTION_FORMAT_EMPTY_DATA_VOLUME:-}" /usr/local/sbin/inspection-prepare-host

cat > /etc/systemd/system/inspection-secrets-refresh.service <<'UNIT'
[Unit]
Description=Refresh protected Inspection secrets from OCI Vault
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=oneshot
Environment=PATH=/opt/inspection/oci-cli/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin
EnvironmentFile=/etc/inspection/public.env
Environment=INSPECTION_BUNDLE_DIR=/opt/inspection/current
ExecStart=/usr/local/sbin/inspection-secrets-refresh
UNIT
systemctl daemon-reload

cat > /etc/systemd/system/inspection-metrics.service <<'UNIT'
[Unit]
Description=Publish Inspection host capacity and heartbeat metrics
After=network-online.target srv-inspection.mount
Wants=network-online.target
Requires=srv-inspection.mount

[Service]
Type=oneshot
Environment=PATH=/opt/inspection/oci-cli/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin
EnvironmentFile=/etc/inspection/public.env
ExecStart=/usr/local/sbin/inspection-publish-metrics
UNIT
cat > /etc/systemd/system/inspection-metrics.timer <<'UNIT'
[Unit]
Description=Publish Inspection capacity metrics every minute

[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
AccuracySec=10s
Persistent=true
Unit=inspection-metrics.service

[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
systemctl enable --now inspection-metrics.timer

echo "Host packages are installed. Prepare the OCI data volume and install root-owned public.env and secrets.map before refreshing secrets."
