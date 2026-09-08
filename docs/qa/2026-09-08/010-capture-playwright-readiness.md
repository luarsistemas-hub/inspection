# QA: corrigir readiness do servidor Playwright do Capture

## Severidade

Média — a suíte E2E oficial do Capture não chega a executar os testes.

## Evidência

`apps/capture/playwright.config.ts` usa `http://localhost:3003` como URL do
`webServer`, mas o produto expõe somente `/capture/[linkToken]`. Com a porta
livre, o Playwright inicia o Next.js e encerra após 60 segundos aguardando uma
resposta válida na raiz. Com o container Docker já ativo, a mesma checagem não
reconhece o servidor e uma segunda instância falha com `EADDRINUSE`.

## Esperado

`npm run test:e2e` deve reconhecer um servidor existente ou iniciar o servidor
de teste e executar a suíte em ambiente limpo e com a stack local já ativa.

## Critérios de aceite

- Configurar uma URL de readiness que responda com sucesso ou adicionar um
  health endpoint estável ao Capture.
- Validar execução com a porta livre e com `reuseExistingServer` habilitado.
- `npm run test:e2e` executa os projetos Android e WebKit sem timeout.
- Manter trace e screenshot para falhas reais de teste.
