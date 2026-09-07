---
round: 6
round_created_at: 2026-09-07T17:14:07.382907Z
status: resolved
file: scripts/local.sh
line: 138
severity: medium
author: reviewer
---

# Issue 004: Diagnóstico exibe portas fixas de infraestrutura

## Review Comment

`scripts/local.sh:138-141` sempre informa MinIO `9002/9003`, RabbitMQ `15673` e Mailpit `8026`, embora o Compose permita configurar essas portas em `deploy/docker-compose.yml:41-42`, `:247-248` e `:260-261`. Com portas customizadas, `status` direciona o operador para endpoints incorretos durante diagnóstico. Gere essas URLs a partir das variáveis carregadas.

## Triage

- Decision: `VALID`
- Root cause: the `status` branch hard-codes the host ports for MinIO, its console, RabbitMQ management, and Mailpit even though `load_env` loads the corresponding `INSPECTION_*_PORT` variables and the Compose file uses them for host bindings.
- Fix: interpolate the loaded variables with the same defaults as `deploy/docker-compose.yml` when rendering each diagnostic URL.
- Verification: `bash -n scripts/local.sh` plus a controlled `status` execution with custom values for all four variables confirmed that the printed URLs use those values.
