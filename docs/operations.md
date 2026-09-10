# Operação e diagnóstico

Use `./scripts/local.sh status` e `./scripts/local.sh logs <serviço>`. `healthz` significa que o processo está vivo; `readyz` apenas lê `platform.schema_migrations` e verifica a janela compatível. Isso não comprova filas, scheduler, MinIO ou providers.

Para falha de startup, confira `docker compose --env-file .env.inspection -f deploy/docker-compose.yml ps`, logs de `inspection-migrate`, DSN e roles. Para `UNAUTHENTICATED`, confira issuer, audience, redirect URI, relógio e membership. Para uploads, confira bucket privado e endpoint público. Para eventos acumulados, confira RabbitMQ, worker, inbox/outbox e contratos.

```sh
./scripts/smoke.sh
./scripts/security-smoke.sh
./scripts/load-smoke.sh
```

Os smoke tests assumem API, Admin, Dashboard e Capture ativos. `down` preserva volumes; não há limpeza destrutiva nos scripts.
