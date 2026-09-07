---
round: 5
round_created_at: 2026-09-07T16:54:40.800467Z
status: resolved
file: scripts/dev.sh
line: 38
severity: medium
author: reviewer
---

# Issue 002: dev.sh ignora portas customizadas do worker, scheduler e web

## Review Comment

`scripts/dev.sh:38` verifica `INSPECTION_WORKER_PORT`, mas exporta sempre `INSPECTION_HTTP_ADDR=:8082`; `:42` repete o problema com o scheduler em `:8083`. O caminho `web` (`:43-46`) também executa `npm run dev` sem passar `INSPECTION_WEB_PORT`, portanto o Next inicia na porta padrão3000. Reproduzido com `INSPECTION_WORKER_PORT=8182`: o script imprime `ADDR=:8082`. Faça cada processo escutar a porta configurada e valide a porta web antes de iniciar.

## Triage

- Decision: `VALID`
- Root cause: `worker` and `scheduler` check their configured ports but replace the resulting HTTP address with hard-coded `:8082` and `:8083`; `web` neither checks `INSPECTION_WEB_PORT` nor forwards it to Next.js, so the dev server falls back to 3000.
- Fix: derive the worker and scheduler default addresses from their corresponding port variables, check the configured web port before dependency installation/startup, and pass that port to Next.js with `npm run dev -- --port`.
- Regression coverage: shell syntax validation plus static command assertions and isolated execution checks for the generated worker, scheduler, and web command arguments. No existing script test suite owns this invariant.
