# Runbook — testes canônicos da Task 06

Este documento descreve como executar a suíte oficial atribuída à Task 06.

## Escopo

A Task 06 possui 148 casos atribuídos:

- 14 unitários: `UT-011`, `UT-012`, `UT-015`, `UT-016`, `UT-034`, `UT-035`, `UT-036`, `UT-037`, `UT-044`, `UT-045`, `UT-048`, `UT-049`, `UT-066`, `UT-067`;
- 134 integrações:
  - `IT-241..IT-310`;
  - `IT-321..IT-340`;
  - `IT-371..IT-375`;
  - `IT-390`;
  - `IT-423..IT-430`;
  - `IT-433..IT-438`;
  - `IT-527..IT-534`;
  - `IT-561..IT-562`;
  - `IT-567..IT-576`;
  - `IT-583..IT-586`.

O manifesto executável está em
[`harness.Task06AssignedCases()`](../internal/integration/harness/case_manifest.go).
Ele valida a atribuição e a contagem, mas não substitui os testes oficiais.

## 1. Fornecer o corpus oficial

Antes da execução, disponibilize:

1. os arquivos oficiais dos testes;
2. a origem do corpus (ZIP, caminho local ou repositório Git);
3. o commit, tag ou checksum usado;
4. todos os IDs atribuídos, sem casos ausentes ou renomeados.

Coloque os testes dentro do módulo, preferencialmente em:

```text
services/inspection/internal/integration/task06_external/
```

Essa localização permite importar o pacote interno `harness`. A suíte deve
registrar uma implementação para cada ID retornado por
`harness.Task06AssignedCases()` e falhar se algum caso não estiver registrado.

## 2. Subir as dependências

Na raiz do repositório:

```sh
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml ps
```

Os testes dependem de PostgreSQL, RabbitMQ, MinIO, LiteLLM stub e Gotenberg
stub. Os stubs são determinísticos e não exigem credenciais de provedores
externos.

## 3. Configurar o banco de teste

Use um banco dedicado. Se ele ainda não existir, crie-o com um usuário
administrativo do PostgreSQL:

```sql
CREATE DATABASE inspection_task06_verify;
```

Configure a conexão administrativa e a conexão runtime não privilegiada:

```sh
export INSPECTION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/inspection_task06_verify?sslmode=disable'
export INSPECTION_TEST_RUNTIME_DATABASE_URL='postgres://inspection_runtime:runtime-test@localhost:5432/inspection_task06_verify?sslmode=disable'
export INSPECTION_TEST_MIGRATE=true
```

O harness executa as migrações pela conexão administrativa, configura a senha
do papel `inspection_runtime` e executa as asserções tenant-scoped pela conexão
runtime, com RLS.

## 4. Configurar API e autenticação de teste

Para casos GraphQL, use o modo de teste explicitamente:

```sh
export INSPECTION_ENV=test
export INSPECTION_TEST_AUTH_ENABLED=true
export INSPECTION_TEST_API_URL='http://localhost:8080'
```

Os headers de identidade de teste só são aceitos quando essas duas variáveis
estão ativas. Nunca habilite essa autenticação fora de um ambiente de teste.

## 5. Sobrescrever endpoints, se necessário

Os padrões do Compose são suficientes para execução local. Em CI ou ambiente
efêmero, sobrescreva apenas o que mudar:

```sh
export INSPECTION_TEST_RABBITMQ_URL='amqp://inspection:inspection@localhost:5672/'
export INSPECTION_TEST_LITELLM_URL='http://localhost:18080'
export INSPECTION_TEST_GOTENBERG_URL='http://localhost:18081'
export INSPECTION_TEST_MINIO_ENDPOINT='localhost:9000'
export INSPECTION_TEST_MINIO_ACCESS_KEY='contract'
export INSPECTION_TEST_MINIO_SECRET_KEY='contract'
export INSPECTION_TEST_MINIO_BUCKET='inspection-private'
```

O harness também aceita `INSPECTION_TEST_MIGRATION_DATABASE_URL`,
`INSPECTION_TEST_TIMEOUT` e `INSPECTION_TEST_POLL_INTERVAL`.

## 6. Iniciar os processos da aplicação

A suíte pode iniciar API, worker e scheduler com seu próprio executor de
processos ou em terminais separados usando os mesmos valores de ambiente:

```sh
go run ./services/inspection/cmd/inspection-api
go run ./services/inspection/cmd/inspection-worker
go run ./services/inspection/cmd/inspection-scheduler
```

O harness fornece `StartProcess`, `WaitReady`, `Output` e `Close` para o runner
que preferir controlar o ciclo de vida automaticamente.

## 7. Executar a suíte

Primeiro valide somente a infraestrutura:

```sh
go test -tags=integration -count=1 \
  ./services/inspection/internal/integration/harness \
  -run 'TestTask06HarnessSmoke|TestTask06FixtureSeedsCompleteGraph'
```

Depois execute o corpus completo:

```sh
go test -tags=integration -count=1 ./...
```

O runner deve usar os recursos do harness:

- `SeedTask06Fixture` para dados isolados;
- `WithinTenant` para consultas com RLS;
- `Task06AuthHeaders` para GraphQL;
- `PublishPayload` para eventos canônicos;
- `WaitOutbox`, `WaitInbox` e `WaitQueue` para eventual consistency;
- `ResetProviderStubs` entre cenários LiteLLM/Gotenberg.

## 8. Critérios de aceite

A Task 06 só pode ser marcada como concluída quando:

- os 148 IDs forem descobertos e executados;
- nenhum caso estiver ausente, ignorado ou marcado como placeholder;
- todos os testes passarem com exit code `0`;
- Postgres/RLS, RabbitMQ, MinIO, LiteLLM e Gotenberg tiverem sido usados no
  ambiente de integração;
- os logs da execução forem preservados como evidência;
- `go test ./...`, `go vet ./...` e `go build ./...` continuarem passando.

Enquanto o corpus oficial não for fornecido, o repositório está preparado para
a execução, mas a Task 06 deve permanecer `pending`.
