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

`INSPECTION_TRUSTED_PROXY_CIDRS` é uma lista opcional de CIDRs separados por vírgula. O padrão vazio usa o IP da conexão e ignora cabeçalhos encaminhados. CIDRs inválidos impedem a inicialização. Em OCI, o `reverse_proxy` do Caddy envia `X-Forwarded-For` com o IP do cliente. Como o peer TCP direto da API é o container Caddy na rede Docker `app`, configure `INSPECTION_TRUSTED_PROXY_CIDRS=<IP_DO_CADDY>/32`, obtido do IP do serviço `caddy` em `docker network inspect inspection_app` (ou `docker inspect` no container). Reconfira após recriar o Caddy; mantenha a API acessível somente pela rede/proxy e não confie na sub-rede inteira, na VCN ou em faixas públicas.

O migrador só cria o prompt de análise quando ele não existe; alterações em
`internal/features/analysis/prompt/default_system_prompt.go` não chegam a
ambientes já seedados. Para publicá-las, rode `inspection-prompt-seed -replace`
(sobrescreve edições feitas pelo admin) ou atualize o prompt pela mutation
`updateAnalysisPrompt`. Inspeções existentes continuam fixadas no snapshot
anterior.

O catálogo estruturado v2 permanece desativado por padrão. Para ativá-lo, avalie
o corpus de fixtures sanitizadas pelo fluxo LiteLLM do produto, aprove o release
gate de qualidade e publique explicitamente uma nova definição versionada com
schema v2; ela valerá somente para análises futuras. Não altere snapshots
históricos. Até esse gate, novas análises usam o schema v1. Consulte as
[fixtures de avaliação](internal/features/analysis/process_inventory_job/testdata/checklist-v2/README.md).

Vistorias de saída por foto com escopo fixado (laudo v3) seguem regras estritas
de comparação: cada conclusão exige um par confirmado de evidências do mesmo
item e ambiente; a ausência de item móvel exige o ambiente registrado por
inteiro; item sem vista na referência aprovada, ou mostrado sem condição
avaliável, fica "sem base para comparar" (sem condição da referência). O modelo
só explica lacunas pela vista atual; "ambiente não capturado" e "falha na
análise" são registrados apenas pelo sistema. O job de cada ambiente lê o
requisito de captura associado à identidade da referência (chave do escopo no
laudo v3). Na criação, cada cômodo do escopo é ligado a no máximo um ambiente
da referência: primeiro pela chave com que o ambiente foi capturado
(`env:<chave do cômodo>`), depois por nome e tipo somente quando o casamento é
único nos dois sentidos. Ambiente da referência sem cômodo correspondente (ou
com mais de um candidato) não recebe requisito: seus itens saem "não
observados" com a limitação "ambiente não correspondido à referência"
(`ENVIRONMENT_UNMATCHED`), nunca "ambiente não capturado".

Decisão: uma vistoria de saída nova com modelo v1/v2 grava escopo `UNDEFINED`
("escopo não definido", sem cômodos) e também segue as regras estritas. Sem
definição de cobertura por cômodo não há como comprovar que o ambiente foi
registrado por inteiro, então a ausência de item móvel nunca é "ausência
comprovada": o item fica "não observado" (`ROOM_COVERAGE_INSUFFICIENT`) com a
limitação que cita o escopo não definido, e a classificação é "Requer atenção"
em vez de "Crítica". Itens fixos continuam podendo ter ausência comprovada com
par confirmado.

O laudo v3 traz `Report.comparison`/`CustomerReport.comparison` com
todos os itens da referência e contagens estáveis (filtro `comparisonOutcome`),
e a seção `inventory` repete os mesmos resultados e contagens, com o mesmo
vocabulário de resultados (`reportcore.ComparisonOutcomeLabel`) no HTML, no PDF,
em `CustomerInventoryEntry.statusLabel` e no Dashboard; laudos v1/v2 mantêm os
rótulos históricos (`statusLabel` nulo). A mutation
`reviseComparisonPairing` registra o pareamento decidido pela equipe e abre uma
nova geração do ambiente do item, que copia exatamente a geração anterior
(resultados e pares, inclusive os da equipe) e julga de novo só o item revisado,
decisões da equipe cuja análise falhou e itens apoiados em foto substituída.
Cada job estrito grava qual resposta enviada do ambiente leu
(`capture_answer_ref`); se o envio vigente do ambiente mudou desde a geração
anterior (recaptura enviada depois dela), nada é copiado e o ambiente inteiro
é julgado de novo. Job estrito que esgota as tentativas registra "falha na
análise" só para itens com base; itens sem vista na referência ficam "sem base
para comparar". A revisão emite a próxima versão do laudo sem alterar as anteriores. Vistorias legadas, de vídeo e com origem só de mídia
mantêm o comportamento histórico.

O template do laudo v3 foi atualizado sem incrementar `PDFRenderVersion`.
Esse campo não impede que artefatos já armazenados sejam servidos. Laudos v3
emitidos antes desta mudança mantêm o PDF emitido; sua apresentação só muda
quando uma nova versão é solicitada manualmente por `reissueReport`.
