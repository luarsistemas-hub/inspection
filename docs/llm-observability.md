# Observabilidade de LLM

O worker expõe métricas Prometheus em `GET /metrics`, protegido por `INSPECTION_METRICS_TOKEN`. As métricas são locais ao processo: para obter o total do serviço, o Prometheus deve coletar todas as réplicas e agregá-las. Elas medem chamadas do worker para o LiteLLM e não incluem retries internos do proxy nem substituem os registros financeiros em `usage.records`.

## Correlação e logs

Os eventos JSON emitidos pelo worker usam o evento `llm_observation` e carregam os identificadores disponíveis:

- `eventId`, `correlationId`, `causationId` e `executionId` identificam a entrega e seu processamento;
- `jobId` e `inspectionId` identificam a operação de análise;
- `callId` identifica uma chamada individual ao gateway e muda em cada retry;
- `attempt` e `replayGeneration` identificam a entrega RabbitMQ;
- `tenantHash`, `promptDigest` e `inputDigest` são hashes ou identificadores limitados;
- `provider`, `model`, `gatewayRequestId`, tokens, custo, status HTTP, `transportDelivered` e latência descrevem a chamada quando disponíveis.

Prompts, imagens, respostas, URLs, credenciais e textos arbitrários de erro não são registrados.
O custo aparece somente quando informado pelo gateway; não há inferência de moeda ou preço.

## Ledger durável por inspeção

A migration 40 cria `usage.llm_calls`, com uma linha por tentativa de chamada ao
gateway. A linha é criada como `STARTED` antes do transporte e finalizada como
`FINISHED` depois do retorno, inclusive em timeout, erro HTTP, resposta
malformada ou rejeição posterior da resposta. Retries possuem novos `callId`.

O ledger grava apenas IDs, hashes, modo, tentativa, provider/model retornados,
status HTTP, latência, tokens e custo explicitamente informado. Não há prompt,
imagem, resposta, moeda ou preço inferido. Se a finalização falhar, a chamada
original não é repetida e a linha permanece `STARTED`; isso é indicado por
`inspection_llm_ledger_writes_total{phase="finish",result="error"}`.

`usage.records` e `usage.daily_summaries` continuam representando somente o
resultado aceito e não são recalculados pelo ledger. Portanto, o custo
operacional potencial de uma inspeção é a soma dos custos não nulos das chamadas
com `transportDelivered=true`; chamadas sem custo informado permanecem
desconhecidas.

A API GraphQL expõe `inspectionLLMUsage(inspectionId:, mode: LIVE, first:, after:)`
para `TENANT_ADMIN` e `MANAGER`. Ela retorna tentativas, chamadas entregues,
tokens conhecidos, `knownReportedCost`, chamadas sem custo, detalhes seguros das
chamadas e os indicadores `costComplete`, `coverageStartedAt` e
`coverageComplete`. Inspeções criadas antes da aplicação da migration 40 possuem
cobertura histórica parcial e não recebem backfill; nesse caso o custo não é
declarado completo.

Eventos principais:

| Evento | Uso |
| --- | --- |
| `llm_call_started` | chamada entregue ao gateway observado |
| `llm_call_finished` | retorno técnico da chamada, inclusive erro |
| `analysis_attempt` | requisição, resposta, validação ou persistência |
| `analysis_processing` | resultado após o commit da inbox |
| `analysis_transaction_rollback` / `analysis_transaction_commit_error` | rollback do handler ou falha ao confirmar a transação |
| `analysis_delivery` | publicação de retry ou DLQ |

Códigos técnicos do gateway: `invalid_input`, `timeout`, `cancelled`, `transport`, `authentication`, `rate_limit`, `provider_http` e `malformed_response`.

## Métricas

As labels são limitadas a valores conhecidos: `mode`, `comparison_mode`, `model_alias`, `outcome`, `code`, `direction`, `stage`, `action` e `result`. IDs e provider/model retornados ficam nos logs.

| Métrica | Labels principais |
| --- | --- |
| `inspection_llm_calls_total` | modo, modo de comparação, alias e resultado técnico |
| `inspection_llm_call_duration_seconds` | modo, modo de comparação, alias e resultado técnico |
| `inspection_llm_inflight` | modo e alias |
| `inspection_llm_tokens_total` | modo, alias e direção `input/output` |
| `inspection_llm_usage_missing_total` | modo, alias e direção ausente |
| `inspection_llm_ledger_writes_total` | fase `start/finish` e resultado da escrita |
| `inspection_analysis_validation_total` | modo, comparação, resultado e código |
| `inspection_analysis_stage_duration_seconds` | etapa e resultado |
| `inspection_analysis_processing_total` | `completed`, `inconclusive`, `duplicate` ou `error` |
| `inspection_analysis_delivery_actions_total` | ação `retry/dlq` e resultado da publicação |

As durações usam buckets de 100 ms a 300 s e `+Inf`. Tokens só são somados quando o provider os informa.

## Scrape externo

O token deve ser montado pelo ambiente de deploy e referenciado por arquivo de credencial do Prometheus:

```yaml
scrape_configs:
  - job_name: inspection-worker
    metrics_path: /metrics
    authorization:
      credentials_file: /etc/prometheus/secrets/inspection-metrics.token
    static_configs:
      - targets:
          - inspection-worker-1:8082
          - inspection-worker-2:8082
```

## Consultas iniciais

Taxa de erro técnico em produção, ignorando o modo mock:

```promql
sum(rate(inspection_llm_calls_total{mode="live",outcome!="success"}[15m]))
/
clamp_min(sum(rate(inspection_llm_calls_total{mode="live"}[15m])), 1)
```

P95 de duração do gateway:

```promql
histogram_quantile(0.95,
  sum by (le) (rate(inspection_llm_call_duration_seconds_bucket{mode="live"}[15m]))
)
```

Retries publicados e análises inconclusivas:

```promql
increase(inspection_analysis_delivery_actions_total{action="retry",result="published"}[15m])

increase(inspection_analysis_processing_total{outcome="inconclusive"}[15m])
```

Chamadas sem uso informado:

```promql
sum by (direction) (increase(inspection_llm_usage_missing_total{mode="live"}[15m]))
```

Falhas de persistência do ledger:

```promql
sum(increase(inspection_llm_ledger_writes_total{result="error"}[15m])) by (phase)
```

Regras iniciais de alerta, filtrando `mock`:

```promql
# Erro técnico acima de 10% com pelo menos 20 chamadas em 15 minutos.
(
  sum(increase(inspection_llm_calls_total{mode="live",outcome!="success"}[15m]))
  /
  clamp_min(sum(increase(inspection_llm_calls_total{mode="live"}[15m])), 1)
) > 0.10
and sum(increase(inspection_llm_calls_total{mode="live"}[15m])) >= 20

# P95 acima de 80% do timeout configurado (24s para o padrão de 30s; ajuste ao ambiente).
histogram_quantile(0.95,
  sum by (le) (rate(inspection_llm_call_duration_seconds_bucket{mode="live"}[15m]))
) > 24

# Fallback técnico/inconclusivo ou publicação em DLQ.
increase(inspection_analysis_processing_total{outcome="inconclusive"}[15m]) > 0
or increase(inspection_analysis_delivery_actions_total{action="dlq",result="published"}[15m]) > 0

# Nenhuma informação de uso por 15 minutos apesar de chamadas live.
sum(increase(inspection_llm_usage_missing_total{mode="live"}[15m])) > 0
and sum(increase(inspection_llm_calls_total{mode="live"}[15m])) > 0
```

## Alertas iniciais

Os limites abaixo são pontos de partida e devem ser ajustados ao volume real:

- erro técnico acima de 10% com pelo menos 20 chamadas em 15 minutos;
- P95 acima de 80% de `INSPECTION_PROVIDER_TIMEOUT`;
- qualquer crescimento de fallback inconclusivo técnico ou publicação em DLQ;
- ausência de uso informado em chamadas live por 15 minutos, quando houver chamadas no mesmo período.
