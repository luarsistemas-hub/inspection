# Runbook — testes da Task 06 (autonomous-inspection-platform)

Este documento descreve como rastrear e executar a suíte da Task 06 (análise,
classificação, relatórios, painel e retenção). Os testes ficam no próprio
repositório; não existe corpus externo.

## Escopo

A Task 06 atribui 148 casos de `.compozy/tasks/autonomous-inspection-platform/_tests.md`:

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

Decisões posteriores alteraram o produto e o catálogo:

- o Gotenberg foi removido: o PDF é montado em processo pelo Maroto
  (`features/reports/render_pdf`). `UT-015` e `UT-016` foram readequados e
  `IT-374` e `IT-375` estão obsoletos (cobertura equivalente em `IT-263` e `IT-266`);
- o modo consolidado de relatório foi removido (migration 50, `report_mode`):
  `IT-271` e `IT-276` estão obsoletos;
- o frontend legado `apps/web` e o `services/contract` não existem mais.

## Rastreabilidade

Os identificadores deste catálogo colidem com os de outros workflows (por exemplo
`IT-428` também existe como teste de projeção em outra frente). Por isso:

1. um teste só conta quando o nome da função começa por `TestAIP_` e carrega o ID
   (`TestAIP_IT_241…`, `TestAIP_UT_034…`; intervalos como `TestAIP_IT_567ToIT576…`);
2. ele precisa estar na área cujo comportamento o ID define (por exemplo, `IT-423`
   é a query `report` e vive em `platform/graphql/resolvers`).

O manifesto executável é
[`harness.AutonomousTask06Cases()`](../internal/integration/harness/case_manifest_autonomous_task06.go).
Ele classifica cada ID como `COVERED` (com a área do teste), `PENDING` (com nota
de motivo e dono) ou `OBSOLETE` (com a decisão que o superou). O teste fail-fast

```sh
go test ./services/inspection/internal/integration/harness -run TestAutonomousTask06 -v
```

confere que os 148 IDs coincidem com `task_06.md` e que todo ID `COVERED` tem uma
função `TestAIP_` na área indicada; ele também mostra o balanço
(`assigned/covered/obsolete/pending`). `Task06AssignedCases()` (`case_manifest.go`)
lista casos de outro workflow e não deve ser usado para esta task.

`COVERED` é uma verificação mecânica do rótulo. Os casos rotulados por outras
frentes (alertas críticos `IT-291..300`, retenção `IT-301..310`, seeds `IT-321..340`)
são marcados com essa observação: a semântica é revisada por quem os implementou.

## Onde ficam os testes

| Casos | Área |
|---|---|
| UT-011, UT-012 | `internal/platform/llm` |
| UT-015, UT-016, IT-263, IT-266 | `internal/features/reports/render_pdf` |
| UT-034, UT-035, UT-067 | `internal/features/analysis/core` |
| UT-036, UT-037 (validação) | `internal/features/reports/core` |
| UT-037 (imutabilidade no PostgreSQL), IT-261, IT-262, IT-264, IT-265, IT-267, IT-270 | `internal/features/reports/generate_snapshot` (usa `get_report` e `download_pdf`) |
| UT-044 | `internal/features/retention/core` |
| UT-045 | `internal/features/retention/purge_data` |
| UT-048, UT-049 | `internal/features/dashboard/core` |
| UT-066 | `internal/features/analysis/process_comparison` |
| IT-241..IT-260 (análise PHOTO e classificação), IT-371..IT-373 (LiteLLM) | `internal/integration/harness` |
| IT-268, IT-423..IT-438, IT-527..IT-534 | `internal/platform/graphql/resolvers` (`task06_aip_transport_test.go`) |
| IT-561, IT-562, IT-567..IT-576, IT-583..IT-586 | `internal/platform/messaging` |

## 1. Subir as dependências

Na raiz do repositório:

```sh
./scripts/local.sh infra
docker compose -f deploy/docker-compose.yml ps
```

Os testes de integração usam PostgreSQL, RabbitMQ e MinIO. O LiteLLM dos casos
`IT-371..IT-373` é um servidor HTTP de teste compatível com a API do LiteLLM,
iniciado pelo próprio teste e exercitado pelo adaptador real `llm.HTTPGateway`;
o Maroto gera os PDFs em processo.

## 2. Configurar um banco descartável

Nunca rode `-tags integration` contra o banco principal `inspection`. Crie um banco
próprio e aponte as variáveis de teste para ele (o harness executa as migrações pela
conexão administrativa e usa a role de runtime, com RLS, nas asserções):

```sh
set -a; . ./.env.inspection; set +a
DB=inspection_task06_tmp
ADMIN=$(echo "$INSPECTION_MIGRATION_DATABASE_URL" | sed 's#/inspection?#/postgres?#')
psql "$ADMIN" -qc "DROP DATABASE IF EXISTS $DB" -c "CREATE DATABASE $DB"
export INSPECTION_TEST_DATABASE_URL="$(echo "$INSPECTION_MIGRATION_DATABASE_URL" | sed "s#/inspection?#/$DB?#")"
export INSPECTION_TEST_MIGRATION_DATABASE_URL="$INSPECTION_TEST_DATABASE_URL"
export INSPECTION_TEST_RUNTIME_DATABASE_URL="$(echo "$INSPECTION_DATABASE_URL" | sed "s#/inspection?#/$DB?#")"
export INSPECTION_TEST_MIGRATE=true
unset INSPECTION_DATABASE_URL INSPECTION_MIGRATION_DATABASE_URL INSPECTION_DISPATCHER_DATABASE_URL
```

Ao terminar, apague o banco (`DROP DATABASE`). Os endpoints de RabbitMQ e MinIO
têm padrões do Compose; sobrescreva apenas o que mudar
(`INSPECTION_TEST_RABBITMQ_URL`, `INSPECTION_TEST_MINIO_*`, `INSPECTION_TEST_TIMEOUT`,
`INSPECTION_TEST_POLL_INTERVAL`).

## 3. Executar

Testes unitários e o manifesto (não precisam de infraestrutura):

```sh
go test ./...
```

Casos de integração da Task 06, um pacote por vez (`-p 1`):

```sh
go test -tags integration -p 1 -count=1 -run 'TestAIP_' \
  ./services/inspection/internal/integration/harness \
  ./services/inspection/internal/features/reports/generate_snapshot
```

Para um caso: `-run 'TestAIP_IT_245'`. Execute também `go vet ./...` e `go build ./...`.

## 4. Critérios de aceite

A Task 06 só pode ser marcada como concluída quando:

- `INSPECTION_REQUIRE_COMPLETE_MANIFEST=true go test ./services/inspection/internal/integration/harness -run TestAutonomousTask06ManifestIsComplete`
  passar, isto é, nenhum caso estiver `PENDING` (todos `COVERED` ou `OBSOLETE` com nota);
- todos os testes `TestAIP_` passarem com exit code `0` (unitários e, com
  `-tags integration`, os de PostgreSQL/RLS, MinIO e o LiteLLM de teste);
- os logs da execução forem preservados como evidência;
- `go test ./...`, `go vet ./...` e `go build ./...` continuarem passando.

Enquanto houver casos `PENDING`, a Task 06 deve permanecer `pending`. No estado
atual os pendentes são `IT-269` (depende de o tipo `Report` expor publicação),
`IT-272..IT-275` e `IT-277..IT-280` (linha do tempo de projeto), `IT-281..IT-290`
(painel e triagem), `IT-304` e `IT-390` (retenção).
