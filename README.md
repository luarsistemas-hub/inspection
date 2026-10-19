# Inspection

Inspection é uma plataforma multi-tenant para planejar inspeções, coletar evidências, processar mídia e publicar relatórios auditáveis. O monorepo contém API GraphQL em Go, worker, scheduler, migrations, PostgreSQL com RLS, integrações locais e as aplicações Next.js Admin, Dashboard e Capture.

## Início rápido

Requisitos: Docker Desktop/Compose, Go 1.26.5, Node.js 22 e npm.

```sh
./scripts/local.sh init
./scripts/local.sh up
```

Abra <http://localhost:3000>. Configure as credenciais de bootstrap do Keycloak somente em `.env.inspection` antes de subir a stack. Para desenvolver no host, execute `./scripts/local.sh infra` e abra `./scripts/dev.sh api`, `worker`, `scheduler` e `all` em terminais separados.

Por padrão, `./scripts/local.sh up` também ativa o profile `video` e sobe o
worker de vídeo. Para subir a stack sem esse worker (por exemplo, ao testar
fluxos PHOTO isoladamente), use `INSPECTION_VIDEO_WORKER=0 ./scripts/local.sh up`.
O scratch de decodificação fica no volume Docker `inspection-video-scratch`;
`/tmp` continua disponível como tmpfs pequeno para arquivos temporários gerais.

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

## Primeiro acesso

Faça login no Keycloak e use `createTenant` para provisionar tenant, unidade inicial e membership `TENANT_ADMIN`. O exemplo está em [`docs/graphql.md`](docs/graphql.md). `healthz` indica processo vivo e `readyz` verifica somente banco/schema compatível.

Para desenvolvimento no host, deixe a infraestrutura ativa com `./scripts/local.sh infra` e execute `./scripts/dev.sh all`. Esse modo inicia API, worker, scheduler e os quatro frontends. Se quiser alterar somente o backend mantendo os frontends, execute `./scripts/dev.sh api` em um terminal e `./scripts/dev.sh all` em outro; o segundo comando reutiliza a API já disponível. Os demais componentes também podem ser iniciados individualmente.

Depois do onboarding, crie um cenário completo para QA manual (participante,
ativo, projeto, inspeção, notificação e convite de captura). O seed é executado
em container e faz o bootstrap do Admin automaticamente quando necessário:

```sh
./scripts/local.sh seed
```

Para validar vídeo somente na stack local, execute
`INSPECTION_QA_MEDIA_KIND=VIDEO INSPECTION_QA_ENABLE_VIDEO_ADMISSION=true ./scripts/local.sh seed`.
Essa opção habilita a admissão de vídeo no banco local e registra o motivo na
auditoria; o padrão continua desabilitado. Isso não substitui os gates de
admissão de produção, incluindo a validação em dispositivos físicos.

O seed aceita `INSPECTION_QA_SCENARIO=ORIGIN_VIDEO` para preparar um convite
descartável de origem em vídeo e `INSPECTION_QA_SCENARIO=FIXED_ORIGIN_VIDEO`
para criar uma O1 ativa com frames e scope sintéticos no MinIO. Ambos os
cenários só são aceitos com `INSPECTION_ENV=local`; o convite VIDEO requer
`INSPECTION_QA_ENABLE_VIDEO_ADMISSION=true`. A admissão continua desligada por
padrão. Para gerar, sob demanda e fora do Git, o clipe preto de limite, use
`./scripts/generate-video-boundary-clips.sh`; o script cria um diretório
temporário se nenhum diretório de saída for informado.

O comando imprime uma URL descartável do Capture. O código OTP é entregue no
Mailpit local em <http://localhost:8026>.

## Comandos

```sh
./scripts/local.sh status
./scripts/local.sh logs inspection-api
./scripts/local.sh migrate
./scripts/local.sh down
./scripts/local.sh down -v
./scripts/verify.sh
```

## Documentação

Consulte [`docs/README.md`](docs/README.md), [`deploy/README.md`](deploy/README.md), [`services/inspection/README.md`](services/inspection/README.md), [`scripts/README.md`](scripts/README.md) e [`libs/identity/README.md`](libs/identity/README.md). O schema GraphQL em `services/inspection/schema.graphqls` é a referência canônica; arquivos `generated` são verificados pela CI.
