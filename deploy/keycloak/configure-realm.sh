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
"$kcadm" update authentication/required-actions/UPDATE_PASSWORD -r inspection \
  -s alias=UPDATE_PASSWORD \
  -s providerId=UPDATE_PASSWORD \
  -s enabled=true \
  -s defaultAction=false \
  -s priority=40
"$kcadm" update realms/inspection \
  -s resetPasswordAllowed=true \
  -s "smtpServer=$KEYCLOAK_SMTP_SERVER_JSON"
