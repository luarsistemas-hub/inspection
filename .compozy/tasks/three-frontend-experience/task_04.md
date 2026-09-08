---
status: pending
title: Standalone Admin Frontend
type: frontend
complexity: high
---

# Task 4: Standalone Admin Frontend

## Overview

Create the independent Admin product and move all tenant configuration, access, catalog, asset, governance, and audit journeys out of the combined web application. Admin uses its own OIDC client, route tree, GraphQL client, lockfile, image, and release while consuming an exact design-system package version.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- `apps/admin` MUST be a standalone Next.js project with its own package metadata, lockfile, build, tests, generated GraphQL client, Dockerfile, and runtime configuration.
- Admin MUST authenticate with Authorization Code plus PKCE through `inspection-admin`, keep tokens only in memory, and validate same-origin return paths.
- Every protected entry MUST require current `ADMIN` entitlement and `TENANT_ADMIN`; denial MUST reveal no configuration data and offer a safe allowed destination.
- Admin MUST cover tenant/defaults, business units, users, customer/internal invitations, entitlements, hierarchical scopes, effective-access explanation, participants, catalogs, templates, profiles, assets, origins, publication/notification/retention policy, and audit.
- Administrative forms MUST handle field validation, optimistic version conflicts, idempotent retries, interruption, unsaved changes, limits, and archived dependencies.
- Product and tenant identity MUST remain visible, and the transition to Dashboard MUST preserve only a validated relative destination.
- Admin MUST use only its own GraphQL operation documents and MUST pin the exact private design-system package version.
- The product MUST meet the TechSpec responsive, browser, Portuguese localization, keyboard, focus, reduced-motion, and WCAG 2.2 AA contracts.
- Existing Dashboard operations and Capture responsibilities MUST NOT be implemented as Admin routes.
- Route queries MUST be paginated and feature-specific; the current all-domain `DashboardOverview(first: 100)` query MUST NOT be copied.
- Any Admin query contract completed by task 02 MUST be consumed directly rather than replaced with client-side joins or hidden bulk data.
</requirements>

## Subtasks

- [ ] 4.1 Establish the standalone Admin project, route shell, metadata, configuration, code generation, and product-specific GraphQL transport.
- [ ] 4.2 Deliver PKCE sign-in/callback, in-memory session, tenant selection, entitlement guard, safe deep links, denial, and Dashboard transition.
- [ ] 4.3 Deliver tenant and business-unit configuration with defaults, inheritance, lifecycle impact, conflict, and unsaved-change behavior.
- [ ] 4.4 Deliver membership invitations, role/product/scope assignment, descendant previews, effective-access explanation, resend, and revocation.
- [ ] 4.5 Deliver participant, delivery-channel, segment, template, analysis-profile, asset, assignment, and origin administration.
- [ ] 4.6 Deliver publication, notification, retention, privacy, and audit governance screens.
- [ ] 4.7 Deliver search, cursor paging, filters, empty/error states, responsive behavior, and accessibility across Admin.
- [ ] 4.8 Implement the assigned Vitest, integration, and Admin Playwright journeys.
## Implementation Details

Follow the TechSpec “Frontend Project Structure” and “Frontend Route Ownership.” Split the existing monolithic dashboard page by Admin feature ownership; do not import code from Dashboard or Capture, and do not implement authorization only by hiding controls.

### Relevant Files

- `apps/web/app/(dashboard)/page.tsx` — current combined administrative and operational UI to separate.
- `apps/web/app/auth/callback/page.tsx` — current PKCE callback behavior.
- `apps/web/src/auth/pkce.ts` and `session.ts` — current in-memory internal session baseline.
- `apps/web/src/graphql/client.ts` — current mixed operation transport to split.
- `apps/web/src/graphql/documents/dashboard.graphql` and `generated.ts` — existing internal documents/types.
- `apps/web/src/locales/pt-BR.ts` and `app/styles.css` — current copy/style input.
- `apps/web/codegen.ts`, `vitest.config.ts`, `playwright.config.ts`, and `package.json` — pinned toolchain.
- `apps/web/tests/unit/pkce.test.ts` and `next-config.test.ts` — auth and product security configuration baselines.
- `services/inspection/schema.graphqls` — canonical Admin contract.

### Dependent Files

- `apps/admin/app/` and `src/features/` — new route and feature ownership.
- `apps/admin/src/auth/` and `src/graphql/` — isolated audience, session, transport, documents, and generated types.
- `apps/admin/tests/unit/` and `tests/e2e/` — assigned component and Admin journey suites.
- `packages/inspection-design-system/` — exact published dependency from task 03.
- `deploy/docker-compose.yml`, `deploy/keycloak/inspection-realm.json`, and verification scripts — wired by task 07.
- `apps/web/` — retired by task 07 only after Admin, Dashboard, and Capture parity.

### Related ADRs

- [ADR-001: Split the Existing Web Experience into Three Independent Frontends](adrs/adr-001.md) — Admin product boundary.
- [ADR-005: Use Three Standalone Frontend Projects in One Repository](adrs/adr-005.md) — isolated project/release contract.
- [ADR-006: Use Separate OIDC Clients with Shared SSO and One GraphQL API](adrs/adr-006.md) — Admin auth.
- [ADR-007: Separate Product Entitlements from Roles and Resolve Scope Hierarchies at Read Time](adrs/adr-007.md) — access UI.

## Deliverables

- Independently installable, buildable, deployable, and testable `apps/admin`.
- Complete tenant administration, structural registration, access, governance, and audit experience.
- Secure `inspection-admin` PKCE/session/product transition and generated Admin GraphQL client.
- Assigned Admin unit, integration, E2E, responsive, and accessibility coverage.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; read each full definition before implementation.

- [ ] Unit: UT-053, UT-054, UT-055, UT-071 — Admin PKCE/session safety and GraphQL transport.
- [ ] Integration: IT-011, IT-012, IT-013, IT-014, IT-015, IT-016, IT-017, IT-018, IT-019, IT-020, IT-031, IT-032, IT-033, IT-034, IT-035, IT-036, IT-037, IT-038, IT-039, IT-040, IT-041, IT-042, IT-043, IT-044, IT-045, IT-046, IT-047, IT-048, IT-049, IT-050 — tenant, business-unit, catalog, asset, access-governance, and interruption/scale edge behavior.
- [ ] End-to-end: E2E-005, E2E-006, E2E-007, E2E-008, E2E-009, E2E-010, E2E-011, E2E-012, E2E-013, E2E-014, E2E-015, E2E-016, E2E-017, E2E-018, E2E-019, E2E-020, E2E-021, E2E-022 — US-002 through US-005 Admin acceptance journeys.

## Success Criteria

- Every assigned test case implemented and passing.
- A user without current Admin entitlement receives no administrative data or mutation surface.
- All Admin PRD journeys complete through the real GraphQL boundary.
- Admin clean install, codegen, lint, unit tests, production build, and browser suite run independently.
