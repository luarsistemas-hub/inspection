---
status: completed
title: Operational, Customer and Capture Backend
type: backend
complexity: critical
---

# Task 3: Operational, Customer and Capture Backend

## Overview

Complete the operational, customer-facing, notification, recapture, and external Capture backend capabilities for US-022 through US-032. This slice replaces placeholder or resolver-local behavior with authorized vertical slices and observable domain state across API, scheduler, worker, object storage, report rendering, and delivery providers.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. Every operational or customer operation MUST be a registered VSA command/query using Task 1 membership context for internal products and the existing scoped cookie/CSRF responsibility for Capture.
- R2. Schedules, inspections, projects, stages, reports, publications, downloads, notifications, recapture, media, and submissions MUST expose their current, historical, conflict, unavailable, and recoverable states rather than mutation-only acknowledgements.
- R3. Collections MUST use stable cursors with default 25 and maximum 100 and preserve the filters, freshness, ordering, and authorization required by their owning journey.
- R4. Schedule, inspection, project, report, publication, recapture, and Capture mutations MUST enforce idempotency, optimistic concurrency, valid predecessor states, scoped roles, and generic non-disclosing failures.
- R5. Report downloads MUST be PDF-only, observable while preparing, integrity-checked, retained for 24 hours, and reauthorized before each time-limited access issue.
- R6. Publication invalidation MUST withdraw customer access immediately, retain internal history, notify affected recipients, and MUST NOT expose an older publication automatically.
- R7. Customer portfolio, timeline, report, and evidence MUST use explicit allowlisted DTOs with restrictive defaults and MUST never expose internal entities or fields for browser-side filtering.
- R8. Customer media access MUST be authorized and time-limited on every request and fail safely after policy, publication, replacement, soft-delete, purge, or scope changes.
- R9. Notification preferences MUST expose human-readable active verified destinations and a current version; optional external channels may change but mandatory in-product notices MUST remain enabled.
- R10. Recapture MUST preserve requested-only context and original/replacement lineage while rejecting duplicate, expired, completed, canceled, invalidated, or purged work.
- R11. Capture recovery MUST reconcile durable server and object-store multipart state, resume only missing 5 MiB parts, preserve honest local/server status, and require reauthentication after the two-hour session expires.
- R12. Media MUST enforce the 20 MiB JPEG/PNG/WebP/HEIC boundary, 24-hour upload expiry, digest verification, and idempotent completion without creating duplicate logical media.
</requirements>

## Subtasks

- [x] 3.1 Complete schedule collection/detail/impact and create, update, cancel, materialization, and reminder semantics.
- [x] 3.2 Complete inspection collection/detail/history and scoped create, cancel, invalidate, and terminal-state behavior.
- [x] 3.3 Complete project collection/detail/timeline and ordered exceptional, start, skip, close, and reopen stage transitions.
- [x] 3.4 Replace direct Dashboard reads with summary, triage, and timeline query slices that agree on filters, freshness, scope, and pagination.
- [x] 3.5 Complete immutable report detail, PDF download preparation, publication current/history, publish/invalidate, audit, and delivery state.
- [x] 3.6 Implement default-deny customer portfolio, timeline, active report, evidence lineage, and time-limited media projections.
- [x] 3.7 Complete recipient notification, unread count, authorization-resolved links, mark-read, verified channels, preferences, and delivery fan-out.
- [x] 3.8 Complete targeted recapture query/history/request/submission/deadline behavior and replacement lineage.
- [x] 3.9 Complete public decline, false-positive, durable upload states, multipart reconciliation, recovery, reauthentication, and invalid-work quarantine.
- [x] 3.10 Add required models, migrations, RLS, events, mediator registrations, composition wiring, and all assigned tests.

## Implementation Details

Follow the TechSpec “Data Models,” “Integration Points,” “Testing Approach,” and build-order step 3. Task 2 supplies active-resource, publication-policy, retention, verified-destination, and focused-operation foundations. Task 4 owns final GraphQL naming and resolver mapping; this task must expose complete application contracts without adding persistence logic to the resolver layer.

### Relevant Files

- `services/inspection/internal/platform/database/models.go` and `migrations/planner.go` — additive state, indexes, constraints, and RLS.
- `services/inspection/internal/contracts/events/events.go` — operational, publication, notification, recapture, and media events.
- `services/inspection/internal/platform/auth/authorize.go`, `scope.go`, and `tenanttx/tenanttx.go` — membership-derived tenant and scoped role enforcement.
- `services/inspection/internal/platform/objectstore/objectstore.go` and `minio.go` — multipart reconciliation, digest verification, and authorized media.
- `services/inspection/internal/platform/notifications/notifications.go` and `providers.go` — per-destination email, SMS, and WhatsApp delivery.
- `services/inspection/internal/platform/messaging/retry.go` — 5-second, 30-second, and 5-minute retry schedule before DLQ.
- `services/inspection/internal/features/schedules/` — schedule core and create/update/cancel/list/materialize/reminder slices.
- `services/inspection/internal/features/inspections/` — inspection create/get/list/cancel/invalidate and lifecycle core.
- `services/inspection/internal/features/projects/` — project/stage create/get/list/start/skip/close/reopen operations.
- `services/inspection/internal/features/dashboard/` — summary, triage, timeline, and new customer portfolio/timeline slices.
- `services/inspection/internal/features/reports/` — report snapshot, render, detail, download, publication, and customer projections.
- `services/inspection/internal/features/reports/customer/core/evidence.go` — existing allowlist mapper to activate through customer slices.
- `services/inspection/internal/features/notifications/recipient/` — notification list, unread count, links, preferences, and mark-read.
- `services/inspection/internal/features/recapture/` — request, list/detail, deadline, submission, and lineage.
- `services/inspection/internal/features/capture/`, `invitations/`, and `media/` — scoped bootstrap, decline, metadata, impossibility, false-positive, uploads, and submission.
- `services/inspection/internal/integration/harness/` — PostgreSQL, GraphQL, event, provider, and object-store integration fixtures.
- `services/inspection/internal/features/capture_integration_test/` and `lifecycle_integration_test/` — existing concurrency and lifecycle patterns.
- `services/inspection/cmd/inspection-api/main.go`, `inspection-worker/main.go`, and `inspection-scheduler/main.go` — composition and process lifecycle.

New slice siblings should cover schedule detail/preview, inspection history, registered Dashboard projections, publication current/history and transitions, customer portfolio/timeline/report/evidence, notification preferences and mark-read, recapture list/detail, and media upload reconciliation.

### Dependent Files

- `services/inspection/schema.graphqls` — Task 4 exposes these complete application contracts.
- `services/inspection/internal/platform/graphql/resolvers/resolver.go`, `schema.resolvers.go`, and `helpers.go` — Task 4 maps slices and removes resolver-local persistence.
- `services/inspection/internal/platform/graphql/generated.go` and `models_gen.go` — Task 4 regenerates the Go boundary.
- `apps/dashboard/src/features/dashboard/operations.graphql` and `capabilities.ts` — Task 6 consumes operational/customer contracts.
- `apps/dashboard/src/features/notifications/use-notifications.ts` — Task 6 consumes verified preferences and current notification state.
- `apps/capture/src/graphql/documents/capture.graphql` — Task 7 consumes decline, false-positive, and recovery contracts.
- `apps/capture/src/pwa/uploads.ts` and `drafts.ts` — Task 7 reconciles local and server-confirmed state.
- `.github/workflows/ci.yml` and parity fixtures — Task 8 execute contract and authenticated browser evidence.

### Related ADRs

- [ADR-003: Tenant Isolation and Delegated Administration](adrs/ADR-003-tenant-isolation-and-delegated-administration.md)
- [ADR-004: Versioned Lifecycle, Origin Provenance and Retention](adrs/ADR-004-versioned-lifecycle-origin-and-retention.md)
- [ADR-005: Customer Publication and Communication Policy](adrs/ADR-005-customer-publication-and-communication-policy.md)
- [ADR-007: Server-Resolved Membership Context](adrs/adr-007-server-resolved-membership-context.md)
- [ADR-009: Domain-Specific Observable Operation State](adrs/adr-009-domain-operation-state.md)
- [ADR-011: Fixed Operational Boundaries](adrs/adr-011-operational-boundaries.md)

## Deliverables

- Complete operational, report, publication, download, customer, notification, recapture, and Capture application contracts.
- Explicit customer-safe projections and authorized time-limited media behavior with immediate publication withdrawal.
- Durable domain-specific asynchronous state across API, outbox, worker, scheduler, MinIO, Gotenberg, and delivery providers.
- Unit, setup, PostgreSQL/RLS, object-store, outbox, provider, concurrency, and recovery coverage for all assigned cases.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — compact ranges are completed from the matching stories and edge-case rows in `_user_stories.md`; preserve every stable ID below.

- [x] Unit baseline: UT-085, UT-086, UT-087, UT-088, UT-089, UT-090, UT-091, UT-092, UT-093, UT-094, UT-095, UT-096, UT-097, UT-098, UT-099, UT-100, UT-101, UT-102, UT-103, UT-104, UT-105, UT-106, UT-107, UT-108, UT-109, UT-110, UT-111, UT-112, UT-113, UT-114, UT-115, UT-116, UT-117, UT-118, UT-119, UT-120, UT-121, UT-122, UT-123, UT-124, UT-125, UT-126, UT-127, UT-128 — backend behavior for US-022 through US-032.
- [x] Unit edge cases for US-022: UT-154.01, UT-154.02, UT-154.03, UT-154.04, UT-154.05, UT-154.06, UT-154.07, UT-154.08, UT-154.09, UT-154.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-023: UT-155.01, UT-155.02, UT-155.03, UT-155.04, UT-155.05, UT-155.06, UT-155.07, UT-155.08, UT-155.09, UT-155.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-024: UT-156.01, UT-156.02, UT-156.03, UT-156.04, UT-156.05, UT-156.06, UT-156.07, UT-156.08, UT-156.09, UT-156.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-025: UT-157.01, UT-157.02, UT-157.03, UT-157.04, UT-157.05, UT-157.06, UT-157.07, UT-157.08, UT-157.09, UT-157.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-026: UT-158.01, UT-158.02, UT-158.03, UT-158.04, UT-158.05, UT-158.06, UT-158.07, UT-158.08, UT-158.09, UT-158.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-027: UT-159.01, UT-159.02, UT-159.03, UT-159.04, UT-159.05, UT-159.06, UT-159.07, UT-159.08, UT-159.09, UT-159.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-028: UT-160.01, UT-160.02, UT-160.03, UT-160.04, UT-160.05, UT-160.06, UT-160.07, UT-160.08, UT-160.09, UT-160.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-029: UT-161.01, UT-161.02, UT-161.03, UT-161.04, UT-161.05, UT-161.06, UT-161.07, UT-161.08, UT-161.09, UT-161.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-030: UT-162.01, UT-162.02, UT-162.03, UT-162.04, UT-162.05, UT-162.06, UT-162.07, UT-162.08, UT-162.09, UT-162.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-031: UT-163.01, UT-163.02, UT-163.03, UT-163.04, UT-163.05, UT-163.06, UT-163.07, UT-163.08, UT-163.09, UT-163.10 — EC-1 through EC-10.
- [x] Unit edge cases for US-032: UT-164.01, UT-164.02, UT-164.03, UT-164.04, UT-164.05, UT-164.06, UT-164.07, UT-164.08, UT-164.09, UT-164.10 — EC-1 through EC-10.
- [x] Integration baseline: IT-022, IT-023, IT-024, IT-025, IT-026, IT-027, IT-028, IT-029, IT-030, IT-031, IT-032 — operational, customer, notification, recapture, and Capture boundaries.
- [x] Integration edge cases for US-022: IT-055.01, IT-055.02, IT-055.03, IT-055.04, IT-055.05, IT-055.06, IT-055.07, IT-055.08, IT-055.09, IT-055.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-023: IT-056.01, IT-056.02, IT-056.03, IT-056.04, IT-056.05, IT-056.06, IT-056.07, IT-056.08, IT-056.09, IT-056.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-024: IT-057.01, IT-057.02, IT-057.03, IT-057.04, IT-057.05, IT-057.06, IT-057.07, IT-057.08, IT-057.09, IT-057.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-025: IT-058.01, IT-058.02, IT-058.03, IT-058.04, IT-058.05, IT-058.06, IT-058.07, IT-058.08, IT-058.09, IT-058.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-026: IT-059.01, IT-059.02, IT-059.03, IT-059.04, IT-059.05, IT-059.06, IT-059.07, IT-059.08, IT-059.09, IT-059.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-027: IT-060.01, IT-060.02, IT-060.03, IT-060.04, IT-060.05, IT-060.06, IT-060.07, IT-060.08, IT-060.09, IT-060.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-028: IT-061.01, IT-061.02, IT-061.03, IT-061.04, IT-061.05, IT-061.06, IT-061.07, IT-061.08, IT-061.09, IT-061.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-029: IT-062.01, IT-062.02, IT-062.03, IT-062.04, IT-062.05, IT-062.06, IT-062.07, IT-062.08, IT-062.09, IT-062.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-030: IT-063.01, IT-063.02, IT-063.03, IT-063.04, IT-063.05, IT-063.06, IT-063.07, IT-063.08, IT-063.09, IT-063.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-031: IT-064.01, IT-064.02, IT-064.03, IT-064.04, IT-064.05, IT-064.06, IT-064.07, IT-064.08, IT-064.09, IT-064.10 — EC-1 through EC-10.
- [x] Integration edge cases for US-032: IT-065.01, IT-065.02, IT-065.03, IT-065.04, IT-065.05, IT-065.06, IT-065.07, IT-065.08, IT-065.09, IT-065.10 — EC-1 through EC-10.

## Success Criteria

- Every assigned test case implemented and passing.
- Operational state transitions reject invalid order, stale versions, duplicate replay, and cross-scope access.
- Customer projections contain only allowlisted policy-approved fields and withdraw invalidated publications immediately.
- Download, delivery, recapture, and upload flows expose truthful recoverable state through their terminal outcome.
- Multipart recovery never duplicates media and never attaches uploads to expired or invalid work.
- API, worker, and scheduler register all new slices and preserve bounded shutdown.
