#!/usr/bin/env bash
set -euo pipefail

source /etc/inspection/public.env
docker compose --env-file /etc/inspection/compose.env -f /opt/inspection/current/compose.yaml exec -T keycloak /bin/bash -euc '
  kcadm=/opt/keycloak/bin/kcadm.sh
  "$kcadm" config credentials --server http://localhost:8080 --realm master \
    --user "$KC_BOOTSTRAP_ADMIN_USERNAME" --password "$KC_BOOTSTRAP_ADMIN_PASSWORD" >/dev/null
  users="$("$kcadm" get users -r master -q "username=$KC_BOOTSTRAP_ADMIN_USERNAME")"
  user_id="$(printf "%s" "$users" | jq -er "if length == 1 then .[0].id else error(\"expected exactly one bootstrap administrator\") end")"
  "$kcadm" delete "users/$user_id" -r master
  echo "Temporary Keycloak master-realm bootstrap administrator removed."
'
