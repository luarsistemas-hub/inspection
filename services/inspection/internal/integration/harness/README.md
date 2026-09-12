# Harness de integração externa

O harness fica dentro do serviço para reutilizar contratos de eventos, transações tenant, migrador e topologia RabbitMQ da produção.

Suba as dependências locais primeiro:

```sh
docker compose -f deploy/docker-compose.yml up -d postgres rabbitmq minio minio-setup mailpit twilio-fake meta-fake litellm-stub gotenberg-stub
```

Execute o gate com banco que já possui o schema:

```sh
INSPECTION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/inspection_task06_verify?sslmode=disable' \
INSPECTION_TEST_MIGRATE=false \
go test -tags=integration ./services/inspection/internal/integration/harness -run TestTask06HarnessSmoke -count=1
```

Fixtures tenant-scoped usam a role runtime não privilegiada exigida pelo RLS. Configure URL administrativa e URL runtime:

```sh
export INSPECTION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/inspection_task06_verify?sslmode=disable'
export INSPECTION_TEST_RUNTIME_DATABASE_URL='postgres://inspection_runtime:runtime-test@localhost:5432/inspection_task06_verify?sslmode=disable'
export INSPECTION_TEST_AUTH_ENABLED=true
export INSPECTION_ENV=test
```

`SeedTask06Fixture` cria tenant isolado, principal autorizado, capture submetido e PNG privado no MinIO. Use `Task06AuthHeaders`, `WaitOutbox`/`WaitInbox`/`WaitQueue` e `ResetProviderStubs`. Headers de test auth só são aceitos com `INSPECTION_ENV=test` e `INSPECTION_TEST_AUTH_ENABLED=true`.

O suite externo deve criar um harness por processo, declarar contratos de fila, semear via `WithinTenant`, publicar com `PublishPayload`, chamar GraphQL por `GraphQL` e aguardar com `Eventually`. Sobrescreva `INSPECTION_TEST_*` na CI; credenciais não ficam no código.

`Task06AssignedCases()` expõe as nove cases atribuídas a esta tarefa como manifesto fail-fast: IT-047 a IT-050 e E2E-009/E2E-012 a E2E-015. O runner externo ainda precisa registrar implementação para cada ID.
