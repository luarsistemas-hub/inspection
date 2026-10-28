# Runbook operacional de vídeo

Este runbook cobre o worker de preparação de vídeo, a fila isolada e as ações operacionais disponíveis no Admin e na API. Use uma conta com as permissões adequadas e registre os IDs de mídia/preparação e os horários do incidente; não copie tokens, URLs assinadas, dados pessoais ou conteúdo de cenas para chamados e logs.

## Arquitetura e fluxo

O `inspection-video-worker` é um serviço separado do worker geral. Ele consome trabalho persistido no PostgreSQL e mensagens de wake-up da fila RabbitMQ `inspection.video`; polling recupera trabalho mesmo se uma mensagem não chegar. Um permit único por processo serializa decodificação, preparação semântica e comparação. O worker usa FFmpeg/ffprobe, scratch em volume de disco (máximo configurado de 4 GiB) e `llm.Gateway` para screening e chamadas auxiliares limitadas. A API, o worker geral de fotos e o scheduler continuam independentes: indisponibilidade do worker de vídeo não deve bloquear análise PHOTO.

O worker publica progresso por etapa e checkpoints duráveis. Cada execução usa lease com token de fencing: a configuração padrão é lease de 120 segundos, heartbeat de 30 segundos e polling de 2 segundos. Um worker antigo que perdeu o lease não pode publicar resultado. A fila RabbitMQ acelera o despertar, mas o estado durável no banco é a fonte para recuperação.

A triagem de conteúdo sensível fica ativa por padrão. `INSPECTION_SKIP_SENSITIVE_CONTENT_VALIDATION=true` desativa somente essa detecção em fotos e vídeos; formato, integridade e duração continuam validados. `INSPECTION_IMAGE_VALIDATION_ENABLED=false` também ativa o bypass por compatibilidade. Valores inválidos na nova variável impedem a inicialização. Os workers registram a configuração efetiva em `platform.worker_runtime_settings`; o aviso de inicialização identifica qual variável habilitou o bypass. A mudança vale para novas mídias e não libera itens já bloqueados.

## Saúde, métricas e alertas

No ambiente local, `./scripts/local.sh up` sobe a stack com o worker de vídeo por padrão. `INSPECTION_VIDEO_WORKER=0 ./scripts/local.sh up` deixa o worker fora para cenários PHOTO. O Compose local o coloca no profile `video`, publica apenas em loopback na porta configurável `INSPECTION_VIDEO_WORKER_PORT` (padrão 8091) e expõe `GET /health`. O `/health` do worker é um sinal de processo, não comprova sozinho conectividade com todas as dependências.

Verificações locais úteis:

```sh
./scripts/local.sh status
./scripts/local.sh logs inspection-video-worker
curl --fail http://localhost:8091/health
curl --fail http://localhost:8080/healthz
curl --fail http://localhost:8080/readyz
```

Use a porta efetiva se `INSPECTION_VIDEO_WORKER_PORT` ou `INSPECTION_API_PORT` tiver sido alterada. `/healthz` verifica vida da API; `/readyz` verifica prontidão da API e compatibilidade do schema. Para métricas do worker, `GET /metrics` exige `Authorization: Bearer <INSPECTION_METRICS_TOKEN>` e retorna 404 sem token válido. Não coloque o token na linha de comando compartilhada, em tickets ou no histórico do shell. Em OCI, o endpoint do worker fica publicado somente no loopback da VM em `127.0.0.1:8091/metrics`; o scheduler publica em `127.0.0.1:8092/metrics`.

Observe principalmente as métricas `inspection_video_queue_depth` e `inspection_video_oldest_runnable_age_seconds` por etapa/status, `inspection_video_preparations_total`, `inspection_video_preparation_attempts_total`, `inspection_video_lease_losses_total`, `inspection_video_decoded_seconds_total`, `inspection_video_selected_frames_total`, `inspection_video_selected_bytes_total`, `inspection_video_gateway_requests_total`, `inspection_video_gateway_tokens_total`, `inspection_video_expiration_lag_seconds` e `inspection_video_partial_deletions_total`. O worker também publica falhas terminais de configuração do provedor e o timestamp do último heartbeat de trabalho.

Os alarmes OCI configurados são:

| Alarme | Condição | Severidade |
| --- | --- | --- |
| Latência GraphQL (`oci_monitoring_alarm.graphql_latency`, `inspection-graphql-p95-latency-high`) | p95 GraphQL acima de `graphql_request_p95_threshold_seconds` (padrão 2 s) por 10 min | WARNING |
| Fila de vídeo | Item executável mais antigo acima de 30 min por 5 min | WARNING |
| Scratch | Espaço livre abaixo de 8 GiB | CRITICAL |
| Expiração da fonte | Atraso de expiração acima de 15 min por 5 min | WARNING |
| Configuração do provedor | Mais de 3 falhas terminais em 15 min | CRITICAL |
| Heartbeat de trabalho | Mais de 120 s sem heartbeat | CRITICAL |

As métricas OCI são agregadas pelo coletor do host; consulte também logs do serviço e a lista Admin abaixo. A latência HTTP é observada por `inspection_http_request_duration_seconds{route,status_class}`; o coletor publica o p95 da rota `graphql` como `GraphQLRequestP95Seconds`. O alarme acima cobre essa latência de requisições GraphQL, incluindo operações relacionadas a fotos. Ausência de heartbeat pode significar falta de trabalho, processo parado ou falha de coleta: correlacione com profundidade/idade da fila e `/health` antes de agir.

## Preparações: investigar, tentar novamente e reprocessar

1. No Admin, abra **Operações de vídeo** (`/video-operations`) e filtre a lista por status. A consulta `videoPreparations` pagina resultados e mostra etapa, tentativas, frames processados, código de erro seguro, possibilidade de retry e disponibilidade da fonte.
2. Verifique primeiro `/health`, logs do `inspection-video-worker`, idade da fila, espaço scratch e alarmes do provedor. Corrija a causa (por exemplo, restaurar o worker, espaço ou configuração do provedor) antes de repetir trabalho.
3. Para uma falha retryable na mesma fonte e no mesmo perfil, use **Tentar novamente**. A operação `retryVideoPreparation` exige `mediaId`, `expectedPreparationVersion` e `clientMutationId`; use a versão atual exibida pela consulta. Versão desatualizada retorna conflito. Trabalho pronto ou não retryable não deve ser forçado por retry.
4. Use **Reprocessar** somente quando a intenção for criar uma preparação imutável com um perfil de extração implantado. `reprocessVideoPreparation` exige também `profileDigest` conhecido pelo deployment. Não invente digest nem use reprocessamento para contornar fonte expirada quando a extração precisa ler o original.
5. Para regenerar o plano de comparação de uma inspeção, a API oferece `reanalyzeVideoInspection` com inspeção, plano esperado e chave de idempotência. Ela usa as submissões e a origem já fixadas; não redireciona o histórico para uma origem mais nova. A mutation existe no GraphQL, mas não é uma ação da tela Admin atual.
6. Acompanhe a etapa/status no Admin e as métricas de fila até a conclusão. Preserve o código seguro e a geração resultante no registro do incidente.

## Disponibilidade do vídeo

Vídeo está disponível para todos os tenants e não depende de uma ativação no Admin. Antes de publicar uma versão da API que permita novos fluxos de vídeo, confirme que o worker está implantado e saudável, que dependências e storage estão prontos e que os critérios de release foram aprovados. A tela **Operações de vídeo** continua disponível para acompanhar preparações, retries, retenção e exclusão.

## Expiração de fontes, leases, legal hold e deleção

O scheduler verifica expiração de originais a cada cinco minutos. A contagem de retenção começa na primeira preparação bem-sucedida; por padrão a janela congelada é 30 dias para origem e 5 dias para fonte corrente, sujeita à política de vídeo do tenant. Reanálise/retry sem mudança de perfil não reinicia esse marco. A expiração do original é independente da retenção de frames e relatórios: após expirar, o original deixa de estar disponível, mas frames ainda referenciados podem permanecer para evidência e relatórios.

Antes de uma fonte ser removida, o scheduler verifica holds e leases ativos. Um lease de leitura/preparação em andamento pode terminar dentro do seu limite; depois do vencimento, novas leituras não são admitidas. A fonte passa por `DELETING` e só vira `EXPIRED` quando original e proxies estiverem ausentes. Falha parcial deve ser repetida pelo processo de retenção; monitore `inspection_video_partial_deletions_total` e o atraso de expiração. Não remova objetos diretamente do MinIO nem limpe o volume scratch manualmente como correção de uma fonte.

Na seção **Retenção e revisão** do Admin, informe exatamente um alvo: ID da fonte (`sourceMediaId`) ou versão de origem (`originVersionId`), além do motivo. Use **Aplicar retenção** (`applyLegalHold`) antes do prazo quando houver obrigação de preservar; **Liberar retenção** (`releaseLegalHold`) quando a obrigação terminar. Se a fonte já estiver em `DELETING`, a aplicação do hold pode retornar conflito: não prometa restauração do original. Um hold sobre frames retidos não recupera bytes de original já expirados.

**Solicitar exclusão** (`recordDeletionRequest`) registra um único escopo — inspeção, fonte ou origem — e retorna o ID do pedido, não a confirmação de conclusão. O scheduler processa pedidos `REQUESTED` e `BLOCKED` em lotes; holds efetivos, leases/trabalho ativo, origem ainda `ACTIVE` ou prazo de retenção não vencido podem manter o pedido `BLOCKED` com o motivo. Falhas transitórias deixam o pedido pendente para nova tentativa. Para acompanhar, use o `requestId` retornado e consulte, com acesso operacional autorizado, `retention.deletion_requests` (`status`, `reason`, `processed_at`); não há consulta GraphQL de status posterior no contrato atual. Para escopo de fonte, acompanhe também `media.video_sources.status` e `delete_error_code`: a fonte recebe `DELETING` com lease/token de fencing; falha de storage registra erro e permite retry após o lease, e sucesso marca a fonte `EXPIRED`.

A deleção da fonte remove o original e os derivados `VIDEO_PLAYBACK`; os frames retidos e seus relatórios seguem o ciclo de retenção de evidências e não são apagados por essa operação. Invalidar uma origem também remove suas referências de evidência; a limpeza do banco de frames é feita depois pelo purgador quando não restarem referências nem holds. `inspection_video_partial_deletions_total` existe para falhas parciais de remoção de fonte, mas neste checkout o executor de pedidos explícitos não recebe o coletor de métricas; portanto, essa métrica não é um sinal confiável de progresso ou falha desses pedidos. Informe conclusão do pedido somente quando `status=COMPLETED` estiver registrado e o estado do alvo corresponder ao escopo solicitado. Se o solicitante espera também a remoção de frames/relatórios, confirme a conclusão desse ciclo separado antes de afirmar que esses dados foram removidos.

## Revisar uma detecção sensível marcada incorretamente

Confirme a detecção original e a fonte correta com um revisor autorizado. Na seção **Retenção e revisão**, informe o `mediaId` e motivo não sensível e use **Registrar falso positivo** (`declareSensitiveDetectionFalsePositive`). A revisão é auditada; em vídeo ela se aplica ao resultado de screening da fonte e pode permitir a retomada da preparação. Se o original expirou ou não está mais disponível, a recuperação que exige decodificação pode não ser possível. Não contorne a revisão alterando dados diretamente no banco, nem inclua descrição explícita da cena no motivo.

## Worker parado, lease perdido ou fila crescente

1. Compare `/health` do worker, logs, `inspection_video_work_heartbeat_timestamp_seconds`, profundidade e idade da fila, métricas do scheduler e alertas OCI. Confirme também prontidão da API, PostgreSQL, RabbitMQ, MinIO e LiteLLM.
2. Se o container estiver ausente/parado, recupere-o pelo procedimento de deploy Compose já usado no ambiente. Em OCI, o serviço pertence ao profile `video`; o comando documentado para ativá-lo é `docker compose --profile video up -d inspection-video-worker` (no diretório e contexto Compose do deploy). No ambiente local, o comando oficial é `./scripts/local.sh up`, com `INSPECTION_VIDEO_WORKER=1` (padrão). Não use `./scripts/local.sh down` para recuperar só o worker: esse comando afeta a stack local inteira.
3. Se um processo morreu durante uma etapa, os checkpoints confirmados permanecem. O lease expira e outro ciclo pode reivindicar o trabalho; o token de fencing impede que o processo antigo publique um resultado tardio. Aguarde a janela do lease (padrão 120 s) e observe se a idade da fila volta a cair antes de disparar retry manual.
4. Se a fila continuar crescendo, procure bloqueio em dependências, falhas de configuração/autenticação do provedor, scratch abaixo do mínimo de 8 GiB ou repetidas perdas de lease. O worker tem concorrência 1 por processo; o aumento de paralelismo não é um ajuste operacional improvisado. Corrija a causa e use retry/reprocessamento pela operação apropriada.
5. Se o scratch estiver abaixo de 8 GiB, não apague arquivos ou objetos em uso manualmente. Restaure espaço pelo procedimento de capacidade/host e confirme o alerta resolvido; o worker exige margem livre antes de aceitar trabalho.

## Gates de release ainda pendentes

O conjunto atual ainda requer evidência de aparelhos físicos (iPhone Safari e Android Chrome), corpus de vídeo nomeado e consentido com avaliação semântica e medição de capacidade na VM com carga representativa. O roteiro e campos de evidência para aparelhos estão em [device-validation-gate.md](../../.compozy/tasks/vistoria-video/device-validation-gate.md). Os gates de corpus e capacidade também precisam de evidência aprovada. Até que todos os gates aplicáveis sejam fechados, mantenha a admissão de vídeo em produção desabilitada; subir o container do worker não habilita admissão.
