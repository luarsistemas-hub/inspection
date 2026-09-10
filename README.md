# Inspection

Inspection é uma plataforma multi-tenant para planejar inspeções, coletar evidências, processar mídia e publicar relatórios auditáveis. O monorepo contém API GraphQL em Go, worker, scheduler, migrations, PostgreSQL com RLS, integrações locais e as aplicações Next.js Admin, Dashboard e Capture.

## Início rápido

Requisitos: Docker Desktop/Compose, Go 1.26.5, Node.js 22 e npm.

```sh
./scripts/local.sh init
./scripts/local.sh up
```

Abra <http://localhost:3000>. Configure as credenciais de bootstrap do Keycloak somente em `.env.inspection` antes de subir a stack. Para desenvolver no host, execute `./scripts/local.sh infra` e abra `./scripts/dev.sh api`, `worker`, `scheduler` e `all` em terminais separados.

## Componentes e URLs

| Componente | URL/porta |
| --- | --- |
| API GraphQL | <http://localhost:8080/graphql> |
| Frontend | <http://localhost:3000> |
| Keycloak | <http://localhost:8081> |
| PostgreSQL | `localhost:5433` |
| RabbitMQ | `localhost:5673`, painel `15673` |
| Dragonfly | `localhost:6380` |
| MinIO | API `9002`, console `9003` |
| Mailpit | <http://localhost:8026> |
| Gotenberg | <http://localhost:3001> |

## Primeiro acesso

Faça login no Keycloak e use `createTenant` para provisionar tenant, unidade inicial e membership `TENANT_ADMIN`. O exemplo está em [`docs/graphql.md`](docs/graphql.md). `healthz` indica processo vivo e `readyz` verifica somente banco/schema compatível.

Depois do onboarding, crie um cenário completo para QA manual (participante,
ativo, projeto, inspeção, notificação e convite de captura). O seed é executado
em container e faz o bootstrap do Admin automaticamente quando necessário:

```sh
./scripts/local.sh seed
```

O comando imprime uma URL descartável do Capture. O código OTP é entregue no
Mailpit local em <http://localhost:8026>.

## Comandos

```sh
./scripts/local.sh status
./scripts/local.sh logs inspection-api
./scripts/local.sh migrate
./scripts/local.sh down
./scripts/verify.sh
```

## Documentação

Consulte [`docs/README.md`](docs/README.md), [`deploy/README.md`](deploy/README.md), [`services/inspection/README.md`](services/inspection/README.md), [`scripts/README.md`](scripts/README.md) e [`libs/identity/README.md`](libs/identity/README.md). O schema GraphQL em `services/inspection/schema.graphqls` é a referência canônica; arquivos `generated` são verificados pela CI.
