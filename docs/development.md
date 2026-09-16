# Desenvolvimento e testes

Prepare dependências e dados com `./scripts/local.sh infra` e `./scripts/local.sh seed`; use `./scripts/dev.sh all` para iniciar API, worker, scheduler e os quatro frontends no host. Para alterar somente o backend e manter os frontends, execute `./scripts/dev.sh api` em um terminal e `./scripts/dev.sh all` em outro; o segundo comando detecta e reutiliza a API já disponível. Os demais processos também podem ser iniciados individualmente. O seed usa uma API containerizada temporária e libera a porta `:8080` ao terminar. Os servidores locais usam `:8080`, `:8082`, `:8083`, `:3000`, `:3002`, `:3003` e `:3004`.

```sh
go test ./...
go vet ./...
go build ./...
(cd services/inspection && go run github.com/99designs/gqlgen@v0.17.95 generate)
for product in admin dashboard capture; do (cd "apps/$product" && npm run codegen:check && npm run lint && npm run test && npm run build); done
./scripts/verify.sh
```

Testes PostgreSQL exigem `INSPECTION_TEST_DATABASE_URL`; testes com tag `integration` usam Postgres, RabbitMQ e providers locais. O harness em `services/inspection/internal/integration/harness` oferece fixtures, test auth e esperas de outbox/inbox. Playwright exige navegadores (`npx playwright install chromium webkit`). A CI executa codegen, gofmt, vet, builds, auditoria npm, Vitest, Playwright, Compose e validação Compozy.

O diretório `internal/platform/graphql` na raiz contém artefatos GraphQL residuais e não é importado pelos executáveis de `services/inspection`; o serviço ativo usa seu próprio pacote interno e schema.