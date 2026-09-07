---
round: 5
round_created_at: 2026-09-07T16:54:40.800467Z
status: resolved
file: scripts/local.sh
line: 91
severity: medium
author: reviewer
---

# Issue 003: infra retorna antes de migrations e bootstrap terminarem

## Review Comment

`scripts/local.sh:89-92` executa `docker compose up -d` incluindo `inspection-bootstrap`, `inspection-migrate` e `inspection-runtime-bootstrap`, mas retorna imediatamente sem aguardar os serviços one-shot concluírem. O fluxo documentado manda executar `infra` e depois iniciar API/worker/scheduler no host; nesse intervalo os processos podem iniciar sem roles ou schema prontos e falhar, embora o comando já tenha reportado o início como concluído. Aguarde explicitamente os serviços one-shot concluírem com sucesso antes de retornar.

## Triage

- Decision: `VALID`
- Root cause: `infra` detached the Compose graph and returned immediately, so its
  `service_completed_successfully` dependency conditions only controlled startup
  ordering and did not make the shell command wait for the bootstrap, migration,
  and runtime-bootstrap containers to finish.
- Evidence: `deploy/docker-compose.yml` defines the three one-shot services and
  their completion dependencies, while the previous `infra` branch only called
  `compose up -d` and printed that the jobs had been started.
- Fix: `infra` now waits for each one-shot container to reach `exited`, checks its
  exit code, reports successful completion, and fails with the affected service and
  log command when completion times out or the service exits nonzero.
- Regression coverage: `bash -n scripts/local.sh` and an isolated Docker CLI
  stub exercised both successful completion of all three one-shot services and
  nonzero failure propagation (`inspection-bootstrap__, exit code 7); the shell
  has no existing dedicated test suite for `local.sh`.
