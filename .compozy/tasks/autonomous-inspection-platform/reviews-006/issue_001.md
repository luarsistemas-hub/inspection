---
round: 6
round_created_at: 2026-09-07T17:14:07.382907Z
status: resolved
file: deploy/docker-compose.yml
line: 211
severity: high
author: reviewer
---

# Issue 001: Compose mantém a API do navegador na porta8080

## Review Comment

Com `INSPECTION_API_PORT=8180`, a configuração publica a API em `8180`, mas `deploy/docker-compose.yml:211` e `:224` mantêm `NEXT_PUBLIC_INSPECTION_API_URL` em `http://localhost:8080/graphql`. A reprodução retornou `published_api=8180 web_api=http://localhost:8080/graphql`. O navegador chama uma porta sem a API. Derive o endpoint usando `INSPECTION_API_PORT`, preservando override explícito; o callback Twilio em `:176` também deve acompanhar a porta configurada.

## Triage

- Decision: `VALID`
- Root cause: the API host port is configurable through `INSPECTION_API_PORT`, but both
  frontend endpoint injections and the Twilio callback URL used a literal host port `8080`.
  With `INSPECTION_API_PORT=8180`, Compose therefore published the API on `8180` while the
  browser and callback still targeted `8080`.
- Fix: derive the default frontend GraphQL endpoint and Twilio callback URL from
  `INSPECTION_API_PORT`, while retaining the existing explicit environment-variable overrides.
- Verification owner: `docker compose config`, checking default, custom-port, and explicit-
  override resolutions.
