#!/usr/bin/env bash
set -euo pipefail

data_device="${INSPECTION_DATA_DEVICE:-/dev/disk/by-id/placeholder}"
mountpoint="/srv/inspection"

if [[ "${data_device}" == *placeholder* || ! -b "${data_device}" ]]; then
  echo "Expected OCI data volume device is unavailable: ${data_device}" >&2
  exit 1
fi

mkdir -p "${mountpoint}"
filesystem="$(blkid -o value -s TYPE "${data_device}" || true)"
if [[ -z "${filesystem}" ]]; then
  [[ "${INSPECTION_FORMAT_EMPTY_DATA_VOLUME:-}" == "yes" ]] || {
    echo "Data volume has no filesystem; explicitly set INSPECTION_FORMAT_EMPTY_DATA_VOLUME=yes for first initialization." >&2
    exit 1
  }
  mkfs.ext4 -F "${data_device}"
  filesystem=ext4
fi

[[ "${filesystem}" == ext4 ]] || { echo "Expected ext4 data volume, found ${filesystem}" >&2; exit 1; }
uuid="$(blkid -o value -s UUID "${data_device}")"
install -d -m 0700 /etc/inspection
grep -q "UUID=${uuid} ${mountpoint} " /etc/fstab || echo "UUID=${uuid} ${mountpoint} ext4 defaults,nofail 0 2" >> /etc/fstab
mountpoint -q "${mountpoint}" || mount "${mountpoint}"
printf '%s\n' "$uuid" > /etc/inspection/data-volume.uuid
chmod 0600 /etc/inspection/data-volume.uuid
install -d -o root -g root -m 0700 /etc/inspection /srv/inspection/{postgres,rabbitmq,dragonfly,keycloak,caddy,data}
# These image digests run PostgreSQL as uid/gid 70 and RabbitMQ as uid 100/gid 101.
# Keep their host-mounted data directories private while allowing each daemon to write.
chown -R 70:70 /srv/inspection/postgres
chmod 0700 /srv/inspection/postgres
chown -R 100:101 /srv/inspection/rabbitmq
chmod 0700 /srv/inspection/rabbitmq
install -d -o root -g root -m 0750 /opt/inspection/releases

command -v docker >/dev/null 2>&1 || { echo "Docker must be installed by install-host.sh" >&2; exit 1; }
if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose plugin is unavailable" >&2
  exit 1
fi
install -d -m 0755 /etc/systemd/system/docker.service.d
cat > /etc/systemd/system/docker.service.d/inspection-data-volume.conf <<'UNIT'
[Unit]
Requires=srv-inspection.mount
After=srv-inspection.mount

[Service]
ExecStartPre=/usr/local/sbin/inspection-assert-data-mount
UNIT
systemctl daemon-reload
