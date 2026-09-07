---
round: 6
round_created_at: 2026-09-07T17:14:07.382907Z
status: resolved
file: .github/workflows/ci.yml
line: 34
severity: high
author: reviewer
---

# Issue 003: CI executa E2E sem iniciar API e Keycloak

## Review Comment

O job web em `.github/workflows/ci.yml:34-53` instala dependências, constrói o frontend e executa Playwright, mas não inicia Compose, API, PostgreSQL ou Keycloak. Os testes chamam `page.goto('/')`, esperam o login OIDC e enviam GraphQL para `localhost:8080` (`apps/web/tests/e2e/journeys.spec.ts:102-119`), enquanto `playwright.config.ts:12` inicia somente o Next.js. Assim, os34 contratos E2E não exercitam superfícies reais e falham em um runner limpo. Suba a stack necessária antes do E2E ou configure serviços equivalentes e aguarde readiness.

## Triage

- Decision: `VALID`
- Notes: O job `web` executava os testes E2E em um runner limpo sem iniciar os serviços externos que os testes acessam. O Playwright inicia somente o Next.js; os testes fazem login pelo Keycloak, enviam operações GraphQL para `localhost:8080` e usam a entrega de e-mail do Mailpit. O compose e `scripts/local.sh` já fornecem a topologia e os waits necessários: `infra` prepara dependências, bootstrap e migrations, e `inspection-api` depende de Keycloak, RabbitMQ e MinIO saudáveis. A correção inicia a infraestrutura, sobe e constrói `inspection-api`, aguarda `healthz` e `readyz` antes do Playwright e sempre derruba a stack ao final. O próprio conjunto E2E em `apps/web/tests/e2e/journeys.spec.ts` é a suíte canônica do contrato; não é necessário um teste adicional para YAML de CI.
