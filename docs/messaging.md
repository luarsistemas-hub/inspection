# Mensageria e processamento assíncrono

O mediator entrega comandos dentro da API. Mudanças persistentes publicam envelopes na outbox; o dispatcher publica no RabbitMQ e o consumer grava inbox antes de executar handlers. O worker processa mídia, análise, relatórios, notificações, dashboard e retenção.

Um envelope contém `id`, `type`, `schemaVersion`, `occurredAt`, `tenantId`, `aggregateId`, `correlationId` e `payload`. `DefaultRegistry` valida tipos `.v1`, tamanho máximo e rejeita tokens, OTP, segredos, bytes de imagem e URLs presigned.

Contratos incluem `inspection.created.v1`, `inspection.state_changed.v1`, `capture.submitted.v1`, `media.upload_completed.v1`, `media.verified.v1`, `analysis.comparison_requested.v1`, `inspection.classified.v1`, `report.ready.v1`, `notification.delivery_requested.v1`, `notification.channel_status.v1`, `retention.purge_due.v1` e `retention.purged.v1`.

Falhas transitórias usam backoff e limite de tentativas. Handlers devem ser idempotentes por IDs de evento/job e versões otimistas. RabbitMQ local usa `inspection` / `inspection`; iniciar o worker é necessário para consumir.
