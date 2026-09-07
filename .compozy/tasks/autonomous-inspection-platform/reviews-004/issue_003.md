---
round: 4
round_created_at: 2026-09-07T16:39:51.111341Z
status: resolved
file: scripts/local.sh
line: 107
severity: low
author: reviewer
---

# Issue 003: status exibe URLs incorretas para portas customizadas

## Review Comment

Depois de `INSPECTION_API_PORT` ou `INSPECTION_WEB_PORT` serem configuradas, `scripts/local.sh status` continua exibindo Frontend em3000 e GraphQL em8080 nas linhas107–109. Isso direciona operadores para endpoints errados durante diagnóstico. Gere essas URLs usando as variáveis carregadas pelo script.

## Triage

- Decision: `VALID`
- Root cause: `status` carrega o ambiente, mas imprimia portas literais (`3000` e `8080`) para Frontend, GraphQL e Health/ready.
- Fix: gerar essas URLs com `INSPECTION_WEB_PORT` e `INSPECTION_API_PORT`, usando os mesmos defaults do restante do script.
- Verification: smoke test com `INSPECTION_API_PORT=18080` e `INSPECTION_WEB_PORT=13000` confirmou as portas customizadas; outro smoke test confirmou os defaults `8080` e `3000`.
