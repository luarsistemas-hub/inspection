# Dashboard: notificações não são carregadas nem operáveis

## Severidade

Alta — notificações existentes no banco não aparecem e ações declaradas não existem.

## Evidência

O seed criou três `recipient_notifications` não lidas para o membership atual,
mas o contador permaneceu em zero. A rota `/notifications` renderiza
`InternalContent` (prioridades e ações operacionais), não a lista de
notificações. O resolver `myNotifications` devolve sempre vazio e exclui
`TENANT_ADMIN`; `markNotificationRead` e preferências retornam “not configured”.

## Esperado

O contador, a lista, a leitura e as preferências devem refletir dados reais e
respeitar as capacidades de todos os papéis autorizados.

## Critérios de aceite

- Implementar `myNotifications`, `markNotificationRead` e preferências.
- Definir explicitamente a permissão de `TENANT_ADMIN`.
- Renderizar conteúdo de notificações para audiência interna e cliente.
- Cobrir contador, marcar como lida e paginação em E2E.
