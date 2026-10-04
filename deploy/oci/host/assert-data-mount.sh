#!/usr/bin/env bash
set -euo pipefail

mountpoint -q /srv/inspection || { echo "Inspection data volume is not mounted; refusing to start Docker." >&2; exit 1; }
expected="$(cat /etc/inspection/data-volume.uuid)"
actual="$(findmnt -n -o UUID --target /srv/inspection)"
[[ -n "$expected" && "$actual" == "$expected" ]] || { echo "Unexpected filesystem mounted at /srv/inspection; refusing to start Docker." >&2; exit 1; }
