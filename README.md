# Inspection platform

The active product is the Inspection API, workers and Next.js web application.
The legacy Contract Service POC has been retired; its local `.env` is preserved
outside version control for operators who still need its historical settings.

## Local start

Copy [`.env.example`](.env.example) to an untracked `.env` when running Go
processes on the host. The example documents every consumer and uses host ports
that do not collide with the legacy POC.

```sh
docker compose -f deploy/docker-compose.yml up -d --build
```

The browser is available at `http://localhost:3000`; the GraphQL API is at
`http://localhost:8080/graphql`; Keycloak is at `http://localhost:8081`; and
Gotenberg is exposed locally on port `3001`. Compose initializes the database,
the non-privileged runtime role, the private MinIO bucket and Keycloak realm
idempotently.

## Verification

Run the single repository gate:

```sh
./scripts/verify.sh
```

It generates and checks GraphQL code, formats and tests Go, audits/types/tests/
builds the web app, runs Playwright on production output, validates the Compose
model, and invokes the pinned Compozy 0.2.14 task validator with an isolated
Compozy home. CI executes the same component checks and uploads browser traces
and screenshots on failure.
