#!/usr/bin/env bash
set -euo pipefail

command_line="${SSH_ORIGINAL_COMMAND:-}"
if [[ "$command_line" =~ ^apply-plan[[:space:]]+([A-Za-z0-9][A-Za-z0-9._-]{2,100})[[:space:]]+([a-f0-9]{64})$ ]]; then
  exec sudo -n /usr/local/sbin/inspection-stage-and-apply "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
fi
if [[ "$command_line" =~ ^export-state[[:space:]]+(current|previous)$ ]]; then
  exec sudo -n /usr/local/sbin/inspection-export-state "${BASH_REMATCH[1]}"
fi
if [[ "$command_line" == deployment-status ]]; then
  exec sudo -n /usr/local/sbin/inspection-deployment-status
fi
if [[ "$command_line" =~ ^deployment-status[[:space:]]+([A-Za-z0-9][A-Za-z0-9._-]{2,100})$ ]]; then
  exec sudo -n /usr/local/sbin/inspection-deployment-status "${BASH_REMATCH[1]}"
fi
echo 'Allowed SSH commands: apply-plan <deployment-id> <plan-sha256>, export-state <current|previous>, or deployment-status [deployment-id].' >&2
exit 2
