# Harness de integração externa

O harness fica dentro do serviço para reutilizar contratos de eventos, transações tenant, migrador e topologia RabbitMQ da produção.

Suba as dependências locais primeiro:

```sh
docker compose -f deploy/docker-compose.yml up -d postgres rabbitmq minio minio-setup mailpit twilio-fake meta-fake litellm-stub
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
export INSPECTION_TEST_RUNTIME_DATABASE_URL='postgres://inspection_runtime:inspection-runtime-local@localhost:5432/inspection_task06_verify?sslmode=disable'
export INSPECTION_TEST_AUTH_ENABLED=true
export INSPECTION_ENV=test
```

A role `inspection_runtime` precisa estar provisionada com a senha indicada na URL antes de rodar os testes. O harness não altera a senha dessa role compartilhada com a API local.

`SeedTask06Fixture` cria tenant isolado, principal autorizado, capture submetido e PNG privado no MinIO. Por padrão, o template e a captura usam schema 2, chave `env:new:<uuid>` e requisito de ambiente; passe `SchemaVersion: 1, RequirementKey: "room-1"` quando um teste ainda precisar fixar o contrato legado. `SeedInventoryBaseline` cria uma origem ACTIVE `MOVE_IN` com inventário revisado, ambiente, item e evidência mínima. `SeedVideoEnvironmentFixture` cria uma vistoria VIDEO schema 2 com clipe associado ao ambiente e frame preparado com scope. Use `Task06AuthHeaders`, `WaitOutbox`/`WaitInbox`/`WaitQueue` e `ResetProviderStubs`. Headers de test auth só são aceitos com `INSPECTION_ENV=test` e `INSPECTION_TEST_AUTH_ENABLED=true`.

`SeedVideoOriginFixture(ctx, VideoOriginFixtureOptions{Active: true})` cria uma
origem VIDEO ativa, preparação READY, frame real no MinIO com SHA-256 conferível
e scope item associado. Use `Active: false` quando o teste precisar preparar a
transição de ativação.

O suite externo deve criar um harness por processo, declarar contratos de fila, semear via `WithinTenant`, publicar com `PublishPayload`, chamar GraphQL por `GraphQL` e aguardar com `Eventually`. Sobrescreva `INSPECTION_TEST_*` na CI; credenciais não ficam no código.

`Task06AssignedCases()` expõe as oito cases atribuídas a esta tarefa como manifesto fail-fast: IT-047 a IT-049 e E2E-009/E2E-012 a E2E-015. O runner externo ainda precisa registrar implementação para cada ID.

## PHOTO sem o worker de vídeo

O teste `TestPhotoComparisonSchedulingDoesNotDependOnVideoPreparation` usa
PostgreSQL/RLS e o harness de eventos para criar uma inspeção PHOTO
`CURRENT_ONLY`, processar `capture.submitted.v1` pela slice de comparação
comum e confirmar que o job/outbox PHOTO é criado sem nenhum plano de vídeo.
Ele pode ser executado com o banco descartável padrão do repositório:

```sh
/private/tmp/claude-502/-Users-Payface-Documents-elvio-projetos-GoLang-inspection/a4f647a6-62e8-454b-98ce-03bc20d74f4f/scratchpad/codex/itest.sh photo_worker_stopped -run TestPhotoComparisonSchedulingDoesNotDependOnVideoPreparation ./services/inspection/internal/integration/harness
```

Para executar a jornada Playwright PHOTO existente sem iniciar o container de
vídeo, use `./scripts/e2e-photo-with-video-worker-stopped.sh`. O script define
`INSPECTION_VIDEO_WORKER=0`, sobe a stack, falha se o container estiver ativo e
executa a jornada autenticada E2E-032 no Capture. Essa jornada valida captura e
submissão PHOTO; a jornada ampliada E2E-025 de recaptura e relatório ainda não
está implementada no Playwright.
