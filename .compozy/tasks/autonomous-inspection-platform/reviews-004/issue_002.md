---
round: 4
round_created_at: 2026-09-07T16:39:51.111341Z
status: resolved
file: deploy/docker-compose.yml
line: 228
severity: medium
author: reviewer
---

# Issue 002: Runtime do web ignora storage customizado

## Review Comment

O Compose aceita `NEXT_PUBLIC_STORAGE_URL` como argumento de build em `deploy/docker-compose.yml:215`, mas fixa `NEXT_PUBLIC_STORAGE_URL: http://localhost:9002` no ambiente runtime em `:228`. Com um endpoint customizado, `next.config.ts` é construído com uma origem e `middleware.ts` calcula a CSP com outra, podendo bloquear uploads ou requests ao storage. Propague a mesma variável resolvida para build e runtime.

## Triage

- Decision: `VALID`
- Root cause: the `web` service resolved `NEXT_PUBLIC_STORAGE_URL` from the
  environment for the image build, but replaced it with a fixed localhost
  endpoint in the container runtime environment. This allows the Next.js
  middleware CSP and the runtime configuration to disagree when a custom
  storage endpoint is supplied.
- Fix: resolve `NEXT_PUBLIC_STORAGE_URL` with the same default interpolation in
  the runtime environment as in `build.args`, preserving explicit overrides.
- Verification: `docker compose -f deploy/docker-compose.yml config --quiet`
  passes, and a custom-value config inspection confirms both the build argument
  and runtime environment resolve to the same endpoint.
