# Configuração

`config.Load` lê variáveis de ambiente e valida endpoints, origem, OIDC, role de banco, storage privado, janela de schema e timeout de providers. O Go não lê `.env`; os scripts carregam `.env.inspection` linha a linha, sem executar conteúdo.

| Grupo | Variáveis | Consumidor |
| --- | --- | --- |
| banco | `INSPECTION_DATABASE_URL`, `INSPECTION_RUNTIME_DATABASE_URL`, `INSPECTION_MIGRATION_DATABASE_URL`, `INSPECTION_DISPATCHER_DATABASE_URL`, `INSPECTION_RUNTIME_DB_ROLE` | API, worker, scheduler, migrador |
| processo | `INSPECTION_ENV`, `INSPECTION_HTTP_ADDR`, `INSPECTION_API_PORT`, `INSPECTION_WORKER_PORT`, `INSPECTION_SCHEDULER_PORT`, `INSPECTION_ALLOWED_ORIGIN`, `INSPECTION_SCHEMA_MIN/MAX` | todos |
| OIDC | `INSPECTION_OIDC_ISSUER`, `INSPECTION_OIDC_AUDIENCE`, `INSPECTION_OIDC_JWKS_URL` | API |
| storage | `INSPECTION_MINIO_*`, `INSPECTION_STORAGE_PUBLIC` | API/worker |
| filas | `INSPECTION_RABBITMQ_URL`, `INSPECTION_DRAGONFLY_ADDRESS` | worker/API |
| providers | SMTP, Twilio, `INSPECTION_LITELLM_*`, `INSPECTION_PROVIDER_TIMEOUT` | worker/API |
| browser | `NEXT_PUBLIC_INSPECTION_API_URL`, `NEXT_PUBLIC_OIDC_*`, `NEXT_PUBLIC_STORAGE_URL` | Next.js |

Os valores completos estão em [`.env.example`](../.env.example). Copie para `.env.inspection`; host usa portas publicadas (5433, 5673, 6380, 9002, 1026, 1081, 18080), enquanto Compose usa hostnames e portas internas. A geração de PDFs no worker pode ser ajustada com `INSPECTION_REPORT_PDF_TIMEOUT`, `INSPECTION_REPORT_PDF_LEASE`, `INSPECTION_REPORT_PDF_POLL`, `INSPECTION_REPORT_PDF_MAX_ATTEMPTS`, `INSPECTION_REPORT_PDF_RETRY_DELAYS`, `INSPECTION_REPORT_PDF_MAX_EVIDENCE`, `INSPECTION_REPORT_PDF_MAX_PAGES`, `INSPECTION_REPORT_PDF_MAX_IMAGE_BYTES` e `INSPECTION_REPORT_PDF_MAX_BYTES`. Os padrões são 60 s, 120 s, 1 s, 4 tentativas com esperas de 5 s, 30 s e 5 min, 200 evidências, 200 páginas, 128 MiB e 64 MiB. Variáveis já exportadas têm precedência.

Ao trocar as portas das aplicações, atualize também o redirect URI e `webOrigins` importados em `deploy/keycloak/inspection-realm.json`. O script ajusta automaticamente a origem CORS da API; o registro do cliente OIDC continua sendo explícito por segurança.
