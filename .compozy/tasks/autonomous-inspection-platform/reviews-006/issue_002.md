---
round: 6
round_created_at: 2026-09-07T17:14:07.382907Z
status: resolved
file: .env.example
line: 40
severity: high
author: reviewer
---

# Issue 002: `.env.example` invalida a porta customizada do MinIO

## Review Comment

O arquivo de exemplo define explicitamente `INSPECTION_MINIO_PUBLIC_ENDPOINT=localhost:9002` e `NEXT_PUBLIC_STORAGE_URL=http://localhost:9002` em `:40` e `:69`. Ao copiar esse arquivo e executar com `INSPECTION_MINIO_PORT=9100`, o Compose publica MinIO em `9100`, mas mantém `api_public=localhost:9002` e `web_storage=http://localhost:9002`. Uploads presigned passam a apontar para a porta errada. Remova esses overrides fixos ou derive-os da porta configurada.

## Triage

- Decision: `VALID`
- Notes: `deploy/docker-compose.yml` already derives both `INSPECTION_MINIO_PUBLIC_ENDPOINT` and `NEXT_PUBLIC_STORAGE_URL` from `INSPECTION_MINIO_PORT` when the variables are unset. The fixed values in `.env.example` override those defaults, so a custom `INSPECTION_MINIO_PORT` leaves presigned and browser storage URLs pointing at port 9002. The root-cause fix removes both overrides from the example environment; Compose can therefore derive both values from the configured port.
