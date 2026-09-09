---
status: completed
title: Complete Operational and Customer Dashboard
type: frontend
complexity: critical
---

# Task 6: Complete Operational and Customer Dashboard

## Overview

Deliver the standalone Dashboard as two deliberately separate experiences: internal operational work for managers, employees and viewers, and a restrictive customer portal for customer viewers. Replace the current section-switched placeholder shell with durable generated-operation-backed schedule, inspection, project, triage, report, recapture-request, customer projection, and notification journeys.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. Dashboard MUST retain the selected membership context and send X-Inspection-Membership-ID on every protected request; switching or denial MUST clear prior protected state before new data loads.
- R2. Dashboard MUST use Task 4 generated operation result and variable types from feature-owned documents; handwritten GraphQL strings and copied response types MUST NOT remain in feature components.
- R3. Internal operations and customer projections MUST use separate UI and data models; customer pages MUST consume only allowlisted customer queries and MUST NOT filter internal entities client-side.
- R4. Schedule journeys MUST cover list/filter/pagination, creation, recurrence impact preview, update, cancel, conflicts, replay, recovery, and preservation of historical inspections.
- R5. Inspection and project journeys MUST cover collections, detail/history, creation, valid lifecycle transitions, scopes, expected versions, mutation identities, and recoverable current-state refresh.
- R6. Summary and triage MUST share one active server-backed filter context, preserve filters/cursors/deep links, expose freshness/staleness, and remain read-only for VIEWER without substituting UI gates for authorization.
- R7. Report journeys MUST expose readable versions, canonical/rendered content, current/history publication, PDF-only observable download with digest/expiry, publish, and reasoned version-safe invalidation with immediate customer withdrawal.
- R8. Dashboard MUST implement the manager half of targeted recapture with deficient requirements, original media, reason, deadline, delivery, status, and lineage; Task 8 owns the cross-product completion evidence.
- R9. Customer journeys MUST cover authorized portfolio/search/pagination, customer-safe timeline, active report, explicitly shared evidence, safe unavailable states, stable lineage, and time-limited media access.
- R10. Notification journeys MUST cover unread filtering, pagination, authorized links, idempotent mark-read, and versioned preferences using human-readable active verified destinations while retaining mandatory in-product notices.
- R11. Capability controls MUST remain explanatory and cover denied, conflict, loading, first-use, no-result, stale, interrupted, final, tombstone, and retry states without leaking inaccessible resource existence.
- R12. User text SHOULD be externalized in pt-BR, and changed flows MUST satisfy keyboard, focus, announcements, contrast, 200-percent zoom, and responsive requirements.
</requirements>

## Subtasks

- [x] 6.1 Establish the membership-aware Dashboard shell, role/audience navigation, and protected-state reset behavior.
- [x] 6.2 Deliver schedule collection, creation, impact-preview update, and cancellation.
- [x] 6.3 Deliver inspection collection/history, detail workspace, creation, and lifecycle transitions.
- [x] 6.4 Deliver project collection/detail/timeline and eligible stage/project transitions.
- [x] 6.5 Deliver shared-filter summary and triage queues with viewer-safe read-only behavior.
- [x] 6.6 Deliver report review, observable PDF download, publication history, publish, and immediate invalidation.
- [x] 6.7 Deliver manager recapture-request creation and status, lineage, delivery, and recovery visibility.
- [x] 6.8 Deliver customer-only portfolio, timeline, published report, and explicitly shared evidence experiences.
- [x] 6.9 Deliver notifications, unread state, authorized links, idempotent read handling, and verified-channel preferences.
- [x] 6.10 Implement assigned capability and authenticated Playwright suites including edge and accessibility evidence.

## Implementation Details

Follow the TechSpec “Frontend Design” and the distinct operational/customer architecture. Current schema and clients are intentionally incomplete; consume Task 4 final names rather than inventing frontend-only contracts. The current tenant-shaped route context is display/navigation state only and must never authorize access without the selected membership.

### Relevant Files

- `apps/dashboard/src/features/dashboard/dashboard-shell.tsx` — current audience/section monolith and handwritten operations.
- `apps/dashboard/src/features/dashboard/capabilities.ts` — role/audience gates and direct UT-132 target.
- `apps/dashboard/src/features/dashboard/operations.graphql` — current partial documents to split by feature.
- `apps/dashboard/src/features/notifications/use-notifications.ts` — polling, read state, links, and preference reconciliation.
- `apps/dashboard/src/graphql/client.ts` — bearer plus selected membership header and safe errors.
- `apps/dashboard/src/auth/session.ts` — membership selection, audience, role, and protected-state clearing.
- `apps/dashboard/src/routes.ts` and `src/auth/return-path.ts` — durable list/detail routes and safe returns.
- `apps/dashboard/app/` — replace section wrappers and add schedule, inspection, project, report, and customer detail routes.
- `apps/dashboard/src/features/schedules/`, `inspections/`, `projects/`, `triage/`, `reports/`, `recapture/`, `customer/`, and `notifications/` — recommended feature-owned components and GraphQL documents.
- `apps/dashboard/app/styles.css` — responsive operational/customer states over Task 4 primitives.
- `apps/dashboard/tests/unit/capabilities.test.ts` and `auth-and-client.test.ts` — capabilities and membership-aware transport.
- `apps/dashboard/tests/e2e/operational-dashboard.spec.ts` and `customer-dashboard.spec.ts` — recommended owned journey suites.
- `apps/dashboard/tests/e2e/support/auth.ts` — manager, viewer, customer, and membership selection fixtures.

Recommended route additions include schedules list/detail, inspection detail, project detail, internal report workspace, customer asset timeline, and a customer-only report/evidence route that does not reuse the internal report model.

### Dependent Files

- `services/inspection/schema.graphqls` — Task 4 canonical operational and customer contract.
- `apps/dashboard/codegen.ts` and `apps/dashboard/src/graphql/generated.ts` — generated operation boundary.
- `packages/inspection-design-system/src/index.ts`, primitives, and styles — neutral Task 4 visual and accessibility foundation.
- `services/inspection/internal/features/development/seed_qa/setup.go` and `services/inspection/cmd/inspection-seed/` — Task 8 supplies deterministic multi-persona live-stack fixtures.
- `apps/web/src/graphql/documents/dashboard.graphql` — behavioral migration reference only; do not extend or import it.
- `.compozy/tasks/graphql-frontend-capability-parity/_capability_matrix.md` — Dashboard operation ownership and evidence rows.
- `.github/workflows/ci.yml` — Task 8 executes the complete seeded parity gate.

### Related ADRs

- [ADR-001: Full Capability Parity and Backend-First Delivery](adrs/ADR-001-full-capability-parity-and-delivery-order.md)
- [ADR-003: Tenant Isolation and Delegated Administration](adrs/ADR-003-tenant-isolation-and-delegated-administration.md)
- [ADR-005: Customer Publication and Communication Policy](adrs/ADR-005-customer-publication-and-communication-policy.md)
- [ADR-007: Server-Resolved Membership Context](adrs/adr-007-server-resolved-membership-context.md)
- [ADR-008: Generated GraphQL Operations](adrs/adr-008-generated-graphql-operations.md)
- [ADR-009: Domain-Specific Observable Operation State](adrs/adr-009-domain-operation-state.md)
- [ADR-011: Fixed Operational Boundaries](adrs/adr-011-operational-boundaries.md)

## Deliverables

- Membership-aware internal Dashboard shell and separate customer portal architecture.
- Complete schedule, inspection, project, triage, report, download, publication, recapture-request, customer, and notification journeys.
- Generated-operation-only GraphQL usage, safe capability controls, pt-BR copy, responsive states, and accessibility behavior.
- Unit capability coverage plus authenticated manager, viewer, customer, conflict, recovery, withdrawal, and edge-case browser evidence.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; Task 8 exclusively owns the manager-to-Capture cross-product recapture cases.

- [x] Unit: UT-132 — unavailable actions remain hidden without changing server authorization.
- [x] E2E operational journeys: E2E-022, E2E-023, E2E-024, E2E-025, E2E-026 — schedules, inspections, projects, triage, and reports.
- [x] E2E customer/notification journeys: E2E-028, E2E-029, E2E-030 — customer portfolio/report/evidence and recipient notifications.
- [x] E2E operational edge evidence: E2E-055, E2E-056, E2E-057, E2E-058, E2E-059 — US-022 through US-026 EC families.
- [x] E2E customer/notification edge evidence: E2E-061, E2E-062, E2E-063 — US-028 through US-030 EC families.

## Success Criteria

- Every assigned test case implemented and passing.
- Internal and customer journeys never share broad internal DTOs or client-side privacy filtering.
- Membership switches clear protected data and every request is server-authorized by selected membership.
- Operational filters, deep links, current versions, conflicts, asynchronous state, and recovery remain observable.
- Report invalidation removes customer access immediately and never falls back to an older publication.
- Manager recapture requests are complete and ready for Task 8 cross-product participant evidence.
