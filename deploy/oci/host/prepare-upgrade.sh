#!/usr/bin/env bash
set -euo pipefail

root="${INSPECTION_HOST_ROOT:-/}"
root="${root%/}"
[[ -n "$root" ]] || root=/
data_mount="${root%/}/srv/inspection"
scratch="${data_mount}/video-scratch"
uuid_file="${root%/}/etc/inspection/data-volume.uuid"
image="${1:-}"

[[ "$(id -u)" == 0 ]] || { echo 'Run host upgrade preparation as root.' >&2; exit 1; }
[[ -n "$image" ]] || { echo 'Video worker image is required.' >&2; exit 2; }
[[ -s "$uuid_file" ]] || { echo 'Data volume UUID receipt is missing.' >&2; exit 1; }
mountpoint -q "$data_mount" || { echo 'Inspection data volume is not mounted.' >&2; exit 1; }
expected_uuid="$(tr -d '[:space:]' < "$uuid_file")"
mounted_uuid="$(findmnt -n -o UUID --target "$data_mount" | tr -d '[:space:]')"
[[ -n "$expected_uuid" && "$mounted_uuid" == "$expected_uuid" ]] || { echo 'Mounted inspection data volume UUID does not match its receipt.' >&2; exit 1; }

install -d -o root -g root -m 0700 "$scratch"
identity="$(docker run --rm --entrypoint sh "$image" -c 'printf "%s:%s\n" "$(id -u)" "$(id -g)"')"
[[ "$identity" =~ ^[0-9]+:[0-9]+$ ]] || { echo 'Could not determine video worker uid and gid.' >&2; exit 1; }
uid="${identity%%:*}"
gid="${identity##*:}"
chown "$uid:$gid" "$scratch"
chmod 0700 "$scratch"
docker run --rm --user "$uid:$gid" -v "$scratch:/var/lib/inspection-video-scratch" --entrypoint sh "$image" -eu -c 'f=/var/lib/inspection-video-scratch/.upgrade-check-$$; : > "$f"; rm -f "$f"'
free_bytes="$(python3 - "$scratch" <<'PY'
import os, sys
stats = os.statvfs(sys.argv[1])
print(stats.f_bavail * stats.f_frsize)
PY
)"
[[ "$free_bytes" =~ ^[0-9]+$ ]] && (( free_bytes >= 8589934592 )) || { echo 'Video scratch filesystem must have at least 8 GiB free.' >&2; exit 1; }
echo 'Video worker host prerequisites passed.'
