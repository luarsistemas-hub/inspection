---
status: completed
title: "Async Messaging, External Sessions & Storage"
type: infra
complexity: critical
---

# Task 2: Async Messaging, External Sessions & Storage

## Overview

Deliver the durable asynchronous and private-evidence infrastructure used by capture, analysis, reporting, and notifications. This task makes duplicates, process restarts, bounded provider failures, scoped external access, and private multipart objects normal and recoverable operating conditions.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- State changes and event intent MUST commit through a transactional PostgreSQL outbox; consumers MUST use tenant-scoped inbox deduplication.
- RabbitMQ publishing MUST use confirms and mandatory routing; consumers MUST use durable queues, bounded prefetch, explicit acknowledgments, retries at 5 seconds, 30 seconds, and 5 minutes, and named DLQs.
- Event contracts MUST use versioned safe envelopes and MUST never contain secrets, presigned URLs, image bytes, or broad personal profiles.
- MinIO-compatible storage MUST be private, tenant-scoped, multipart resumable, hash/type/size verified, and incapable of overwriting immutable originals.
- External invitation secrets, OTP challenges, two-hour sessions, immediate revocation, and CSRF proof MUST remain authoritative in PostgreSQL; Dragonfly MUST only enforce ephemeral limits.
- OTP MUST use one shared code across all marked verified channels, ten-minute expiry, five attempts, 60-second resend, and five sends per invitation per hour.
- Notification channel attempts MUST remain independent; aggregate delivery MUST succeed after any selected channel succeeds while other failures continue bounded retries.
- The worker MUST provide reusable media normalization and pure-Go face/document-region screening ports without OCR extraction or external exposure before screening.
- Operational replay, reconciliation, orphan multipart cleanup, callback verification, and observability MUST be explicit and idempotent.
- Invitation/notification infrastructure MUST depend only on resolved delivery intents and stable contracts, never on private participant tables, so this task remains parallel with Task 3.
</requirements>

## Subtasks

- [x] 2.1 Deliver the versioned event registry, transactional outbox, dispatcher, publisher confirms, and inbox contract.
- [x] 2.2 Deliver RabbitMQ exchange, retry, DLQ, consumer lifecycle, replay, and poison-message behavior.
- [x] 2.3 Deliver private S3-compatible multipart storage, scoped signing, verification primitives, and cleanup reconciliation.
- [x] 2.4 Deliver normalization and pure-Go sensitive-content detection adapters with pinned model identity.
- [x] 2.5 Deliver invitation-token, OTP request/verification GraphQL slices, external-session, revocation, cookie, and CSRF behavior.
- [x] 2.6 Deliver Dragonfly-backed rate limits that fail safely without becoming authoritative state.
- [x] 2.7 Deliver channel registry, SMTP/Twilio-compatible adapters, independent delivery aggregation, and signed callbacks.
- [x] 2.8 Deliver worker registration, bounded concurrency, cancellation, fallback, and telemetry behavior.
- [x] 2.9 Deliver Testcontainers and provider fakes for RabbitMQ, MinIO, Dragonfly, Mailpit, and Twilio behavior.

## Implementation Details

Implement each dispatcher, consumer, webhook, and cleanup job as an operation-level slice registered by `Setup`. Keep event schemas under the explicit shared contracts package, while object-store, broker, rate-limit, and provider adapters remain platform concerns. Do not add capture or inspection business rules here; later tasks consume these contracts.

### Relevant Files

- `deploy/docker-compose.yml` — existing PostgreSQL/Dragonfly/MinIO topology, including public MinIO settings that must be removed.
- `services/inspection/internal/platform/` — foundation mechanisms supplied by Task 1.
- `services/inspection/cmd/inspection-worker/main.go` — worker composition root supplied by Task 1.
- `services/contract/internal/features/contracts/create/setup.go` — registration shape to preserve for consumers and webhooks.
- `.compozy/tasks/autonomous-inspection-platform/_techspec.md` — authoritative envelope, media, retry, session, and notification contracts.

### Dependent Files

- `services/inspection/internal/contracts/events/` — versioned envelopes, registry, and fixtures.
- `services/inspection/internal/platform/{messaging,objectstore,ratelimit,security,notifications,sensitivecontent}/` — reusable infrastructure adapters; business session state remains in invitation slices.
- `services/inspection/internal/features/{messaging,notifications,invitations,media}/` — dispatch, delivery, callback, expiry, and cleanup slices.
- `services/inspection/cmd/inspection-worker/main.go` — worker slice registration and lifecycle.
- `deploy/docker-compose.yml` — pinned RabbitMQ, MinIO, Dragonfly, Mailpit, and fake Twilio services.

### Related ADRs

- [ADR-002: Treat Evidence Metadata as Risk Signals, Not Proof of Authenticity](adrs/adr-002.md) — provenance guarantees.
- [ADR-005: Use Scoped, Passwordless External Participation with Directed Recapture](adrs/adr-005.md) — external access and channels.
- [ADR-006: Enforce Configurable Retention with Fixed Default Periods](adrs/adr-006.md) — secret expiry and deletion groundwork.
- [ADR-009: Isolate Tenants with PostgreSQL Row-Level Security](adrs/adr-009.md) — worker tenant context.
- [ADR-008: Build a New Inspection Service as Vertical Slices](adrs/adr-008.md) — consumer, webhook, and job packaging.
- [ADR-010: Use GORM with a Dedicated Schema Migrator](adrs/adr-010.md) — infrastructure model registration.
- [ADR-011: Coordinate Slices Through a Typed In-Process Bus](adrs/adr-011.md) — stable delivery/session contracts.
- [ADR-012: Use gqlgen and Browser-Appropriate Session Models](adrs/adr-012.md) — session and CSRF models.
- [ADR-013: Use Transactional Outbox, RabbitMQ, and Idempotent Consumers](adrs/adr-013.md) — asynchronous delivery.
- [ADR-014: Keep Media Private and Detect Sensitive Content Locally](adrs/adr-014.md) — storage and screening.
- [ADR-017: Design for the Confirmed MVP Capacity and Latency Envelope](adrs/adr-017.md) — concurrency and media limits.

## Deliverables

- Durable outbox/inbox and RabbitMQ retry/DLQ/replay infrastructure.
- Private verified multipart object storage and sensitive-content screening primitives.
- Secure invitation, OTP, external-session, CSRF, and rate-limit primitives.
- Independent email, WhatsApp, and SMS delivery adapters with aggregate status and callbacks.
- Integration harnesses for every infrastructure dependency delivered by this task.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there before writing tests.

- [x] Unit: UT-007, UT-008, UT-009, UT-010, UT-013, UT-014, UT-019, UT-020, UT-021, UT-038, UT-039, UT-040, UT-041, UT-046, UT-047, UT-068, UT-069
- [x] Integration: IT-354, IT-355, IT-356, IT-357, IT-358, IT-359, IT-360, IT-361, IT-362, IT-363, IT-364, IT-365, IT-366, IT-368, IT-369, IT-370, IT-376, IT-377, IT-378, IT-379, IT-383, IT-384, IT-385, IT-503, IT-504, IT-505, IT-506, IT-507, IT-508, IT-541, IT-542, IT-547, IT-548, IT-577, IT-578, IT-579, IT-580, IT-594, IT-595, IT-596

## Success Criteria

- Every assigned test case implemented and passing.
- Duplicate publication/delivery cannot duplicate a business outcome, and exhausted failure reaches its named fallback/DLQ.
- No original, derivative, report, session, or notification path bypasses tenant authorization.
- Session, OTP, CSRF, and callback boundaries meet their exact expiry, rate, signature, and revocation contracts.
- Worker and dependency integration suites pass under restart, redelivery, timeout, cancellation, and provider failure.
