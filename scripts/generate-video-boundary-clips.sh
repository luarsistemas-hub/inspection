#!/usr/bin/env bash
set -Eeuo pipefail

need() { command -v "$1" >/dev/null 2>&1 || { printf 'required command not found: %s\n' "$1" >&2; exit 127; }; }
need ffmpeg
out_dir="${1:-$(mktemp -d "${TMPDIR:-/tmp}/inspection-video-boundary.XXXXXX")}"
mkdir -p "$out_dir"
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i 'color=c=black:s=16x16:r=30:d=900.001' -an \
  -c:v libx264 -preset ultrafast -tune zerolatency -b:v 1k -maxrate 1k -bufsize 2k \
  -pix_fmt yuv420p -movflags +faststart "$out_dir/black.mp4"
printf '%s\n' "$out_dir/black.mp4"
