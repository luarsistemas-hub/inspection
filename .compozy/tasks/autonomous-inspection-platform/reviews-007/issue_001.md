---
round: 7
round_created_at: 2026-09-07T17:34:16.748386Z
status: resolved
file: .github/workflows/ci.yml
line: 58
severity: high
author: reviewer
---

# Issue 001: CI não inicia o worker exigido pelos E2E

## Review Comment

Em `.github/workflows/ci.yml:53-60`, o job de aceitação sobe apenas `inspection-api`. Porém os E2E dependem do `inspection-worker` separado para despachar a outbox, consumir RabbitMQ, enviar OTP/links e gerar relatórios. Isso faz os testes de captura e lifecycle falharem em um runner limpo. Suba o worker antes do Playwright.

## Triage

- Decision: `VALID`
- Root cause: o job `web` do workflow iniciava somente `inspection-api`, embora o
  Compose defina `inspection-worker` como o processo que despacha a outbox e
  consome os eventos assíncronos usados pelos fluxos de captura e lifecycle.
- Evidence: `deploy/docker-compose.yml` declara o serviço `inspection-worker`
  com o comando `inspection-worker`, as credenciais de dispatcher e a conexão
  RabbitMQ; `apps/web/tests/e2e/journeys.spec.ts` lê entregas no Mailpit e
  exercita captura/submissão que dependem desse processamento. O comando
  anterior do CI não selecionava o worker, então um runner limpo deixava esses
  efeitos assíncronos sem consumidor.
- Resolution: iniciar `inspection-api` e `inspection-worker` juntos no passo
  `Start acceptance services`. O Compose mantém a ordem via `depends_on`, e a
  espera existente de `/healthz` e `/readyz` continua garantindo que a API
  esteja pronta antes do Playwright.
- Regression coverage: o próprio job de aceitação é a suíte canônica para a
  integração entre API, worker, RabbitMQ e Mailpit; a configuração agora sobe
  explicitamente os dois processos antes de `npm run test:e2e`.
