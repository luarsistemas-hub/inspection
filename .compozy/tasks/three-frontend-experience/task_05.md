---
status: pending
title: Standalone Role-Adaptive Dashboard
type: frontend
complexity: critical
---

# Task 5: Standalone Role-Adaptive Dashboard

## Overview

Create the independent Dashboard product for scoped internal operations and permissioned customer review. The same application must compose materially different internal and customer experiences while relying exclusively on current backend capabilities and customer-safe GraphQL contracts.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- `apps/dashboard` MUST own its package metadata, lockfile, generated client, build, tests, Dockerfile, runtime configuration, and `inspection-dashboard` PKCE session.
- Dashboard entry MUST require current `DASHBOARD` entitlement and MUST re-evaluate capabilities after revocation or tenant deactivation.
- Internal manager/employee/viewer homes MUST adapt summaries, priority, navigation, and actions to effective role and hierarchical scope.
- Inspection, schedule, project, stage, triage, report, publication, invalidation, download, and directed-recapture operations MUST stay in Dashboard.
- `VIEWER` and `CUSTOMER_VIEWER` compositions MUST omit mutation controls, while direct mutation attempts remain server-denied.
- Customer entry MUST be asset/project-first and show only explicitly granted/inherited portfolio, chronological progress, and currently published results.
- Customer evidence MUST default to simple mode and offer advanced lineage without ever exposing blocked visuals or internal fields.
- Customer report navigation MUST distinguish the current publication from explicitly requested historical published versions.
- In-app notifications MUST use paginated current-member reads, 30-second visible polling, focus refresh, unread state, safe links, and idempotent read updates.
- Verified external channel preferences MUST be editable in Dashboard; in-app delivery remains mandatory and prior delivery records remain immutable.
- Filters, deep links, cursors, stale state, conflicts, partial loading, ordering, and 100x portfolio scale MUST preserve scope and user context.
- Dashboard MUST pin the exact design-system package version and meet the desktop/mobile browser, Portuguese, WCAG 2.2 AA, keyboard, and reduced-motion contracts.
</requirements>

## Subtasks

- [ ] 5.1 Establish the standalone Dashboard project, route shell, code generation, transport, and independent build/test configuration.
- [ ] 5.2 Deliver Dashboard PKCE/session, tenant/deep-link selection, entitlement guard, role capability composition, and Admin transition when permitted.
- [ ] 5.3 Deliver role-adaptive internal home, scoped summaries, triage, filters, paging, and stale/error/empty states.
- [ ] 5.4 Deliver inspection, schedule, project/stage, report publication/invalidation/download, and recapture operational journeys.
- [ ] 5.5 Deliver mutation-free internal viewer composition and immediate revocation behavior.
- [ ] 5.6 Deliver customer portfolio, asset/project timeline, current published report, and simple/advanced evidence experiences.
- [ ] 5.7 Deliver in-app notification center, unread state, polling lifecycle, safe destinations, and external-delivery status where authorized.
- [ ] 5.8 Deliver responsive/accessibility behavior and all assigned Vitest, integration, and Playwright contracts.
## Implementation Details

Follow the TechSpec “Story-to-Component Mapping,” customer allowlist, notification polling, and authorization sections. Internal and customer feature compositions may share presentation primitives but must use different GraphQL documents where the schema creates a customer safety boundary.

### Relevant Files

- `apps/web/app/(dashboard)/page.tsx` — current combined internal dashboard and administration implementation.
- `apps/web/src/auth/pkce.ts` and `session.ts` — current internal authentication baseline.
- `apps/web/src/graphql/client.ts` — current dashboard operations and report access.
- `apps/web/src/graphql/documents/dashboard.graphql` and `generated.ts` — operation/codegen baseline.
- `apps/web/tests/e2e/journeys.spec.ts` — current internal journey patterns.
- `services/inspection/internal/features/dashboard/` — summary, triage, and timeline backends.
- `services/inspection/internal/features/inspections/`, `projects/`, `schedules/`, and `recapture/` — operational contracts.
- `services/inspection/internal/features/reports/` and `notifications/` — report/publication/notification contracts.
- `services/inspection/schema.graphqls` — canonical internal and customer-safe API.

### Dependent Files

- `apps/dashboard/app/` and `src/features/` — new role-adaptive route/features.
- `apps/dashboard/src/auth/` and `src/graphql/` — isolated audience/session and separate internal/customer documents.
- `apps/dashboard/tests/unit/` and `tests/e2e/` — capability, polling, customer-safety, and browser journeys.
- `packages/inspection-design-system/` — exact published dependency.
- `deploy/docker-compose.yml`, Keycloak realm, and smoke/security scripts — task 07 runtime wiring.
- `apps/web/` — removed only during final cutover.

### Related ADRs

- [ADR-002: Permissioned and Role-Adaptive Dashboard](adrs/adr-002.md) — primary Dashboard composition.
- [ADR-003: Tenant-Controlled Report Publication](adrs/adr-003.md) — internal publication controls.
- [ADR-005: Use Three Standalone Frontend Projects in One Repository](adrs/adr-005.md) — isolated project contract.
- [ADR-006: Use Separate OIDC Clients with Shared SSO and One GraphQL API](adrs/adr-006.md) — Dashboard auth.
- [ADR-007: Separate Product Entitlements from Roles and Resolve Scope Hierarchies at Read Time](adrs/adr-007.md) — capabilities and scope.
- [ADR-008: Publish Reports Through an Audited Visibility Ledger and Customer-Safe Projections](adrs/adr-008.md) — customer evidence and notifications.

## Deliverables

- Independently deployable `apps/dashboard` with internal and customer role compositions.
- Complete operational, triage, report, recapture, customer portfolio/evidence, and notification journeys.
- Secure Dashboard PKCE/session, generated operation sets, safe media behavior, and immediate revocation handling.
- Assigned Dashboard unit, integration, E2E, accessibility, responsive, and scale coverage.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; read each full definition before implementation.

- [ ] Unit: UT-056, UT-057, UT-058, UT-072 — capability composition, polling, and Dashboard transport.
- [ ] Integration: IT-051, IT-052, IT-053, IT-054, IT-055, IT-056, IT-057, IT-058, IT-059, IT-060, IT-061, IT-062, IT-063, IT-064, IT-065, IT-066, IT-067, IT-068, IT-069, IT-070, IT-071, IT-072, IT-073, IT-074, IT-075, IT-076, IT-077, IT-078, IT-079, IT-080, IT-081, IT-082, IT-083, IT-084, IT-085, IT-086, IT-087, IT-088, IT-089, IT-090, IT-091, IT-092, IT-093, IT-094, IT-095, IT-096, IT-097, IT-098, IT-099, IT-100, IT-101, IT-102, IT-103, IT-104, IT-105, IT-106, IT-107, IT-108, IT-109, IT-110, IT-111, IT-112, IT-113, IT-114, IT-115, IT-116, IT-117, IT-118, IT-119, IT-120 — all US-006 through US-012 Dashboard edge contracts.
- [ ] End-to-end: E2E-023, E2E-024, E2E-025, E2E-026, E2E-027, E2E-028, E2E-029, E2E-030, E2E-031, E2E-032, E2E-033, E2E-034, E2E-035, E2E-036, E2E-037, E2E-038, E2E-039, E2E-040, E2E-041, E2E-042, E2E-043, E2E-044, E2E-045, E2E-046, E2E-047, E2E-048, E2E-049, E2E-050, E2E-051, E2E-052 — all US-006 through US-012 Dashboard acceptance journeys.

## Success Criteria

- Every assigned test case implemented and passing.
- Internal and customer users see only role-, entitlement-, tenant-, and scope-permitted data/actions.
- No customer operation can request or render an internal-only report/evidence field.
- Dashboard clean install, codegen, lint, unit tests, production build, and browser suite run independently.
