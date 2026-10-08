#!/usr/bin/env bash
set -euo pipefail

: "${KEYCLOAK_BOOTSTRAP_USERNAME:?Keycloak bootstrap username is required}"
: "${KEYCLOAK_BOOTSTRAP_PASSWORD:?Keycloak bootstrap password is required}"
: "${KEYCLOAK_SMTP_SERVER_JSON:?Keycloak SMTP configuration is required}"

kcadm=/opt/keycloak/bin/kcadm.sh
"$kcadm" config credentials \
  --server "${KEYCLOAK_ADMIN_URL:-http://keycloak:8080}" \
  --realm master \
  --user "$KEYCLOAK_BOOTSTRAP_USERNAME" \
  --password "$KEYCLOAK_BOOTSTRAP_PASSWORD"
if ! "$kcadm" get authentication/required-actions/UPDATE_PASSWORD -r inspection >/dev/null 2>&1; then
  "$kcadm" create authentication/register-required-action -r inspection -s providerId=UPDATE_PASSWORD
fi

repair_required_action() {
  local alias="$1" name="$2" provider_id saved_action
  if ! "$kcadm" get "authentication/required-actions/$alias" -r inspection >/dev/null 2>&1; then
    if [[ "$alias" == UPDATE_PASSWORD ]]; then
      "$kcadm" create authentication/register-required-action -r inspection \
        -s "providerId=$alias" -s "name=$name"
    fi
    return
  fi

  provider_id="$("$kcadm" get "authentication/required-actions/$alias" -r inspection \
    --fields providerId --format csv --noquotes)"
  [[ "$provider_id" == "$alias" ]] && return
  if [[ -n "$provider_id" ]]; then
    printf 'unexpected providerId for required action %s: %s\n' "$alias" "$provider_id" >&2
    return 1
  fi

  # Keycloak does not persist providerId changes through the update endpoint.
  # Re-register the broken import and restore its existing settings.
  saved_action="$(mktemp)"
  "$kcadm" get "authentication/required-actions/$alias" -r inspection > "$saved_action"
  "$kcadm" delete "authentication/required-actions/$alias" -r inspection
  "$kcadm" create authentication/register-required-action -r inspection \
    -s "providerId=$alias" -s "name=$name"
  "$kcadm" update "authentication/required-actions/$alias" -r inspection -f "$saved_action"
  rm -f "$saved_action"

  provider_id="$("$kcadm" get "authentication/required-actions/$alias" -r inspection \
    --fields providerId --format csv --noquotes)"
  [[ "$provider_id" == "$alias" ]] || {
    printf 'failed to repair required action %s\n' "$alias" >&2
    return 1
  }
}

if [[ "${KEYCLOAK_REPAIR_REQUIRED_ACTIONS:-false}" == true ]]; then
  repair_required_action VERIFY_EMAIL 'Verify Email'
  repair_required_action UPDATE_PROFILE 'Update Profile'
  repair_required_action CONFIGURE_TOTP 'Configure OTP'
  repair_required_action UPDATE_PASSWORD 'Update Password'
  repair_required_action VERIFY_PROFILE 'Verify Profile'
fi

"$kcadm" update authentication/required-actions/UPDATE_PASSWORD -r inspection \
  -s alias=UPDATE_PASSWORD \
  -s providerId=UPDATE_PASSWORD \
  -s enabled=true \
  -s defaultAction=false \
  -s priority=40
"$kcadm" update realms/inspection \
  -s resetPasswordAllowed=true \
  -s "smtpServer=$KEYCLOAK_SMTP_SERVER_JSON"
