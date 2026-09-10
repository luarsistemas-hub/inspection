---
status: completed
title: Complete Evidence Trail Admin
type: frontend
complexity: high
---

# Task 5: Complete Evidence Trail Admin

## Overview

Replace the generic GraphQL query shell with the complete scoped Admin product defined by the approved Evidence Trail and Precise Operations reference. This slice consumes the stable typed contract and neutral primitives from Task 4 to deliver tenant entry, collections, details, lifecycle forms, history, bulk operations, governance, audit, and accessible recovery for US-003 through US-021. The current seeded Chrome run is the starting regression baseline: the seven primary buttons and every “Ver histórico” control render but do nothing, text filters do not consistently reach the server, only the first page is loaded, retention columns are shaped from the first row, usage is absent from Auditoria, and a page reload loses the session.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. Admin MUST use Task 4 generated operation result and variable types from feature-owned GraphQL documents; no runtime handwritten operation strings or raw JSON primary UI may remain.
- R2. No tenant business query MUST run before an identity-owned membership is selected; every protected request MUST attach X-Inspection-Membership-ID.
- R3. Membership and scope selection MUST be per-tab, persistent and visible, and switching or denial MUST clear or cancel prior protected state before new data loads.
- R4. Navigation MUST use the six accepted groups, display tenant, scope, role and page responsibility, and provide an explicit Dashboard exit without sharing its shell.
- R5. Every collection MUST preserve query parameters and provide resource-specific search, filters, count/freshness, sorting, dense semantic table, server pagination, primary/context actions, and distinct first-use, no-result, loading, stale, denied, and recoverable-error states.
- R6. Every detail MUST expose copyable identity, state, scope, version, relationships, allowed actions, and auditable history while restricting archived, inactive, invalidated, soft-deleted, and tombstone states.
- R7. Long or high-impact forms MUST be full pages with labels, inline userErrors, scope/consequence, expected version, impact or before/after preview, unsaved-draft recovery, single idempotent submission, and conflict review without silent overwrite.
- R8. Capability controls MUST reflect current delegated role and effective scope for explanation only; server authorization remains authoritative and denials MUST NOT disclose inaccessible resource existence.
- R9. Bulk, import, export, delivery, and purge experiences MUST render focused domain state and exact operational boundaries and MUST NOT offer bulk mutation for sensitive access or governance changes.
- R10. User text MUST be externalized in pt-BR, tenant timezone MUST govern display with UTC available for audit, and changed flows MUST meet WCAG 2.2 AA, keyboard, 44-pixel targets, status announcements, 200% zoom, and 320-pixel consultation requirements.
- R11. Every visible primary action (Criar unidade, Convidar usuário, Criar participante, Registrar ativo, Configurar política, Revisar contexto and Exportar filtros) and every history action MUST have a typed route or modal flow, submit the owning mutation/query, show pending/success/error state, and refresh the affected collection or detail.
- R12. Collection search and filters MUST be debounced, sent as the owning GraphQL variables, preserved in the URL, cancellable on navigation/context switch, and combined with cursor pagination; no screen may silently display only the first page.
- R13. Admin session recovery MUST preserve a safe return path, reauthenticate after reload or expiry, discard stale tenant data before replacement context loads, and keep a valid session when only one operation is forbidden.
- R14. Governance tables MUST use typed resource-specific columns so publication mode, retention periods, delivery state, audit fields and usage metrics remain visible independently of row order.
</requirements>

## Subtasks

- [x] 5.1 Replace the generic shell with grouped Admin navigation, administrative overview, per-tab membership/scope context, role visibility, and Dashboard exit.
- [x] 5.2 Deliver reusable Admin composition for collections, details, history, full-page forms, resource states, conflicts, confirmations, and operation status.
- [ ] 5.3 Deliver tenant and business-unit detail, create, edit, impact, and archive journeys.
- [ ] 5.4 Deliver memberships, administrative invitations, delegated role/scope, disable, and effective-access explanation/comparison journeys.
- [ ] 5.5 Deliver participant, contact, destination, segment, import, version, publish, activate, and archive journeys.
- [ ] 5.6 Deliver template and analysis-profile collection, detail, readable preview, version, publish, activate, and retire journeys.
- [ ] 5.7 Deliver asset registration, assignment, import/export, archive, and invited/administrative origin lineage journeys.
- [ ] 5.8 Deliver publication/notification policy, delivery attempt/retry, retention, legal hold, deletion, purge, and tombstone journeys.
- [x] 5.9 Deliver searchable audit/event detail, usage summaries, authorized exports, and links from high-impact outcomes to evidence.
- [x] 5.10 Implement every assigned component, route-integration, authenticated persona, responsive, and accessibility case.
- [x] 5.11 Wire and verify all seeded primary actions and history controls against the corresponding typed query/mutation, including refresh and user-facing operation state.
- [x] 5.12 Replace first-page-only loading with URL-backed server filters, cursor navigation, cancellation, and stale-response protection.
- [x] 5.13 Add reload/expiry recovery, forbidden-operation isolation, and explicit context reset coverage.
- [x] 5.14 Split governance and audit/usage views into typed resource sections with working CSV export and detail/history links.

## Implementation Details

Follow the TechSpec “Frontend Design” and the imported market reference for information architecture, required screen patterns, states, tokens, geometry, accessibility, and acceptance criteria. Keep Admin composition under `apps/admin`; Task 4 primitives remain product-neutral. Query-string state must preserve search, filters, sort, cursor, and scope from collection to detail and back. Start by replacing the current `AdminShell` button placeholders and generic row/action rendering, then split the work into domain slices so each form owns its GraphQL document, validation, mutation state and refresh policy.

### Relevant Files

- `.compozy/tasks/graphql-frontend-capability-parity/referencia-mercado-admin-inspection.md` — mandatory Admin product/design baseline.
- `apps/admin/src/features/admin/admin-shell.tsx` — current monolithic gate and JSON query shell to replace.
- `apps/admin/src/features/admin/operations.graphql` — current shallow documents to split by domain.
- `apps/admin/src/graphql/client.ts` — membership header, generated inputs/results, abort, and stable user-error handling.
- `apps/admin/src/auth/session.ts` — per-tab membership/scope and delegated Admin capability state.
- `apps/admin/src/auth/return-path.ts` and `src/routes.ts` — durable routes and safe collection/detail return context.
- `apps/admin/app/layout.tsx`, `app/styles.css`, and `app/page.tsx` — distinct shell, fonts, tokens, and entry flow.
- `apps/admin/app/organization/page.tsx`, `access/page.tsx`, `catalogs/page.tsx`, `assets/page.tsx`, `governance/page.tsx`, and `audit/page.tsx` — replace flat generic sections with owned routes.
- `apps/admin/src/features/admin/shell/` — recommended membership selector, context, grouped navigation, and product shell.
- `apps/admin/src/features/admin/shared/` — recommended collection, table, filters, pagination, detail, history, form, conflict, confirmation, state, and copyable-ID composition.
- `apps/admin/src/features/admin/overview/`, `organization/`, `access/`, `participation/`, `configuration/`, and `governance/` — recommended domain-owned components and GraphQL documents.
- `apps/admin/src/features/admin/i18n/pt-BR.ts` and `format/date-time.ts` — recommended externalized copy and tenant-timezone formatting.
- `apps/admin/tests/unit/` and `tests/integration/` — shell, state, conflict, route-context, and edge-case coverage.
- `apps/admin/tests/e2e/` and `tests/e2e/support/auth.ts` — seeded membership/persona journeys and accessibility evidence.
- `apps/admin/tests/e2e/admin.spec.ts` and `tests/e2e/authenticated-admin.spec.ts` — update stale route/button assumptions and make authenticated Admin journeys executable in Chrome.
- `apps/admin/src/graphql/generated.ts` — generated mutation, detail, history, usage and pagination types consumed by the domain slices.

Recommended route families include `/overview`; organization tenant, business-unit and origin routes; access user, invitation, role and effective-access routes; participation participant and segment routes; configuration template, analysis-profile and asset routes; governance policy, delivery, retention, audit and usage routes; and focused import/export operation-status routes.

### Dependent Files

- `services/inspection/schema.graphqls` — completed Task 4 canonical Admin contract.
- `apps/admin/codegen.ts` and `src/graphql/generated.ts` — generated-operation boundary; never hand-edit generated output.
- `packages/inspection-design-system/src/index.ts`, `src/styles.css`, and Task 4 primitives — neutral visual and accessibility foundation.
- `deploy/docker-compose.yml` and environment configuration — API, OIDC, Dashboard exit, and seeded local runtime.
- `.compozy/tasks/graphql-frontend-capability-parity/_capability_matrix.md` — Admin operation ownership and journey evidence.
- `.github/workflows/ci.yml` and Task 8 parity fixtures — final authenticated execution and removal gate.

### Related ADRs

- [ADR-002: Admin Product and Design Direction](adrs/ADR-002-admin-product-and-design-direction.md)
- [ADR-003: Tenant Isolation and Delegated Administration](adrs/ADR-003-tenant-isolation-and-delegated-administration.md)
- [ADR-004: Versioned Lifecycle, Origin Provenance and Retention](adrs/ADR-004-versioned-lifecycle-origin-and-retention.md)
- [ADR-005: Customer Publication and Communication Policy](adrs/ADR-005-customer-publication-and-communication-policy.md)
- [ADR-007: Server-Resolved Membership Context](adrs/adr-007-server-resolved-membership-context.md)
- [ADR-008: Generated GraphQL Operations](adrs/adr-008-generated-graphql-operations.md)
- [ADR-009: Domain-Specific Observable Operation State](adrs/adr-009-domain-operation-state.md)
- [ADR-010: Executable Parity Release Gate](adrs/adr-010-parity-release-gate.md)
- [ADR-011: Fixed Operational Boundaries](adrs/adr-011-operational-boundaries.md)

## Deliverables

- Distinct Evidence Trail Admin shell, grouped information architecture, persistent tenant/scope context, and Dashboard exit.
- Complete typed collections, details, histories, forms, lifecycle, bulk, governance, audit, usage, and operation-state journeys.
- No visible Admin action remains inert: all seven primary actions, copy feedback, history, pagination, CSV export and context recovery produce a verifiable UI outcome.
- Seeded Chrome behavior is covered for the previously observed inert buttons, ineffective filters, first-page truncation, hidden retention fields, missing usage view and reload session loss.
- Externalized pt-BR copy, tenant-timezone formatting, responsive consultation, and WCAG 2.2 AA interaction behavior.
- Unit, route-integration, authenticated Playwright, denial, conflict, recovery, scale, keyboard, zoom, and responsive coverage.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — expanded IDs retain one-to-one story ownership.

- [ ] Unit Admin contracts: UT-009, UT-010, UT-011, UT-012 — scoped shell, navigation, collection, detail, and responsive behavior.
- [ ] Unit product states: UT-130, UT-131 — distinct collection states and version-conflict draft recovery.
- [ ] Unit edge cases: UT-135.01, UT-135.02, UT-135.03, UT-135.04, UT-135.05, UT-135.06, UT-135.07, UT-135.08, UT-135.09, UT-135.10 — US-003 EC-1 through EC-10.
- [ ] Integration route context: IT-003 — tenant, scope, filters, and detail restoration.
- [ ] Integration edge cases: IT-036.01, IT-036.02, IT-036.03, IT-036.04, IT-036.05, IT-036.06, IT-036.07, IT-036.08, IT-036.09, IT-036.10 — US-003 EC-1 through EC-10.
- [ ] E2E owned journeys: E2E-002, E2E-003, E2E-004, E2E-005, E2E-006, E2E-007, E2E-008, E2E-009, E2E-010, E2E-011, E2E-012, E2E-013, E2E-014, E2E-015, E2E-016, E2E-017, E2E-018, E2E-019, E2E-020, E2E-021 — tenant selection plus Admin US-003 through US-021 journeys.
- [ ] E2E tenant/Admin edge evidence: E2E-035.01, E2E-036.01 — US-002 and US-003 browser edge families.
- [ ] E2E administrative edge evidence: E2E-037, E2E-038, E2E-039, E2E-040, E2E-041, E2E-042, E2E-043, E2E-044, E2E-045, E2E-046, E2E-047, E2E-048, E2E-049, E2E-050, E2E-051, E2E-052, E2E-053, E2E-054 — US-004 through US-021 browser edge families.
- Regression assertions for the seeded Chrome audit belong to E2E-003 and the owned Admin journeys above: each primary button opens a working flow, “Ver histórico” resolves the selected resource, a non-matching filter produces an empty state, pagination reaches a second page, governance retains resource-specific columns, Auditoria exposes usage/export, and reauthentication restores the original safe route.

## Verification evidence

- `npm test -- --run` — PASS, 3 files and 10 tests.
- `npm run lint` — PASS, no errors.
- `npm run build` — PASS, GraphQL generation, type checking, static generation of 12 routes, and production build.
- `git diff --check` — PASS.
- `npm run codegen` — PASS; generated operation artifacts include Admin history, usage, and primary mutations.
- Authenticated Playwright journeys remain gated by the local seeded stack and `INSPECTION_E2E_AUTH=true`.

## Success Criteria

- Every assigned test case implemented and passing.
- No Admin page exposes raw GraphQL execution or raw JSON as its primary experience.
- Tenant and scope remain visible, per-tab, server-validated, and preserved across route context without authorizing by tenant ID.
- Every owned administrative operation has a typed business journey with explicit first-use, denial, conflict, interruption, and terminal behavior.
- High-impact mutations show scope and consequence, prevent duplicate submission, preserve drafts, and link to audit evidence.
- The imported market-reference architecture and accessibility checklist are satisfied at desktop, tablet, 320 pixels, and 200% zoom.
