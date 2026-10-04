# Infraestrutura local

Para provisionar e operar o ambiente de produção OCI, siga o runbook completo em [`oci/readme.md`](oci/readme.md). Ele usa Terraform Resource Manager em stacks separadas, um host ARM64 e mantém o Compose abaixo para desenvolvimento local.

`docker-compose.yml` sobe PostgreSQL, Dragonfly, MinIO, RabbitMQ, Keycloak, Mailpit e stubs WireMock. `inspection-bootstrap`, `inspection-migrate` e `inspection-runtime-bootstrap` são one-shot e preparam roles, schemas, migrations e bucket.

Use `../scripts/local.sh up` para toda a stack ou `../scripts/local.sh infra` para dependências. Nomes internos como `postgres` e `keycloak` só funcionam entre containers; o browser usa localhost. Volumes nomeados preservam dados entre reinicializações.

O login do realm `inspection` usa o tema `inspection`, mantido em `keycloak/themes/inspection/login`. O serviço monta esse diretório no Keycloak local, e o bootstrap seleciona o tema também em realms já criados. A tela oferece português brasileiro e inglês; o português é o idioma padrão.

O onboarding local usa as chaves públicas e secret de teste do Cloudflare Turnstile. Em ambientes compartilhados, configure `NEXT_PUBLIC_TURNSTILE_SITE_KEY`, `INSPECTION_TURNSTILE_SITE_KEY`, `INSPECTION_TURNSTILE_SECRET`, `INSPECTION_ONBOARDING_ORIGIN` e habilite `INSPECTION_TURNSTILE_ENABLED`. Use chaves reais e hostname HTTPS autorizado no ambiente de produção; mantenha a secret somente na API.

No rollout, publique primeiro a API com `INSPECTION_TURNSTILE_ENABLED=false`, depois o frontend com a chave pública e, por fim, altere a flag da API para `true`. Em produção, a API falha na inicialização se a proteção estiver habilitada sem as chaves ou sem uma origem de onboarding HTTPS permitida.
