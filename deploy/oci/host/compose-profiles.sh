#!/usr/bin/env bash

inspection_env_value() {
  local file="$1" key="$2"
  [[ -f "$file" ]] || return 0
  awk -v key="$key" '
    index($0, key "=") == 1 {
      value = substr($0, length(key) + 2)
      if ((substr(value, 1, 1) == "\047" && substr(value, length(value), 1) == "\047") ||
          (substr(value, 1, 1) == "\042" && substr(value, length(value), 1) == "\042")) {
        value = substr(value, 2, length(value) - 2)
      }
      result = value
    }
    END { if (result != "") print result }
  ' "$file"
}

inspection_transcription_active() {
  local env_file="$1"
  local enabled image
  enabled="$(inspection_env_value "$env_file" INSPECTION_TRANSCRIPTION_WORKER)"
  image="$(inspection_env_value "$env_file" TRANSCRIPTION_WORKER_IMAGE)"
  [[ "$enabled" != 0 && -n "$image" ]]
}

inspection_compose_profile_args() {
  local env_file="$1"
  if inspection_transcription_active "$env_file"; then
    printf '%s\n' --profile transcription
  fi
}

inspection_app_services() {
  local env_file="$1"
  printf '%s\n' inspection-api inspection-worker inspection-scheduler admin dashboard capture onboarding caddy
  if inspection_transcription_active "$env_file"; then
    printf '%s\n' inspection-transcription-worker
  fi
}
