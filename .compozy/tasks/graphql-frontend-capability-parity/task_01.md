---
status: completed
title: Membership Context and Super Admin Foundation
type: backend
complexity: critical
---

# Task 1: Membership Context and Super Admin Foundation

## Overview

Deliver the identity and membership boundary required for every protected Admin and Dashboard request. This slice removes implicit tenant selection, supports one OIDC identity across multiple tenants, and provisions the fixed Admin identity through injected secrets without weakening tenant isolation or Capture security.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. OIDC authentication MUST establish issuer, subject, audience, and actor identity without choosing a tenant membership implicitly.
- R2. Protected Admin and Dashboard requests MUST resolve X-Inspection-Membership-ID on every request, verify ownership and active state, and derive tenant/RLS context only from that membership.
- R3. Malformed, foreign, disabled, or inactive membership selections MUST return the same non-disclosing denial outcome.
- R4. The identity-owned selector MUST expose only stable, paginated membership and tenant summary data owned by the verified subject and MUST NOT enable cross-tenant business queries.
- R5. Membership persistence and RLS MUST support the same OIDC identity in multiple tenants while preserving tenant isolation for business data.
- R6. Request metadata MUST be stateless and safe for concurrent tabs; no membership or tenant context may leak between requests.
- R7. Delegated administrative role constants and database constraints MUST include the six approved roles plus AUDITOR; their full permission matrices belong to Task 2.
- R8. Tenant creation MUST provision exactly one active TENANT_ADMIN membership for the fixed Admin identity atomically and idempotently.
- R9. Admin credentials and provisioning secrets MUST be injected by the environment, MUST NOT have committed defaults, and MUST be redacted from logs, responses, snapshots, and generated artifacts.
- R10. Capture MUST retain its link-scoped cookie, CSRF, and responsibility boundary without accepting the internal membership header as authority.
</requirements>

## Subtasks

- [x] 1.1 Separate verified OIDC identity establishment from tenant membership selection.
- [x] 1.2 Deliver the paginated identity-owned membership selector and active-membership summary.
- [x] 1.3 Resolve and revalidate X-Inspection-Membership-ID for every protected internal request.
- [x] 1.4 Make request metadata and tenant transactions stateless and safe across concurrent tabs.
- [x] 1.5 Evolve membership identity, uniqueness, indexes, constraints, and RLS for multi-tenant identities.
- [x] 1.6 Establish delegated administrative role constants and compatible database constraints.
- [x] 1.7 Provision the fixed Admin membership and required entitlements atomically during tenant creation.
- [x] 1.8 Replace committed product credentials with secret-backed idempotent Keycloak provisioning.
- [x] 1.9 Wire configuration, CORS, membership resolution, audit identity, and composition roots.
- [x] 1.10 Implement every assigned unit and PostgreSQL/RLS integration case.

## Implementation Details

Follow the TechSpec sections “Core Interfaces,” “Integration Points,” and “Development Sequencing.” Authentication verifies identity first; the selected membership is a server-validated selector, and only the resolved membership may establish tenant transactions. UT-007 is backend-owned here as proof that each request rebuilds context and never reuses prior tenant metadata; browser cache clearing is completed in Task 5.

### Relevant Files

- `services/inspection/internal/platform/auth/oidc.go` — establish verified identity without arbitrary membership selection.
- `services/inspection/internal/platform/auth/store.go` — resolve an exact owned membership and list selector-safe summaries.
- `services/inspection/internal/platform/auth/authorize.go` — delegated role constants and current-membership validation.
- `services/inspection/internal/platform/requestctx/context.go` — separate verified identity from selected membership metadata.
- `services/inspection/internal/platform/tenanttx/tenanttx.go` — establish tenant-scoped transactions after membership validation.
- `services/inspection/internal/platform/httpboundary/cors.go` — allow the membership header for internal products.
- `services/inspection/internal/platform/config/config.go` — validate secret-backed bootstrap settings.
- `services/inspection/internal/platform/database/models.go` — membership identity, indexes, and selector projection state.
- `services/inspection/internal/platform/database/migrations/planner.go` — compatible uniqueness, role, and forced-RLS migration.
- `services/inspection/internal/features/tenancy/create_tenant/setup.go` — atomic and idempotent fixed-Admin membership provisioning.
- `services/inspection/internal/features/access/list_identity_memberships/setup.go` — new identity-owned selector slice.
- `services/inspection/internal/platform/auth/membership_context.go` — new per-request membership resolver boundary.
- `services/inspection/cmd/inspection-api/main.go` — compose OIDC, selector exception, membership middleware, and GraphQL.
- `deploy/keycloak/inspection-realm.json` — remove committed product credentials and reusable client secrets.
- `deploy/docker-compose.yml` and `scripts/local.sh` — inject and run secret-backed provisioning.

### Dependent Files

- `services/inspection/schema.graphqls` — Task 4 exposes selector and active-membership contracts.
- `services/inspection/internal/platform/graphql/resolvers/schema.resolvers.go` — Task 4 maps selector queries without direct persistence.
- `services/inspection/internal/features/access/invite_internal_user/setup.go`, `assign_role_scope/setup.go`, and `disable_membership/setup.go` — Task 2 consumes stable identity and delegated-role contracts.
- `services/inspection/internal/features/audit/record_event/setup.go` — consumes actor, tenant, membership, product, and correlation metadata.
- `services/inspection/internal/integration/harness/graphql.go` and `fixtures.go` — represent one subject with multiple memberships.
- `apps/admin/src/auth/session.ts` and `apps/admin/src/graphql/client.ts` — Task 5 stores and attaches per-tab membership context.
- `apps/dashboard/src/auth/session.ts` and `apps/dashboard/src/graphql/client.ts` — Task 6 consumes the same header contract.
- `README.md` and `deploy/README.md` — Task 8 removes committed-credential guidance.

### Related ADRs

- [ADR-003: Tenant Isolation and Delegated Administration](adrs/ADR-003-tenant-isolation-and-delegated-administration.md)
- [ADR-006: Fixed Cross-Environment Super Admin Credential](adrs/ADR-006-fixed-cross-environment-super-admin-credential.md)
- [ADR-007: Server-Resolved Membership Context](adrs/adr-007-server-resolved-membership-context.md)
- [ADR-012: Secret-Provisioned Fixed Super Administrator](adrs/adr-012-secret-provisioned-super-admin.md)

## Deliverables

- Multi-tenant identity and membership persistence with compatible migrations, indexes, constraints, and forced RLS.
- Identity-owned membership selection and per-request membership-context resolution.
- Secret-backed, idempotent fixed-Admin provisioning with no credential material in repository artifacts.
- Delegated-role foundations, CORS support, API wiring, audit metadata, and integration fixtures.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there and the corresponding US-002 edge case in `_user_stories.md` before writing tests.

- [x] Unit: UT-005, UT-006, UT-007, UT-008 — membership ownership, generic denial, request-context replacement, and bootstrap replay.
- [x] Unit edge cases: UT-134.01, UT-134.02, UT-134.03, UT-134.04, UT-134.05, UT-134.06, UT-134.07, UT-134.08, UT-134.09, UT-134.10 — US-002 EC-1 through EC-10.
- [x] Integration: IT-002 — two memberships for one subject select independent RLS transactions.
- [x] Integration edge cases: IT-035.01, IT-035.02, IT-035.03, IT-035.04, IT-035.05, IT-035.06, IT-035.07, IT-035.08, IT-035.09, IT-035.10 — US-002 EC-1 through EC-10 across the real HTTP/PostgreSQL boundary.

## Success Criteria

- Every assigned test case implemented and passing.
- One OIDC subject can select multiple memberships without cross-tenant row visibility.
- The next request fails closed after membership or tenant deactivation.
- Concurrent requests from different tabs cannot reuse each other's tenant metadata.
- Tenant creation and replay produce one fixed-Admin membership without exposing secret material.
