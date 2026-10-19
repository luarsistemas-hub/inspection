#!/usr/bin/env bash
set -euo pipefail

: "${POSTGRES_USER:=postgres}"
: "${POSTGRES_DB:=inspection}"
: "${PGHOST:=localhost}"
: "${PGPORT:=5432}"
: "${PGPASSWORD:?PGPASSWORD is required}"
: "${KEYCLOAK_DB_PASSWORD:?KEYCLOAK_DB_PASSWORD is required}"
: "${INSPECTION_RUNTIME_PASSWORD:?INSPECTION_RUNTIME_PASSWORD is required}"
: "${INSPECTION_WORKER_PASSWORD:?INSPECTION_WORKER_PASSWORD is required}"
: "${INSPECTION_MIGRATOR_PASSWORD:?INSPECTION_MIGRATOR_PASSWORD is required}"
: "${INSPECTION_DISPATCHER_PASSWORD:?INSPECTION_DISPATCHER_PASSWORD is required}"
export POSTGRES_USER POSTGRES_DB PGHOST PGPORT PGPASSWORD

bash deploy/oci/postgres-init.sh
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -v ON_ERROR_STOP=1 \
  -v migrator_password="$INSPECTION_MIGRATOR_PASSWORD" \
  -v dispatcher_password="$INSPECTION_DISPATCHER_PASSWORD" <<'SQL'
CREATE ROLE inspection_migrator LOGIN SUPERUSER PASSWORD :'migrator_password';
CREATE ROLE inspection_dispatcher LOGIN PASSWORD :'dispatcher_password';
SQL
