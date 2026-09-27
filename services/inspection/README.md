# Inspection Service

| Executável | Responsabilidade |
| --- | --- |
| `inspection-api` | GraphQL, autenticação, autorização e endpoints operacionais |
| `inspection-worker` | dispatcher/consumer RabbitMQ e handlers assíncronos |
| `inspection-scheduler` | agendas, lembretes e retenção periódica |
| `inspection-migrate` | schema, RLS, roles e migrations versionadas |
| `inspection-prompt-seed` | cria o prompt `REAL_ESTATE` padrão; `-replace` publica o default embutido |

Features ficam em `internal/features`; plataforma compartilhada em `internal/platform`; eventos em `internal/contracts/events`. Execute `../../scripts/local.sh infra` e `../../scripts/dev.sh api` para desenvolvimento.

```sh
go test ./...
go vet ./...
go build ./...
go run github.com/99designs/gqlgen@v0.17.95 generate
```

Somente o migrador altera schema. Runtime usa `inspection_runtime`; worker usa `inspection_worker`.

O migrador só cria o prompt de análise quando ele não existe; alterações em
`internal/features/analysis/prompt/default_system_prompt.go` não chegam a
ambientes já seedados. Para publicá-las, rode `inspection-prompt-seed -replace`
(sobrescreve edições feitas pelo admin) ou atualize o prompt pela mutation
`updateAnalysisPrompt`. Inspeções existentes continuam fixadas no snapshot
anterior.
