---
status: pending
title: Administrative, Catalog and Governance Backend
type: backend
complexity: critical
---

# Task 2: Administrative, Catalog and Governance Backend

## Overview

Complete the tenant-owned backend capabilities needed by the Admin product from tenant configuration through governance and audit. This slice delivers independently queryable VSA operations, domain-specific asynchronous state, migrations and RLS for US-004 through US-021 so Task 4 can expose a complete thin GraphQL boundary.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. Every new administrative operation MUST be a vertical slice with setup.go as its public entry, mediator registration, context propagation, tenanttx/GORM WithContext I/O, and dependency-validation tests.
- R2. Tenant, business-unit, membership, invitation, participant, catalog, asset, origin, policy, retention, audit, delivery, usage, import, export, and purge reads MUST use stable cursors with default 25 and maximum 100.
- R3. Delegated permissions MUST use the Task 1 membership context and MUST agree with Authorizer decisions without exposing cross-tenant or out-of-scope objects.
- R4. Versioned mutations MUST require expected version and client mutation identity, reject stale writes, preserve current state, and emit safe audit/correlation outcomes.
- R5. Published catalog, template, profile, origin, and policy versions MUST remain immutable; retirement, archive, activation, invalidation, and soft delete MUST preserve historical references.
- R6. Administrative origin upload MUST preserve the original media and ADMIN_UPLOAD provenance, actor, declared source, capture date, justification, digest, verification, lineage, and audit state.
- R7. Participant and asset imports MUST be UTF-8 CSV limited to 10,000 rows or 25 MiB, require preview before apply, and expose accepted, rejected, conflicted, and unprocessed rows without duplicate replay.
- R8. Exports MUST preserve active authorization and filters, expose focused observable state, expire after 24 hours, and reauthorize every download issue.
- R9. Delivery records MUST expose intent and per-channel/destination attempts for email, SMS, and WhatsApp; retries MUST skip destinations already confirmed successful.
- R10. Retention and purge MUST enforce valid policies, immediate soft deletion, legal-hold visibility, ordered eligibility, final transactional hold recheck, idempotent object deletion, and an allowlisted non-sensitive tombstone.
- R11. Background import, export, delivery, and purge work MUST use domain-specific operation records and the existing transactional outbox/worker rather than a generic jobs API.
- R12. Internal failures MUST remain generic externally while logs and audit retain redacted tenant, membership, actor, operation, state, idempotency, causation, and correlation context.
- R13. Administrative user invitations MUST accept an operator-facing email identity, persist an expiring invitation and delivery state, resolve the recipient identity through the configured OIDC provider, and activate exactly one approved membership after acceptance without exposing provider credentials to the browser.
</requirements>

## Subtasks

- [ ] 2.1 Complete persistence models, indexes, immutable-version constraints, RLS, migrations, and audit-safe operation state for all administrative domains.
- [ ] 2.2 Complete tenant and business-unit detail, collection, impact, lifecycle, idempotency, concurrency, and audit operations.
- [ ] 2.3 Complete membership and invitation administration plus delegated role/scope assignment and effective-access explanation/comparison.
- [ ] 2.4 Complete participant/contact lifecycle, verified delivery selection, import preview/apply/status, safe bulk archive, and filtered export.
- [ ] 2.5 Complete segment, template, and analysis-profile collection/detail/version history, activation, retirement, and usage relationships.
- [ ] 2.6 Complete asset lifecycle, impact, assignments, import/export/bulk, administrative origin upload, invitation administration, and origin lineage.
- [ ] 2.7 Complete publication and notification policy history plus delivery detail and eligible-target retry.
- [ ] 2.8 Complete retention status, deletion request, legal hold, purge eligibility/progress/result, scheduler enqueue, worker processing, and tombstone projection.
- [ ] 2.9 Complete audit and usage filters, details, stable cursors, freshness, timezone handling, and authorized asynchronous exports.
- [ ] 2.10 Register every slice and handler in API, worker, and scheduler composition roots and implement all assigned tests.
- [ ] 2.11 Complete the email-based administrative invitation lifecycle, provider delivery state, acceptance binding, resend/revoke idempotency, and audit events.

## Implementation Details

Follow the TechSpec “Data Models,” “Integration Points,” “Testing Approach,” and build-order step 2. Existing resolvers still perform several GORM reads directly; this task must provide mediator-ready commands and queries while Task 4 owns the coordinated schema, resolver, and generated-code change. Focused import, export, delivery, and purge records implement ADR-009 and must not be collapsed into a generic job abstraction.

### Relevant Files

- `services/inspection/internal/platform/database/models.go` — tenant-owned models and Models registration.
- `services/inspection/internal/platform/database/migrator.go` and `migrations/planner.go` — migrator-only schema, constraints, indexes, immutable triggers, roles, and forced RLS.
- `services/inspection/internal/platform/auth/authorize.go`, `scope.go`, and `store.go` — delegated authority and explainable effective-access source data.
- `services/inspection/internal/platform/tenanttx/tenanttx.go` — context-bound tenant transactions for all runtime I/O.
- `services/inspection/internal/contracts/events/events.go` — versioned domain event registry.
- `services/inspection/internal/platform/messaging/retry.go` — retry vocabulary and DLQ schedule.
- `services/inspection/internal/platform/objectstore/objectstore.go` — origin originals, exports, digest verification, and purge.
- `services/inspection/internal/platform/notifications/notifications.go` — email, SMS, and WhatsApp provider boundary.
- `services/inspection/internal/features/tenancy/` — tenant and business-unit lifecycle slices.
- `services/inspection/internal/features/access/` — membership, invitation, role/scope, and effective-access slices.
- `services/inspection/internal/features/participants/` — participant, contact, delivery-selection, import, bulk, and export slices.
- `services/inspection/internal/features/segments/` and `templates/` — immutable catalog and analysis-profile lifecycle.
- `services/inspection/internal/features/assets/` and `origins/` — asset lifecycle, assignments, imports, administrative upload, and provenance.
- `services/inspection/internal/features/notifications/` and `reports/publication/` — policy, delivery attempts, history, and retry.
- `services/inspection/internal/features/retention/` — policy, deletion, legal hold, purge, and tombstone operations.
- `services/inspection/internal/features/audit/` and `usage/` — searchable audit, usage summaries, and filtered exports.
- `services/inspection/internal/features/catalog_integration_test/catalog_integration_test.go` and `lifecycle_integration_test/lifecycle_integration_test.go` — existing PostgreSQL/RLS integration patterns.
- `services/inspection/cmd/inspection-api/main.go`, `inspection-worker/main.go`, and `inspection-scheduler/main.go` — composition and lifecycle.

New slice siblings should be created under the owning feature for tenant/unit reads and impact; administrative invitation lifecycle; effective-access explain/compare; participant and asset preview/apply/status/export; segment/template/profile detail/history/retirement; administrative origin upload; policy/history/delivery retry; retention/deletion/hold/purge/tombstone status; and audit/usage exports.

### Dependent Files

- `services/inspection/schema.graphqls` — Task 4 exposes the completed administrative contracts.
- `services/inspection/internal/platform/graphql/resolvers/schema.resolvers.go` — Task 4 replaces direct GORM access with thin mediator calls.
- `services/inspection/internal/platform/graphql/generated.go`, `models_gen.go`, and `gqlgen.yml` — regenerated after coordinated schema completion.
- `services/inspection/internal/platform/graphql/catalog_contract_test.go` and `lifecycle_contract_test.go` — consume the final GraphQL mapping.
- `services/inspection/internal/features/reports/customer/core/` — Task 3 consumes publication and evidence policy.
- `services/inspection/internal/features/notifications/recipient/` — Task 3 consumes verified destinations and notification policy.
- `services/inspection/internal/features/inspections/core/`, `schedules/core/`, and `projects/core/` — Task 3 enforces active resource and soft-delete prerequisites.
- `services/inspection/internal/features/capture/core/` — Task 3 consumes participant, destination, origin, and policy state.
- `apps/admin/src/graphql/` and Admin routes — Task 5 consume the final administrative GraphQL contract.

### Related ADRs

- [ADR-001: Full Capability Parity and Backend-First Delivery](adrs/ADR-001-full-capability-parity-and-delivery-order.md)
- [ADR-003: Tenant Isolation and Delegated Administration](adrs/ADR-003-tenant-isolation-and-delegated-administration.md)
- [ADR-004: Versioned Lifecycle, Origin Provenance and Retention](adrs/ADR-004-versioned-lifecycle-origin-and-retention.md)
- [ADR-005: Customer Publication and Communication Policy](adrs/ADR-005-customer-publication-and-communication-policy.md)
- [ADR-007: Server-Resolved Membership Context](adrs/adr-007-server-resolved-membership-context.md)
- [ADR-009: Domain-Specific Observable Operation State](adrs/adr-009-domain-operation-state.md)
- [ADR-011: Fixed Operational Boundaries](adrs/adr-011-operational-boundaries.md)

## Deliverables

- Complete mediator-ready administrative commands, queries, domain records, migrations, indexes, constraints, and forced RLS.
- Complete lifecycle, effective-access, bulk/import/export, origin provenance, governance, delivery, retention, purge, audit, and usage behavior.
- API, worker, scheduler, outbox, MinIO, and provider integration with focused observable state.
- Unit, setup, PostgreSQL/RLS, outbox, object-store, and provider-boundary coverage for every assigned case.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — several compact ranges are behaviorally defined by their corresponding story and edge-case rows in `_user_stories.md`; preserve every stable ID below.

- [ ] Unit baseline: UT-013, UT-014, UT-015, UT-016, UT-017, UT-018, UT-019, UT-020, UT-021, UT-022, UT-023, UT-024, UT-025, UT-026, UT-027, UT-028, UT-029, UT-030, UT-031, UT-032, UT-033, UT-034, UT-035, UT-036, UT-037, UT-038, UT-039, UT-040, UT-041, UT-042, UT-043, UT-044, UT-045, UT-046, UT-047, UT-048, UT-049, UT-050, UT-051, UT-052, UT-053, UT-054, UT-055, UT-056, UT-057, UT-058, UT-059, UT-060, UT-061, UT-062, UT-063, UT-064, UT-065, UT-066, UT-067, UT-068, UT-069, UT-070, UT-071, UT-072, UT-073, UT-074, UT-075, UT-076, UT-077, UT-078, UT-079, UT-080, UT-081, UT-082, UT-083, UT-084 — backend behavior for US-004 through US-021.
- [ ] Unit edge cases for US-004: UT-136.01, UT-136.02, UT-136.03, UT-136.04, UT-136.05, UT-136.06, UT-136.07, UT-136.08, UT-136.09, UT-136.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-005: UT-137.01, UT-137.02, UT-137.03, UT-137.04, UT-137.05, UT-137.06, UT-137.07, UT-137.08, UT-137.09, UT-137.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-006: UT-138.01, UT-138.02, UT-138.03, UT-138.04, UT-138.05, UT-138.06, UT-138.07, UT-138.08, UT-138.09, UT-138.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-007: UT-139.01, UT-139.02, UT-139.03, UT-139.04, UT-139.05, UT-139.06, UT-139.07, UT-139.08, UT-139.09, UT-139.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-008: UT-140.01, UT-140.02, UT-140.03, UT-140.04, UT-140.05, UT-140.06, UT-140.07, UT-140.08, UT-140.09, UT-140.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-009: UT-141.01, UT-141.02, UT-141.03, UT-141.04, UT-141.05, UT-141.06, UT-141.07, UT-141.08, UT-141.09, UT-141.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-010: UT-142.01, UT-142.02, UT-142.03, UT-142.04, UT-142.05, UT-142.06, UT-142.07, UT-142.08, UT-142.09, UT-142.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-011: UT-143.01, UT-143.02, UT-143.03, UT-143.04, UT-143.05, UT-143.06, UT-143.07, UT-143.08, UT-143.09, UT-143.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-012: UT-144.01, UT-144.02, UT-144.03, UT-144.04, UT-144.05, UT-144.06, UT-144.07, UT-144.08, UT-144.09, UT-144.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-013: UT-145.01, UT-145.02, UT-145.03, UT-145.04, UT-145.05, UT-145.06, UT-145.07, UT-145.08, UT-145.09, UT-145.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-014: UT-146.01, UT-146.02, UT-146.03, UT-146.04, UT-146.05, UT-146.06, UT-146.07, UT-146.08, UT-146.09, UT-146.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-015: UT-147.01, UT-147.02, UT-147.03, UT-147.04, UT-147.05, UT-147.06, UT-147.07, UT-147.08, UT-147.09, UT-147.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-016: UT-148.01, UT-148.02, UT-148.03, UT-148.04, UT-148.05, UT-148.06, UT-148.07, UT-148.08, UT-148.09, UT-148.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-017: UT-149.01, UT-149.02, UT-149.03, UT-149.04, UT-149.05, UT-149.06, UT-149.07, UT-149.08, UT-149.09, UT-149.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-018: UT-150.01, UT-150.02, UT-150.03, UT-150.04, UT-150.05, UT-150.06, UT-150.07, UT-150.08, UT-150.09, UT-150.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-019: UT-151.01, UT-151.02, UT-151.03, UT-151.04, UT-151.05, UT-151.06, UT-151.07, UT-151.08, UT-151.09, UT-151.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-020: UT-152.01, UT-152.02, UT-152.03, UT-152.04, UT-152.05, UT-152.06, UT-152.07, UT-152.08, UT-152.09, UT-152.10 — EC-1 through EC-10.
- [ ] Unit edge cases for US-021: UT-153.01, UT-153.02, UT-153.03, UT-153.04, UT-153.05, UT-153.06, UT-153.07, UT-153.08, UT-153.09, UT-153.10 — EC-1 through EC-10.
- [ ] Integration baseline: IT-004, IT-005, IT-006, IT-007, IT-008, IT-009, IT-010, IT-011, IT-012, IT-013, IT-014, IT-015, IT-016, IT-017, IT-018, IT-019, IT-020, IT-021 — PostgreSQL/RLS and external-boundary flows for US-004 through US-021.
- [ ] Integration edge cases for US-004: IT-037.01, IT-037.02, IT-037.03, IT-037.04, IT-037.05, IT-037.06, IT-037.07, IT-037.08, IT-037.09, IT-037.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-005: IT-038.01, IT-038.02, IT-038.03, IT-038.04, IT-038.05, IT-038.06, IT-038.07, IT-038.08, IT-038.09, IT-038.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-006: IT-039.01, IT-039.02, IT-039.03, IT-039.04, IT-039.05, IT-039.06, IT-039.07, IT-039.08, IT-039.09, IT-039.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-007: IT-040.01, IT-040.02, IT-040.03, IT-040.04, IT-040.05, IT-040.06, IT-040.07, IT-040.08, IT-040.09, IT-040.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-008: IT-041.01, IT-041.02, IT-041.03, IT-041.04, IT-041.05, IT-041.06, IT-041.07, IT-041.08, IT-041.09, IT-041.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-009: IT-042.01, IT-042.02, IT-042.03, IT-042.04, IT-042.05, IT-042.06, IT-042.07, IT-042.08, IT-042.09, IT-042.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-010: IT-043.01, IT-043.02, IT-043.03, IT-043.04, IT-043.05, IT-043.06, IT-043.07, IT-043.08, IT-043.09, IT-043.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-011: IT-044.01, IT-044.02, IT-044.03, IT-044.04, IT-044.05, IT-044.06, IT-044.07, IT-044.08, IT-044.09, IT-044.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-012: IT-045.01, IT-045.02, IT-045.03, IT-045.04, IT-045.05, IT-045.06, IT-045.07, IT-045.08, IT-045.09, IT-045.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-013: IT-046.01, IT-046.02, IT-046.03, IT-046.04, IT-046.05, IT-046.06, IT-046.07, IT-046.08, IT-046.09, IT-046.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-014: IT-047.01, IT-047.02, IT-047.03, IT-047.04, IT-047.05, IT-047.06, IT-047.07, IT-047.08, IT-047.09, IT-047.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-015: IT-048.01, IT-048.02, IT-048.03, IT-048.04, IT-048.05, IT-048.06, IT-048.07, IT-048.08, IT-048.09, IT-048.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-016: IT-049.01, IT-049.02, IT-049.03, IT-049.04, IT-049.05, IT-049.06, IT-049.07, IT-049.08, IT-049.09, IT-049.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-017: IT-050.01, IT-050.02, IT-050.03, IT-050.04, IT-050.05, IT-050.06, IT-050.07, IT-050.08, IT-050.09, IT-050.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-018: IT-051.01, IT-051.02, IT-051.03, IT-051.04, IT-051.05, IT-051.06, IT-051.07, IT-051.08, IT-051.09, IT-051.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-019: IT-052.01, IT-052.02, IT-052.03, IT-052.04, IT-052.05, IT-052.06, IT-052.07, IT-052.08, IT-052.09, IT-052.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-020: IT-053.01, IT-053.02, IT-053.03, IT-053.04, IT-053.05, IT-053.06, IT-053.07, IT-053.08, IT-053.09, IT-053.10 — EC-1 through EC-10.
- [ ] Integration edge cases for US-021: IT-054.01, IT-054.02, IT-054.03, IT-054.04, IT-054.05, IT-054.06, IT-054.07, IT-054.08, IT-054.09, IT-054.10 — EC-1 through EC-10.

## Success Criteria

- Every assigned test case implemented and passing.
- Every administrative read and mutation is available through a slice-local mediator contract with no new direct persistence in resolvers.
- Versioned resources preserve immutable history and reject stale or duplicate transitions safely.
- Imports, exports, delivery retries, and purge expose focused recoverable state with the fixed operational limits.
- Legal hold always wins the final purge transaction, and completed purge exposes only allowlisted tombstone fields.
- Delegated Admin results agree with server authorization and never disclose cross-tenant or out-of-scope data.
