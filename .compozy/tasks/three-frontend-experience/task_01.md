---
status: pending
title: Access, Identity and Authorization Foundation
type: backend
complexity: critical
---

# Task 1: Access, Identity and Authorization Foundation

## Overview

Deliver the shared security foundation required before any standalone product can safely consume the API. This slice adds product entitlements, customer invitations, hierarchical scope resolution, multi-audience OIDC, multi-origin CORS, and the GraphQL access contracts while preserving current tenant isolation.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- The database MUST add tenant-isolated product entitlement and invitation state while preserving existing membership and resource-scope names.
- Membership roles MUST add `CUSTOMER_VIEWER`; product access MUST be independent and limited to `ADMIN` and `DASHBOARD`.
- Customer memberships MUST NOT receive `ADMIN`, and non-`TENANT_ADMIN` memberships MUST NOT receive administrative authority.
- Explicit scopes MUST accept business unit, asset, project, and inspection targets and MUST resolve descendants from current canonical relationships.
- Authorization MUST reload membership, entitlement, and scope state for every protected action and fail closed after revocation, tenant deactivation, or hierarchy movement.
- OIDC MUST accept only the configured `inspection-admin` and `inspection-dashboard` audiences and retain verified audience in request context.
- CORS MUST use an exact multi-origin allowlist, reject wildcards/lookalikes, and permit credentialed Capture requests only from its configured origin.
- Customer invitation provisioning MUST use a slice-local Keycloak port, durable idempotency, required-action email, retryable provider state, and one bound subject.
- GraphQL MUST expose the modified `me`, access preview/effective access, invitation, and atomic membership-access contracts with stable safe errors.
- Every new table and query MUST retain PostgreSQL tenant RLS, context propagation, optimistic concurrency, audit, and transactional outbox conventions.
- Compatible migration MUST backfill current memberships as `TENANT_ADMIN → ADMIN + DASHBOARD` and other active internal roles as `DASHBOARD` to prevent lockout.
- Tenant bootstrap MUST create the first administrator's two product entitlements atomically with the membership.
- A provisioned invitation MUST become active only from the first verified OIDC identity after required actions complete; event payloads MUST carry IDs rather than email or secrets.
- Requests MUST choose exactly one authority model: an OIDC principal or an external Capture session, never a merged authority.
</requirements>

## Subtasks

- [x] 1.1 Add compatible access, entitlement, invitation, constraint, index, and RLS migrations/models.
- [x] 1.2 Extend runtime configuration, OIDC verification, request metadata, and exact-origin CORS.
- [x] 1.3 Replace exact-only authorization with product-, role-, mutation-, tenant-, and hierarchy-aware decisions.
- [ ] 1.4 Deliver effective-access and scope-preview query slices with explainable direct grants.
- [ ] 1.5 Deliver internal/customer invitation and atomic membership-access command slices.
- [ ] 1.6 Deliver the Keycloak provisioning adapter and idempotent retrying invitation consumer.
- [x] 1.7 Evolve the canonical GraphQL access schema, resolvers, composition roots, and generated Go boundary.
- [x] 1.8 Cover security, RLS, migration compatibility, concurrency, idempotency, and revocation contracts.
- [ ] 1.9 Inventory every current GraphQL operation and remove resolver-local authorization bypasses.
## Implementation Details

Follow the TechSpec sections “Core Interfaces,” “Data Models,” “GraphQL API Surface,” and “Authorization Algorithm.” Keep every use case in its own vertical slice with `setup.go` as its public entry; platform packages provide only cross-cutting verification, request context, and adapters.

### Relevant Files

- `services/inspection/internal/platform/database/models.go` — current membership, resource scope, and tenant-owned persistence models.
- `services/inspection/internal/platform/database/migrations/planner.go` — ordered compatible SQL, constraints, indexes, roles, and RLS.
- `services/inspection/internal/platform/auth/authorize.go` — current role/exact-scope authorizer to evolve.
- `services/inspection/internal/platform/auth/store.go` — current-state membership and scope loading.
- `services/inspection/internal/platform/auth/oidc.go` and `remote_oidc.go` — audience verification boundary.
- `services/inspection/internal/platform/requestctx/` — authenticated principal and request metadata.
- `services/inspection/internal/platform/config/config.go` — current single audience/origin configuration.
- `services/inspection/internal/platform/httpboundary/cors.go` — current credentialed single-origin middleware.
- `services/inspection/internal/platform/requestctx/context.go` and `auth/testmode.go` — principal/audience fields and integration-auth fixtures.
- `services/inspection/internal/features/access/` — neighboring invitation, role-scope, and disable-membership slices.
- `services/inspection/internal/platform/graphql/resolvers/` — GraphQL-to-mediator boundary and resolver dependencies.
- `services/inspection/schema.graphqls` — canonical schema.
- `services/inspection/cmd/inspection-api/main.go` — API composition root.
- `services/inspection/internal/integration/harness/` — migrated PostgreSQL and GraphQL test fixtures.
- `services/inspection/internal/features/tenancy/create_tenant/setup.go` — initial administrator entitlement bootstrap.
- `services/inspection/internal/contracts/events/events.go` — ID-only identity-provisioning event registry.

### Dependent Files

- `services/inspection/internal/platform/graphql/generated.go` and `models_gen.go` — regenerated Go boundary after schema changes.
- `services/inspection/cmd/inspection-worker/main.go` — invitation consumer composition.
- `deploy/keycloak/inspection-realm.json` — task 07 consumes the new audience and provisioning contract.
- `deploy/docker-compose.yml` — task 07 supplies exact runtime values.
- `apps/admin/src/graphql/generated.ts` and `apps/dashboard/src/graphql/generated.ts` — downstream generated consumers.
- `scripts/security-smoke.sh` — task 07 exercises product/audience isolation.

### Related ADRs

- [ADR-002: Permissioned and Role-Adaptive Dashboard](adrs/adr-002.md) — product and customer access behavior.
- [ADR-006: Use Separate OIDC Clients with Shared SSO and One GraphQL API](adrs/adr-006.md) — identity and API boundary.
- [ADR-007: Separate Product Entitlements from Roles and Resolve Scope Hierarchies at Read Time](adrs/adr-007.md) — access model.

## Deliverables

- Compatible migrations and models for product entitlements, user invitations, project scopes, constraints, indexes, and RLS.
- Product-aware authorization and exact multi-audience/multi-origin security configuration.
- Effective access, scope preview, customer/internal invitation, and atomic access-assignment GraphQL operations.
- Idempotent Keycloak account activation integration and current-state revocation enforcement.
- Regenerated Go GraphQL boundary and completed unit/integration security contracts.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; read each full definition before implementation.

- [ ] Unit: UT-001, UT-002, UT-003, UT-004, UT-005, UT-006, UT-007, UT-008, UT-009, UT-010, UT-011, UT-012, UT-013, UT-014, UT-015, UT-016, UT-017, UT-018, UT-019, UT-020, UT-021, UT-022, UT-023, UT-048, UT-049, UT-050, UT-051, UT-052 — authorizer, hierarchy, invitation, OIDC, configuration, and CORS.
- [ ] Integration: IT-001, IT-002, IT-003, IT-004, IT-005, IT-006, IT-007, IT-008, IT-009, IT-010, IT-021, IT-022, IT-023, IT-024, IT-025, IT-026, IT-027, IT-028, IT-029, IT-030, IT-181, IT-182, IT-183, IT-184, IT-185, IT-197, IT-198, IT-199, IT-200, IT-201, IT-202, IT-203, IT-204, IT-205, IT-221, IT-222, IT-223, IT-224, IT-225 — product access, hierarchical grants, invitation lifecycle, GraphQL access operations, audience isolation, and Keycloak activation.

## Success Criteria

- Every assigned test case implemented and passing.
- Every protected resolver has explicit product, role, mutation, and resource policy metadata.
- Entitlement or scope revocation denies the next protected interaction without waiting for token expiry.
- Cross-product and cross-tenant probes expose no protected data.
- New access tables are RLS-enforced and schema-compatible across the declared deployment window.
