---
round: 7
round_created_at: 2026-09-07T17:34:16.748386Z
status: resolved
file: scripts/dev.sh
line: 44
severity: medium
author: reviewer
---

# Issue 002: dev.sh web usa API fixa ao customizar a porta

## Review Comment

Com `INSPECTION_API_PORT=8180`, `scripts/dev.sh:36` inicia a API em `:8180`, mas o fluxo `web` preserva `NEXT_PUBLIC_INSPECTION_API_URL=http://localhost:8080/graphql` carregada do `.env.example`. O navegador chama a porta antiga. Aplique no `dev.sh` a mesma derivação de URL usada pelo Compose.

## Triage

- Decision: `VALID`
- Root cause: `scripts/dev.sh` carregava `NEXT_PUBLIC_INSPECTION_API_URL` do
  arquivo de ambiente, mas não aplicava a derivação para
  `INSPECTION_API_PORT` diferente de `8080`. Assim, o processo web podia
  iniciar junto de uma API em uma porta customizada e ainda apontar o
  navegador para a porta padrão.
- Evidence: antes da alteração, `scripts/local.sh` já convertia o valor
  padrão `http://localhost:8080/graphql` para
  `http://localhost:${INSPECTION_API_PORT}/graphql`, enquanto `dev.sh` não
  tinha essa regra. O Compose usa a mesma relação entre a porta publicada e
  o endpoint público.
- Resolution: aplicar no carregamento de ambiente de `dev.sh` a mesma
  derivação condicional, alterando apenas o valor padrão e preservando uma
  URL explicitamente customizada.
- Regression coverage: `scripts/dev_test.sh` executa o fluxo `web` com
  `INSPECTION_API_PORT=8180` e confirma que o processo recebe
  `http://localhost:8180/graphql`.
