---
round: 5
round_created_at: 2026-09-07T16:54:40.800467Z
status: resolved
file: deploy/docker-compose.yml
line: 163
severity: high
author: reviewer
---

# Issue 001: Porta MinIO customizada gera URLs presigned inválidas

## Review Comment

`deploy/docker-compose.yml:41` permite `INSPECTION_MINIO_PORT` customizada, mas `:163` fixa `INSPECTION_MINIO_PUBLIC_ENDPOINT` em `localhost:9002`. Evidência: com `INSPECTION_MINIO_PORT=9100`, `docker compose config` publica MinIO em `9100`, enquanto a API continua configurada para presignar usando `localhost:9002`; `inspection-api` usa esse endpoint no `PublicClient` (`services/inspection/cmd/inspection-api/main.go:143-149`). Uploads do browser passam a receber URLs na porta errada. Derive o endpoint público da mesma variável ou exija um override explícito validado.

## Triage

- Decision: `VALID`
- Root cause: the MinIO host port mapping was configurable through `INSPECTION_MINIO_PORT`, while the API's public endpoint and the web storage origin retained a literal `9002` default. With a custom host port, presigned URLs and the browser's storage policy targeted different endpoints.
- Resolution: derive the API public endpoint and web storage URL defaults from `INSPECTION_MINIO_PORT`, while preserving explicit endpoint overrides.
- Verification: `docker compose -f deploy/docker-compose.yml config` with the default port and with `INSPECTION_MINIO_PORT=9100` confirms the published port and all derived public endpoints match.
