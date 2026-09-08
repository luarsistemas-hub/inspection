---
status: pending
title: Three-Product Runtime, CI and Cutover
type: infra
complexity: critical
---

# Task 7: Three-Product Runtime, CI and Cutover

## Overview

Complete the operational separation by wiring three images, origins, OIDC clients, local commands, smoke/security/load gates, and independent CI releases. This task also proves cross-product SSO and isolation, then removes the obsolete combined `apps/web` only after all product parity and runtime gates pass.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- Docker Compose MUST run Admin on 3000, Dashboard on 3002, Capture on 3003, and preserve Gotenberg host port 3001.
- Keycloak realm configuration MUST contain exact `inspection-admin` and `inspection-dashboard` public clients plus the least-privilege provisioning service account.
- API runtime configuration MUST provide exact origins/audiences and MUST NOT use wildcard CORS, shared frontend audiences, or leaked client secrets.
- Local scripts MUST start, stop, validate ports, and report URLs for all three frontend services independently and together.
- Verification MUST run clean install, audit, codegen check, lint, unit, production build, and product browser suites for each independent lockfile.
- CI MUST publish immutable design-system versions and independently build/release each frontend image while retaining canonical schema compatibility checks.
- Smoke/security tests MUST prove SSO continuity, independent entitlements, wrong-audience denial, Capture isolation, revocation, sensitive media denial, and exact-origin CORS.
- Load/observability gates MUST enforce the TechSpec latency, hierarchy, notification, and Capture concurrency targets with product-specific health/release metrics.
- `apps/web` MUST be removed only after all three replacement apps and cross-product acceptance gates pass.
- No compatibility redirect, old capture-link support, npm workspace, shared lockfile, or cross-app source import may remain.
</requirements>

## Subtasks

- [ ] 7.1 Add three Compose frontend services, product images, ports, health checks, and exact runtime environments.
- [ ] 7.2 Replace the single Keycloak client with Admin/Dashboard clients and configure least-privilege invitation provisioning.
- [ ] 7.3 Update local development, smoke, security, and load scripts for three origins and independent services.
- [ ] 7.4 Update root verification/CI for design-system publication and three isolated install/codegen/lint/test/build/image pipelines.
- [ ] 7.5 Add product-specific structured logging, metrics, alerts, reconciliation, and release-version visibility.
- [ ] 7.6 Deliver cross-product SSO, entitlement, deep-link, tenant, CORS, session, and route-ownership browser contracts.
- [ ] 7.7 Run schema compatibility, full Compose, browser matrix, accessibility, security, offline, and capacity gates.
- [ ] 7.8 Remove `apps/web` and obsolete `inspection-web` configuration only after all replacement gates pass.
## Implementation Details

Follow the TechSpec “Development Sequencing,” “Integration Points,” and “Monitoring and Observability.” This is the only task authorized to perform final cutover/removal; preserve unrelated user changes and validate exact targets before deleting the known combined app.

### Relevant Files

- `deploy/docker-compose.yml` — current single `web` service and Gotenberg port.
- `deploy/keycloak/inspection-realm.json` — current `inspection-web` client, audience, redirect, and origin.
- `scripts/local.sh` and `scripts/dev.sh` — current single frontend orchestration and port checks.
- `scripts/verify.sh` — current single-app verification pipeline.
- `scripts/smoke.sh`, `security-smoke.sh`, and `load-smoke.sh` — full-system acceptance.
- `.github/workflows/ci.yml` — current single frontend job to split into package/product/cross-product gates.
- `.env.example` — public configuration names, product ports, origins, audiences, and Capture base URL.
- `docs/frontend.md`, `docs/configuration.md`, `docs/development.md`, and root/deploy/script READMEs — current single-web documentation.
- `services/inspection/internal/platform/config/config.go` — multi-origin/audience runtime contract from task 01.
- `services/inspection/internal/platform/database/migrations/planner.go` — schema compatibility window.
- `apps/admin/Dockerfile`, `apps/dashboard/Dockerfile`, and `apps/capture/Dockerfile` — replacement images.
- `apps/*/codegen.ts` and `playwright.config.ts` — independent schema and browser suites.

### Dependent Files

- `apps/web/` — explicitly removed after successful replacement gates.
- `apps/admin/package-lock.json`, `apps/dashboard/package-lock.json`, and `apps/capture/package-lock.json` — independent reproducible installs.
- `packages/inspection-design-system/package.json` — private package publication metadata.
- `services/inspection/internal/platform/graphql/generated.go` and each app's `generated.ts` — schema drift checks.
- Repository CI workflow files discovered at implementation time — independent package/image pipelines.
- Deployment documentation and environment examples — three URLs, audiences, origins, and ports.
- `tests/cross-product/` — new independently locked Playwright harness for product-boundary journeys.
- `services/inspection/internal/features/invitations/dispatch_capture_invitation/setup.go` — configurable Capture URL composition before cutover.

### Related ADRs

- [ADR-001: Split the Existing Web Experience into Three Independent Frontends](adrs/adr-001.md) — cutover objective.
- [ADR-005: Use Three Standalone Frontend Projects in One Repository](adrs/adr-005.md) — build/release topology and no legacy URLs.
- [ADR-006: Use Separate OIDC Clients with Shared SSO and One GraphQL API](adrs/adr-006.md) — Keycloak and API runtime.
- [ADR-007: Separate Product Entitlements from Roles and Resolve Scope Hierarchies at Read Time](adrs/adr-007.md) — security probes.
- [ADR-008: Publish Reports Through an Audited Visibility Ledger and Customer-Safe Projections](adrs/adr-008.md) — observability and leakage probes.

## Deliverables

- Local and deployable three-product runtime with exact ports, origins, audiences, health checks, and images.
- Keycloak SSO/client/provisioning configuration and environment documentation.
- Independent design-system/frontend CI release pipelines and full verification commands.
- Cross-product security, route, schema, migration, smoke, load, and observability gates.
- Verified removal of the combined `apps/web` and obsolete `inspection-web` configuration.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; read each full definition before implementation.

- [ ] Unit: UT-074, UT-075 — shared transport failure shape and route ownership.
- [ ] Integration: IT-219, IT-230 — three-app schema generation and deployment compatibility window.
- [ ] End-to-end: E2E-001, E2E-002, E2E-003, E2E-004 — cross-product Admin/Dashboard SSO, entitlement, denial, and context journeys.

## Success Criteria

- Every assigned test case implemented and passing.
- `compozy tasks validate --name three-frontend-experience`, root verification, Compose validation, smoke, security, and load gates pass.
- Each frontend installs, builds, tests, images, and releases independently against one canonical GraphQL schema.
- The three documented URLs serve only their owned product routes with correct auth/session boundaries.
- `apps/web` is absent only after parity evidence is green and no unrelated files are removed.
