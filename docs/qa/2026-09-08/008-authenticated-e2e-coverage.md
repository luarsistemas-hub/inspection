# QA: suítes E2E não exercitam autenticação e operações reais

## Severidade

Média — regressões críticas chegam ao ambiente mesmo com Playwright verde.

## Evidência

Os testes atuais do Admin verificam apenas o guard sem sessão; o Dashboard tem
um único teste do boundary de autenticação; o Capture mocka GraphQL para um
link inválido. Nenhum deles detecta as falhas encontradas nesta rodada.

## Esperado

A suíte deve provisionar dados, autenticar no Keycloak local e cobrir os fluxos
reais dos três produtos com console, page errors e respostas GraphQL validadas.

## Critérios de aceite

- Fixture global chama `./scripts/local.sh seed` e captura a URL descartável.
- Projetos Playwright separados para Admin, Dashboard e Capture/mobile.
- Falhar em console/page errors inesperados e em GraphQL com `errors`.
- Cobrir consultas Admin, triagem/detalhes/ações Dashboard e OTP/upload/impossibilidade/submit Capture.
- Preservar trace e screenshot em falhas.
- Manter o readiness de cada `webServer` em uma rota que responda com sucesso.
