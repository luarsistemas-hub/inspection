# Runbook de anotações por voz

## Estado do rollout

**Bloqueado para produção: mantenha anotações por voz desabilitadas.** As policies
novas têm `enabled=false` por padrão e os limites positivos continuam sendo
obrigatórios. Não foi feita alteração em política de produção; o estado de tenants
existentes em produção não foi consultado nesta execução.

O worker dedicado está configurado para desenvolvimento local. A composição
usa fila exclusiva, PostgreSQL, armazenamento privado e aliases LiteLLM de áudio;
isso não representa aprovação dos gates físicos ou de modelo.

## Subir e verificar o worker local

Com a infraestrutura local já inicializada, habilite o perfil dedicado:

```sh
INSPECTION_TRANSCRIPTION_WORKER=1 ./scripts/local.sh up
```

O perfil é gravado no job pela admissão conforme o catálogo; não configure
`INSPECTION_TRANSCRIPTION_PROFILE` no worker. O worker respeita o perfil
imutável registrado no job. Os aliases de transcrição usam timeout individual
de 40s, abaixo dos 45s do cliente HTTP; o lease padrão é 73s.

Na inicialização, antes de abrir qualquer dependência, o worker valida
`INSPECTION_TRANSCRIPTION_PROFILE` (quando informado) e exige
`lease mínimo ≤ INSPECTION_TRANSCRIPTION_LEASE < 5m`, com
`lease mínimo = detector 10s + INSPECTION_TRANSCRIPTION_TIMEOUT + download 10s + persistência 3s + margem 5s`
(73 s no padrão, com timeout de 45 s), porque as etapas rodam em sequência.
A fórmula tem uma única fonte, `process_note.RequiredLease`; sem
`INSPECTION_TRANSCRIPTION_LEASE`, o worker usa exatamente esse mínimo. Depois
que o gateway responde, todas as gravações restantes (staging, ledger e
conclusão ou falha) compartilham um único orçamento de persistência de 3 s,
coberto pela margem de 5 s do lease.
Configuração inválida encerra o processo com
`inspection-transcription-worker exited: INVALID_TRANSCRIPTION_CONFIG`, sem
afetar API, worker geral ou worker de vídeo. Os demais códigos de saída são
`STARTUP_FAILED` (dependência indisponível na inicialização),
`BROKER_CONNECTION_LOST` (o RabbitMQ encerrou o consumo depois de pronto) e
`WORKER_STOPPED` (falha do servidor HTTP ou encerramento além do prazo).

A API também depende do `ffprobe` para medir a duração do áudio enviado. As
imagens da API (`local` e `prebuilt` em `services/inspection/Dockerfile` e
`default` em `deploy/oci/Go.Dockerfile`) instalam o FFmpeg e falham no build se
`ffprobe -version` não executar.

Verifique `http://localhost:8092/healthz` para liveness e
`http://localhost:8092/readyz` para readiness (compatibilidade do schema,
PostgreSQL e conexão RabbitMQ). `./scripts/local.sh status` lista a URL; use
`./scripts/local.sh logs inspection-transcription-worker` para consultar logs.

O worker consome `inspection-transcription-requested` para
`transcription.requested.v1`. As notificações `transcription.completed.v1` e
`transcription.failed.v1` permanecem no outbox; seus consumers e o recálculo
do manifest/prazo do catálogo pertencem à task_05 e ainda não estão ativos.
O contrato de entrada usa retentativas e uma fila `.dlq` após esgotamento.
No SIGTERM, o consumer para novas entregas e o trabalho ativo continua até o
limite seguro do lease, seguido pelo shutdown gracioso do processo.
Um claim com lease ainda válido (`ErrLeaseActive`) é reenfileirado na fila
`.retry.5m`; estados terminais, já concluídos ou com token de lease superado
recebem ack sem nova chamada ao modelo. O desligamento aguarda até lease + 5 s
sobre o lease efetivo (78 segundos no padrão) e o Compose define
`INSPECTION_TRANSCRIPTION_LEASE: 73s` e `stop_grace_period: 85s` para o
Docker não enviar SIGKILL antes disso. Se o lease for aumentado, aumente o
`stop_grace_period` na mesma proporção. O deploy OCI ainda não declara o
serviço do worker de transcrição; ao incluí-lo, replique esse prazo.

Este gate é operacional/documental neste estágio: o Admin ainda não consome um
registro automatizado de DEVICE/MODEL. A aprovação precisa ser registrada e
verificada pela equipe responsável pelo rollout antes de habilitar a política
em produção.

## Pausar novas anotações

1. No Admin, abra **Anotações por voz** e desmarque **Habilitar novas anotações
   por voz**.
2. Salve a política e confirme a mensagem de sucesso e a versão retornada.
3. Confira por uma nova consulta que o estado está desabilitado. Não apague nem
   cancele anotações aceitas: a política bloqueia novas admissões e não deve
   invalidar trabalhos aceitos.

Se a API ou o Admin não estiverem disponíveis, interrompa o rollout e bloqueie
novas admissões no ponto de entrada operacional já implantado. Não altere
diretamente o banco para contornar a API.

## Recuperar falhas e limpeza

- Consulte as filas de entrada, retentativas e DLQ no RabbitMQ. Recuperação de
  leases e reconciliação de limpeza dependem do scheduler; não simule recuperação
  por edição direta do banco.
- O scheduler executa, por padrão a cada minuto (`INSPECTION_TRANSCRIPTION_SWEEP_INTERVAL`,
  entre `1m` e `1h`), as varreduras de anotações por voz: lease vencido volta a
  `RETRY_WAIT` (ou `FAILED` com `RETRY_LIMIT_EXCEEDED` no limite de tentativas),
  áudio sem resultado durável expira 24 h após a conclusão do upload (uploads
  abandonados, 24 h após a intenção), retentativas vencidas recebem uma única
  mensagem por tentativa e obrigações de exclusão pendentes ou falhas são
  repetidas com backoff. Acompanhe `inspection_transcription_sweep_items_total`
  por `scan` e `outcome`; os logs trazem apenas contadores e o tenant.
- As quatro varreduras (lease, expiração, retentativa e limpeza) rodam sempre, em
  um laço próprio do scheduler com limite de tempo igual ao intervalo; uma
  varredura que falha não impede as demais, e os erros aparecem juntos em
  `scheduler transcription audio scan failed tenant=<id>`. Em falhas de
  armazenamento, o log traz somente a classe do erro (`timeout`, `canceled`,
  `denied`, `invalid`, `network` ou `unavailable`), nunca a chave do objeto.
- **Limite conhecido:** uma nota que já tem revisão `MODEL` (resultado pago e
  gravado) nunca expira pela regra de 24 h, porque o resultado durável deve ser
  preservado. Se o `Complete` do worker falhar sempre para essa nota (por
  exemplo, erro de schema ou de constraint), ela fica ciclando entre lease
  vencido e nova entrega, mantém o áudio original e não cria a obrigação de
  exclusão. Sinais: `inspection_transcription_sweep_items_total{scan="lease_recovery",outcome="recovered"}`
  crescendo sem queda e a mesma nota com `LEASE_EXPIRED` por mais de 24 h.
  Corrija a causa no worker e deixe a próxima entrega concluir a nota a partir da
  revisão gravada, sem nova chamada ao modelo; não altere o banco diretamente.
- Reenvie apenas uma falha transitória elegível dentro da janela de retenção do
  áudio e confira o próximo estado e a tentativa no ledger de uso.
- Quando uma transcrição já foi persistida, uma falha de exclusão deve gerar
  somente uma nova tentativa de limpeza. Não reenvie a chamada ao modelo.
- Áudio expirado, ausente ou inválido não pode ser recuperado por nova chamada.
  Ofereça correção de texto quando houver transcrição ou uma nova gravação quando
  a captura ainda estiver aberta.

## Uso e custos desconhecidos

Filtre o consumo por **Transcrição** e depois pelo tenant e pela vistoria. Cada
tentativa potencialmente cobrável deve permanecer como chamada independente.
Use o custo informado pelo gateway como reportado; preserve estimativas em campo
separado quando o ledger passar a fornecê-las. Custo ausente ou resposta perdida
após o envio são desconhecidos/incertos, nunca custo zero. Totais incompletos
devem continuar identificados como parciais.

No estado atual, o ledger/UI não expõe um valor estimado separado. Não produza
uma estimativa manual no valor reportado e não use chamadas simuladas para
aprovar custo de produção.

## Gates antes de habilitar em produção

Registrar data, versão da aplicação/gateway, operador, dispositivo físico,
versão do sistema/navegador, codec MIME real, resultado do `ffprobe`, erro de
intervalo medido, perfil/modelo, versão do corpus, latência e origem dos dados
de uso/custo. Um gate só passa com evidência real e reproduzível.

| Gate | Evidência exigida | Estado deste checkout |
| --- | --- | --- |
| DEVICE-01 | Cinco gravações em iPhone/Safari durante vídeo, zero faixa de áudio no vídeo, vídeo íntegro e erro de intervalo ≤500 ms. | Pendente: aparelho e medições reais não disponíveis. |
| DEVICE-02 | Repetição em Android/Chrome, WebM/Opus e recuperação após reconexão. | Pendente: aparelho e medições reais não disponíveis. |
| MODEL-01 | Corpus rotulado de pelo menos 50 clipes PT-BR em ambos os perfis, erros literais e de atributos, latência e custo/uso do provedor. | Pendente: corpus rotulado e credenciais pagas não disponíveis. |
| MODEL-02 | Para áudio Safari MP4, comprovar MIME real no LiteLLM/Gemini ou conversão FFmpeg limitada, sem alterar aliases visuais nem timeout global. | Pendente: configuração pronta; validação real com provider/dispositivo ainda não registrada. |

## Jornadas automatizadas pendentes

| Caso | Evidência exigida | Estado deste checkout |
| --- | --- | --- |
| IT-001–IT-015 | Política GraphQL: quotas, autorização, versão, concorrência, replay, isolamento por tenant e preservação de notas aceitas. | Parcial: testes focados existem, mas a matriz atribuída não foi integralmente executada nem evidenciada como integração PostgreSQL. |
| IT-251–IT-264 | Uso GraphQL/Admin: tentativas, custo reportado/desconhecido/incerto, paginação, falhas e isolamento por tenant/vistoria. | Parcial: agregação e UI foram exercitadas por testes unitários/pacotes; matriz de cenários completa segue pendente. |
| IT-373–IT-375 | Compatibilidade e erros seguros nas superfícies GraphQL existentes. | Pendente como matriz atribuída; a suíte total também falha em `TestOperationManifestIT001` por manifesto GraphQL stale no worktree compartilhado. |
| IT-376–IT-377, IT-386–IT-387 | Consumer MSG01, perfil e readiness do worker dedicado. | Coberto por `services/inspection/cmd/inspection-transcription-worker/rabbit_integration_test.go` (tag `integration`), com fila e routing key exclusivas por execução (prefixo `it-transcription-<uuid>-`, removidas ao final). IT-376: repositório e ledger PostgreSQL reais, dois consumers; a duplicata durante o lease vai para `.retry.5m`, a redelivery após a conclusão recebe ack, e restam uma revisão, uma chamada no ledger e uma chamada ao gateway (o gateway é um stub em memória). IT-377: envelope inválido vai para a DLQ sem alterar o job. IT-386: health LiteLLM, perfil do catálogo, `/healthz`/`/readyz` e registro dos consumers; o processo completo do worker com MinIO/FFmpeg reais não é iniciado. IT-387: o binário real do worker, iniciado com `INSPECTION_TRANSCRIPTION_PROFILE=UNKNOWN`, sai com código 1 e `INVALID_TRANSCRIPTION_CONFIG`, enquanto consumers e readiness em execução continuam ativos; a API e os workers reais não fazem parte do teste. Execute somente contra dependências descartáveis configuradas explicitamente (`INSPECTION_TEST_*`). |
| IT-378–IT-381 | Consumers MSG02/03, recálculo do manifest e prazo de espera do catálogo. | Pendente da task_05 por decisão do orquestrador; as filas ainda não são declaradas pelo worker de transcrição. |
| E2E-001 | Admin configura 100/300 e 20/30, habilita e Capture mostra limites efetivos. | Pendente: fluxo Capture/API integrado e evidência E2E completa não disponíveis. |
| E2E-019 | Admin filtra Transcrição e distingue sucesso, retry incerto, custo parcial e chamada única. | Pendente: dados/runtime do worker não disponíveis para jornada completa. |

Os demais casos contratuais e as jornadas de ponta a ponta das tarefas 02 e 03
continuam pendentes. A cobertura local do worker não substitui as evidências
físicas e de modelo. Até que esses itens tenham revisão, mantenha a política de
produção desabilitada.

Os aliases `gemini-3.5-flash-lite`, `gemini-2.0-flash-lite` e
`gemini-2.5-flash-lite` estão configurados com timeout individual de 40s. Os
aliases visuais e o timeout global permanecem intactos. A imagem LiteLLM local
continua usando `latest`, pois o Compose documenta que ainda falta confirmar
uma versão/digest publicado compatível; essa decisão permanece anotada junto à
declaração da imagem.

## Catálogo com notas de voz: comportamentos aceitos

- A criação concorrente de `InventoryEnvironment` pelos consumers do catálogo pode
  violar a unicidade; o consumer falha, a mensagem cai na fila de retry de 5 s e a
  reentrega resolve o ambiente existente. É esperado e não exige ação.
- As filas de retry do catálogo (`catalog-*`) e do vídeo reentregam em ambos os
  consumers. O tratamento é idempotente (mesmo manifesto, mesmo job, nenhuma chamada
  extra ao modelo), então a duplicidade de entrega é aceita.
- O texto falado de um pedido é limitado a 24000 caracteres no total; notas além do
  teto saem do pedido com o motivo `NOTE_TEXT_LIMIT_EXCEEDED`, sem truncamento.
- Texto que chega depois de o job do catálogo ter começado não cria manifesto nem
  entra no pedido: só uma regeneração explícita o inclui.
