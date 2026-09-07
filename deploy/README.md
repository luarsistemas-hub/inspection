# Infraestrutura local

`docker-compose.yml` sobe PostgreSQL, Dragonfly, MinIO, RabbitMQ, Keycloak, Mailpit, Gotenberg e stubs WireMock. `inspection-bootstrap`, `inspection-migrate` e `inspection-runtime-bootstrap` são one-shot e preparam roles, schemas, migrations e bucket.

Use `../scripts/local.sh up` para toda a stack ou `../scripts/local.sh infra` para dependências. Nomes internos como `postgres` e `keycloak` só funcionam entre containers; o browser usa localhost. Volumes nomeados preservam dados entre reinicializações.
