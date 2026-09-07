# Configuração

`config.Load` lê variáveis de ambiente e valida endpoints, origem, OIDC, role de banco, storage privado, janela de schema e timeout de providers. O Go não lê `.env`; os scripts carregam `.env.inspection` linha a linha, sem executar conteúdo.

| Grupo | Variáveis | Consumidor |
| --- | --- | --- |
| banco | `INSPECTION_DATABASE_URL`, `INSPECTION_MIGRATION_DATABASE_URL`, `INSPECTION_DISPATCHER_DATABASE_URL`, `INSPECTION_RUNTIME_DB_ROLE` | API, worker, scheduler, migrador |
| processo | `INSPECTION_ENV`, `INSPECTION_HTTP_ADDR`, `INSPECTION_API_PORT`, `INSPECTION_WORKER_PORT`, `INSPECTION_SCHEDULER_PORT`, `INSPECTION_ALLOWED_ORIGIN`, `INSPECTION_SCHEMA_MIN/MAX` | todos |
| OIDC | `INSPECTION_OIDC_ISSUER`, `INSPECTION_OIDC_AUDIENCE`, `INSPECTION_OIDC_JWKS_URL` | API |
| storage | `INSPECTION_MINIO_*`, `INSPECTION_STORAGE_PUBLIC` | API/worker |
| filas | `INSPECTION_RABBITMQ_URL`, `INSPECTION_DRAGONFLY_ADDRESS` | worker/API |
| providers | SMTP, Twilio, `INSPECTION_LITELLM_*`, `INSPECTION_GOTENBERG_URL`, `INSPECTION_PROVIDER_TIMEOUT` | worker/API |
| browser | `NEXT_PUBLIC_INSPECTION_API_URL`, `NEXT_PUBLIC_OIDC_*`, `NEXT_PUBLIC_STORAGE_URL` | Next.js |

Os valores completos estão em [`.env.example`](../.env.example). Copie para `.env.inspection`; host usa portas publicadas (5433, 5673, 6380, 9002, 1026, 1081, 18080 e 3001), enquanto Compose usa hostnames e portas internas. Variáveis já exportadas têm precedência.

Ao trocar `INSPECTION_WEB_PORT`, atualize também o redirect URI e `webOrigins` importados em `deploy/keycloak/inspection-realm.json`. O script ajusta automaticamente a origem CORS da API; o registro do cliente OIDC continua sendo explícito por segurança.
