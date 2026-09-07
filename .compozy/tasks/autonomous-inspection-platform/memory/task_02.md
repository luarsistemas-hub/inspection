# Task 02 Memory

## Current state

- Implementation and all assigned validations are complete. Changes remain uncommitted for manual review.

## Decisions

- Event payloads are registry-validated and reject secret, token, presigned URL, image-byte, and broad-profile fields before consumption.
- Outbox rows transition to `CLAIMED` under `FOR UPDATE SKIP LOCKED`; stale claims are recoverable and only confirmed, routable publications become `PUBLISHED`.
- Inbox uniqueness includes tenant, consumer, event, and replay generation so deliberate replay remains idempotent per generation.
- External tokens include only a tenant locator and an opaque 256-bit secret; the full token is hashed and every authoritative lookup enters `TenantTx` first.
- Twilio callbacks include `tenantId` in the signed callback URL, verify signature plus timestamp, and deduplicate provider transitions within tenant scope.
- Docker Compose retains default ports but permits MinIO host-port overrides for local collision handling. MinIO has no public-origin configuration and its bucket policy is private.
- Docker-gated Testcontainers tests use pinned RabbitMQ, MinIO, Dragonfly, Mailpit, and WireMock images.

## Learnings

- Dragonfly v1.34.2 needs a smaller explicit local footprint (`--proactor_threads=2 --maxmemory=512mb`) in this Docker Desktop environment.
- Host port 9000 was already owned by an unrelated ClickHouse container; validation used `INSPECTION_MINIO_PORT=19000` and `INSPECTION_MINIO_CONSOLE_PORT=19001` without changing defaults.
- RabbitMQ sends `basic.return` before the corresponding publisher acknowledgement; the publisher drains the buffered return after an ack so an unroutable mandatory message cannot be accepted.

## Touched surfaces

- `services/inspection/internal/contracts/events`
- `services/inspection/internal/platform/{database,messaging,objectstore,ratelimit,security,notifications,sensitivecontent,runtime}`
- `services/inspection/internal/features/{invitations,messaging,media,notifications}`
- `services/inspection/internal/platform/graphql`, `services/inspection/schema.graphqls`, and `services/inspection/cmd/{inspection-api,inspection-worker}`
- `deploy/docker-compose.yml`, `deploy/wiremock/mappings/twilio-messages.json`, `go.mod`, `go.sum`, and `go.work.sum`

## Open risks

- Later capture slices must resolve media ownership from PostgreSQL before invoking object-store operations; object-key prefixes are partitioning aids, not authorization proof.
- Later notification business slices should replace the worker's contract-only inbox handlers with their stable local outcomes while preserving the consumer registration contract.

## Verification evidence

- `test -z "$(gofmt -l services/inspection)"` passed.
- `git diff --check` passed.
- `env GOCACHE=/tmp/inspection-go-cache go test -count=1 ./...` passed.
- `env GOCACHE=/tmp/inspection-go-cache go vet ./...` passed.
- `env GOCACHE=/tmp/inspection-go-cache go build ./...` passed.
- `docker compose -f deploy/docker-compose.yml config --quiet` passed.
- `env GOCACHE=/tmp/inspection-go-cache TESTCONTAINERS_RYUK_DISABLED=true go test -count=1 -tags=integration ./services/inspection/internal/platform/integrationdeps -run TestTask02DependencyHarnesses` passed in 14.226s.
- Compose smoke checks showed RabbitMQ, Dragonfly, Mailpit, MinIO, and WireMock healthy; MinIO anonymous access returned 403 and the Twilio fake returned HTTP 201 with `SM-local-test`.
