# Task 06 external integration harness

The harness is deliberately kept inside the inspection service so it can use
the same event contracts, tenant transaction helper, database migrator, and
RabbitMQ topology as production code.

Start the local dependencies first:

```sh
docker compose -f deploy/docker-compose.yml up -d postgres rabbitmq minio minio-setup litellm-stub gotenberg-stub
```

Run the dependency gate with a database that already has the Task 06 schema:

```sh
INSPECTION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/inspection_task06_verify?sslmode=disable' \
INSPECTION_TEST_MIGRATE=false \
go test -tags=integration ./services/inspection/internal/integration/harness -run TestTask06HarnessSmoke -count=1
```

Tenant-scoped fixtures must use the non-privileged runtime role required by
forced RLS. Set both URLs when running fixture or API/worker cases (the first
URL is administrative and applies migrations):

```sh
export INSPECTION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/inspection_task06_verify?sslmode=disable'
export INSPECTION_TEST_RUNTIME_DATABASE_URL='postgres://inspection_runtime:runtime-test@localhost:5432/inspection_task06_verify?sslmode=disable'
export INSPECTION_TEST_AUTH_ENABLED=true
export INSPECTION_ENV=test
```

`SeedTask06Fixture` creates an isolated tenant graph, an authorized test
principal, a submitted capture, and a private PNG object in MinIO. Use
`Task06AuthHeaders` for GraphQL requests, `WaitOutbox`/`WaitInbox`/`WaitQueue`
for asynchronous assertions, and `ResetProviderStubs` between provider cases.
The test-auth headers are rejected unless the API is explicitly running in
`INSPECTION_ENV=test` with `INSPECTION_TEST_AUTH_ENABLED=true`.

The full external suite should import `harness`, create one harness per process,
declare the worker queue contracts, seed data through `WithinTenant`, publish
canonical events with `PublishPayload`, call GraphQL through `GraphQL`, and use
`Eventually` for asynchronous assertions. Override `INSPECTION_TEST_*` values
in CI; no credentials are embedded in the test code.

`Task06AssignedCases()` exposes the exact 148-case assignment (14 unit and 134
integration IDs) as a fail-fast manifest for the external runner. It does not
stand in for the external scenarios: the runner must register an implementation
for every ID and fail when one is missing.
