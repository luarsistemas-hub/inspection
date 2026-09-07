---
round: 4
round_created_at: 2026-09-07T16:39:51.111341Z
status: resolved
file: scripts/dev.sh
line: 36
severity: medium
author: reviewer
---

# Issue 001: dev.sh valida uma porta e inicia em outra

## Review Comment

Com `INSPECTION_API_PORT=8180`, `scripts/dev.sh:36` verifica a porta8180, mas define `INSPECTION_HTTP_ADDR` como `:8080` e inicia o processo nessa porta. O processo host fica inacessível na porta configurada e pode falhar por conflito em8080. Derive o endereço padrão de `INSPECTION_API_PORT`, como já ocorre na checagem.

## Triage

- Decision: `VALID`
- Root cause: the API branch checked `${INSPECTION_API_PORT:-8080}` but independently defaulted `${INSPECTION_API_ADDR:-:8080}`, so a custom port changed only the preflight check and not the listener address.
- Fix: preserve an explicit `INSPECTION_API_ADDR` override and derive its fallback from `INSPECTION_API_PORT`, making the checked port and default listener consistent.
- Verification: no existing shell test suite owns `scripts/dev.sh`; the focused harness asserted `INSPECTION_HTTP_ADDR=:8180` for `INSPECTION_API_PORT=8180` and preserved `INSPECTION_HTTP_ADDR=127.0.0.1:9191` for an explicit address. `bash -n scripts/dev.sh`, `go test ./...`, `go vet ./...`, `go build ./...`, and `git diff --check` passed. The harness process could not connect to the unavailable local PostgreSQL instance after exporting the verified address.
