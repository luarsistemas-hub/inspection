#!/usr/bin/env bash
set -Eeuo pipefail

workspace="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export INSPECTION_VIDEO_WORKER=0
export INSPECTION_E2E_AUTH=true

"$workspace/scripts/local.sh" up

compose_args=(docker compose)
if [[ -f "$workspace/.env.inspection" ]]; then
	compose_args+=(--env-file "$workspace/.env.inspection")
fi
compose_args+=(-f "$workspace/deploy/docker-compose.yml")
video_worker="$("${compose_args[@]}" ps --status running -q inspection-video-worker)"
if [[ -n "$video_worker" ]]; then
	printf 'inspection-video-worker está em execução apesar de INSPECTION_VIDEO_WORKER=0\n' >&2
	exit 1
fi

cd "$workspace/apps/capture"
npm run test:e2e -- --project desktop-chrome --grep 'E2E-032 completes the real invitation, consent, review and submission flow'
