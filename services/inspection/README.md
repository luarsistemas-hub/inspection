# Inspection Service

| Executável | Responsabilidade |
| --- | --- |
| `inspection-api` | GraphQL, autenticação, autorização e endpoints operacionais |
| `inspection-worker` | dispatcher/consumer RabbitMQ e handlers assíncronos |
| `inspection-video-worker` | validação, screening e preparação durável de vídeos com FFmpeg/ffprobe em container dedicado |
| `inspection-scheduler` | agendas, lembretes e retenção periódica |
| `inspection-migrate` | schema, RLS, roles e migrations versionadas |
| `inspection-prompt-seed` | cria o prompt `REAL_ESTATE` padrão; `-replace` publica o default embutido |

Features ficam em `internal/features`; plataforma compartilhada em `internal/platform`; eventos em `internal/contracts/events`. Execute `../../scripts/local.sh infra` e `../../scripts/dev.sh api` para desenvolvimento.

Para saúde, métricas, operações de preparação, retenção e recuperação do worker de vídeo, consulte o [runbook operacional de vídeo](../../docs/runbooks/video.md).

```sh
go test ./...
go vet ./...
go build ./...
```

Após alterar o schema ou os documentos GraphQL de qualquer frontend, execute
`../../scripts/generate-graphql-contracts.sh` a partir deste diretório e inclua
os arquivos gerados no commit. O comando atualiza o código do gqlgen e
`operation-manifest.json`; a CI executa o mesmo comando e rejeita diferenças.

Somente o migrador altera schema. Runtime usa `inspection_runtime`; worker usa `inspection_worker`.

`INSPECTION_SKIP_SENSITIVE_CONTENT_VALIDATION` desativa apenas a detecção de conteúdo sensível, mantendo as demais validações. O padrão é `false`; valores fora de `true/false/1/0/yes/no/on/off` impedem a inicialização. Consulte o [runbook operacional de vídeo](../../docs/runbooks/video.md) para detalhes do bypass e da configuração efetiva dos workers.

O migrador só cria o prompt de análise quando ele não existe; alterações em
`internal/features/analysis/prompt/default_system_prompt.go` não chegam a
ambientes já seedados. Para publicá-las, rode `inspection-prompt-seed -replace`
(sobrescreve edições feitas pelo admin) ou atualize o prompt pela mutation
`updateAnalysisPrompt`. Inspeções existentes continuam fixadas no snapshot
anterior.
