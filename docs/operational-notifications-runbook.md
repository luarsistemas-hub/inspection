# Runbook de notificações operacionais

## Configuração segura

Use `.env.inspection` ignorado pelo Git ou o secret manager do ambiente. O
Compose usa nomes internos (`mailpit:1025`, `twilio-fake:8080` e
`meta-fake:8080`); processos no host usam as portas publicadas (`1026`, `1081`
e `1082`). `SMTP_TLS_MODE=none` é permitido somente no ambiente local.

`NOTIFICATION_MAX_ATTEMPTS` deve permanecer em quatro e
`NOTIFICATION_RETRY_DELAYS` em `5s,30s,5m`, salvo uma mudança coordenada do
contrato. O provider escolhido é persistido com cada canal; alterar a
configuração não muda o provider de uma tentativa já enfileirada.

Não coloque credenciais, destinatários reais, OTPs, tokens, URLs com token,
corpos renderizados ou ciphertext em logs, eventos, RabbitMQ ou arquivos
rastreáveis. O endpoint `/metrics` exige `Authorization: Bearer` com o token
configurado e expõe somente métricas agregadas.

## Chaves e templates

`NOTIFICATION_PAYLOAD_KEYS` é um mapa versionado de chaves AES-256 em base64;
`NOTIFICATION_ACTIVE_PAYLOAD_KEY` aponta para a chave de escrita. Para rotação,
adicione a nova chave, valide o serviço, altere a chave ativa e só remova a
antiga depois da expiração/terminalização de todo payload que ainda possa ser
executado. IDs de template Twilio/Meta devem ser aprovados no provedor e
mantidos em configuração de deployment.

## UNKNOWN e filas

Uma interrupção depois do início de I/O pode deixar a aceitação do provedor
indeterminada. O worker reconcilia a lease expirada para `UNKNOWN` e não reenvia
automaticamente. Correlacione o receipt no provedor, registre a decisão
operacional e só faça uma nova solicitação de negócio quando houver evidência
de que nenhum envio ocorreu.

Antes de qualquer remoção de tratamento v1:

1. implante consumidores compatíveis com v1/v2;
2. verifique `V1QueueDepth=0` e `V1DurableWork=0`;
3. habilite os produtores v2;
4. drene e confirme o trabalho v1;
5. remova o legado somente quando o gate de rollout permitir.

O gate bloqueia a remoção enquanto houver item na fila v1 ou trabalho durável
v1. Em rollback, pare novos produtores v2, mantenha consumidores compatíveis,
preserve as filas e reative a versão anterior somente após confirmar que os
eventos já publicados são suportados.

## Simulação local

```sh
./scripts/local.sh init
./scripts/local.sh infra
docker compose --env-file .env.inspection -f deploy/docker-compose.yml ps
```

Os simuladores WireMock têm respostas determinísticas para sucesso, rate limit
(`X-Simulator-Scenario: transient`) e erro permanente
(`X-Simulator-Scenario: permanent`). Mailpit confirma aceitação SMTP; aceitação
não é promovida para `DELIVERED` sem callback suportado.

## Live smokes

Os casos E2E-012 a E2E-015 só são considerados quando
`INSPECTION_LIVE_SMOKES=true`, as credenciais, templates e endpoints explícitos
do provider estão completos e cada destinatário aparece em
`INSPECTION_LIVE_ALLOWLISTED_RECIPIENTS`. Sem a flag, os testes exibem skip
explícito. Configuração parcial com a flag ativa falha; não há fallback para
valores locais ou destinatários de produção.
