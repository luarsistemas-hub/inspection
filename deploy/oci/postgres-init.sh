#!/usr/bin/env bash
set -euo pipefail

# psql variables are quoted as SQL literals, so passwords may contain quotes.
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v keycloak_password="$KEYCLOAK_DB_PASSWORD" \
  -v runtime_password="$INSPECTION_RUNTIME_PASSWORD" \
  -v worker_password="$INSPECTION_WORKER_PASSWORD" <<'SQL'
CREATE ROLE keycloak LOGIN PASSWORD :'keycloak_password';
CREATE DATABASE keycloak OWNER keycloak;
CREATE ROLE inspection_runtime LOGIN NOINHERIT NOBYPASSRLS PASSWORD :'runtime_password';
CREATE ROLE inspection_worker LOGIN NOINHERIT NOBYPASSRLS PASSWORD :'worker_password';
SQL
