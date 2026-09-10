---
status: completed
title: Executable Parity Gate and Legacy Retirement
type: infra
complexity: critical
---

# Task 8: Executable Parity Gate and Legacy Retirement

## Overview

Deliver the fail-closed release gate that converts the Task 4 capability contract and Tasks 5 through 7 journeys into repeatable authenticated evidence. Stand up deterministic seeded dependencies, verify GraphQL, generated artifacts, RLS, observability and cross-product recapture, inventory every valid legacy behavior and reference, add intentional historical URL behavior, and remove apps/web only after every prerequisite is green.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. CI MUST run an isolated deterministic seeded stack with Keycloak, PostgreSQL migrations and runtime RLS roles, RabbitMQ/outbox worker, private MinIO, Mailpit/Twilio, and deterministic LiteLLM/Gotenberg doubles using injected test secrets only.
- R2. The release gate MUST execute Task 4 GraphQL/generated/hash checks, real PostgreSQL/RLS integration checks, and authenticated Admin, Dashboard, and Capture Playwright suites without opt-in skips.
- R3. Every one of the 33 query and 56 mutation rows plus every mandatory backend gap MUST have fresh owner, persona, journey, and evidence; any missing, stale, duplicate, or mismatched row MUST block release.
- R4. Evidence MUST preserve schema hash, test IDs, route/journey, run identity and timestamp, and actionable failure artifacts while linking outcomes to correlation, audit and domain operation state without exposing credentials, tokens, contacts, or media.
- R5. Cross-product tests MUST execute manager recapture request in Dashboard through participant replacement in Capture, including delivery, object storage, lineage, resulting state, and audit evidence.
- R6. Legacy inventory MUST classify every route, GraphQL document, behavior/form, test, script, package, generated artifact, CI, runtime, deployment, and documentation reference as migrated, explicitly retired, or blocking.
- R7. Legacy inventory fingerprint drift, hidden active dependencies, premature removal, incomplete replacement states, and scale failures MUST fail closed.
- R8. Historical root, callback, and Capture URLs MUST have intentional safe redirect, ownership, denial, archived/deleted, malformed, and not-found behavior without redirect loops.
- R9. apps/web MUST remain until the complete gate passes; after removal no active build, runtime, CI, deployment, local workflow, documentation, or owned frontend may depend on it, while historical ADR/PRD evidence remains intact.
- R10. Gate interruption and retry MUST remain fail-closed, always upload useful failure artifacts, tear down isolated seeded state, and never silently restore split four-frontend ownership.
</requirements>

## Subtasks

- [x] 8.1 Establish isolated deterministic seeded parity-stack lifecycle and readiness for every process and external boundary.
- [x] 8.2 Extend fixture personas and browser authentication for release owner, delegated internal roles, customer, and participant without committed credentials.
- [x] 8.3 Promote generation, contract, RLS, integration, and all authenticated browser suites into one blocking CI gate with artifacts and cleanup.
- [x] 8.4 Populate fresh evidence for all 89 canonical root operations and every mandatory backend gap using Task 4 manifest and validator.
- [x] 8.5 Capture redacted correlation, audit, domain-operation, provider, outbox, and failure evidence.
- [x] 8.6 Implement parity navigation and the Dashboard-to-Capture recapture completion journeys assigned to this task.
- [x] 8.7 Build and version the exhaustive legacy inventory and fail on drift, hidden references, premature removal, or scale gaps.
- [x] 8.8 Define and test intentional historical route handling, safe denials, archived/deleted state, and loop prevention.
- [x] 8.9 Remove apps/web and obsolete active references only after a green gate while preserving explicit retired history and replacement first-use behavior.
- [x] 8.10 Document repeatable local/CI execution, evidence interpretation, fail-closed cutover and recovery, and implement every assigned test.

## Implementation Details

Follow the TechSpec build-order steps 7 through 9 and ADR-010. Current CI installs browser dependencies but does not execute authenticated Playwright; the current verification script assumes an already-running stack; the existing integration harness identifies itself as scaffolding rather than parity proof. The new gate must own isolated state, readiness, seeding, execution, artifacts and teardown. Inventory must distinguish active dependencies from historical references before any deletion occurs.

### Relevant Files

- `.github/workflows/ci.yml` — blocking generation, integration, seeded stack, authenticated browser, artifact, and cleanup orchestration.
- `deploy/docker-compose.yml` — PostgreSQL, Keycloak, RabbitMQ, MinIO, providers, API, worker, scheduler, and three products.
- `deploy/keycloak/inspection-realm.json` and `deploy/wiremock/` — secret-safe personas and deterministic provider outcomes.
- `.env.example` — variable names and test-stack contract without credential defaults.
- `scripts/local.sh`, `dev.sh`, `verify.sh`, `smoke.sh`, `security-smoke.sh`, and `load-smoke.sh` — local and CI lifecycle/check entry points.
- `scripts/parity-gate.sh` — recommended single parity orchestrator.
- `scripts/lib/legacy-inventory.mjs` and `scripts/lib/parity-evidence.mjs` — recommended deterministic inventory and evidence tooling.
- `services/inspection/schema.graphqls`, `gqlgen.yml`, and generated Go artifacts — canonical generation inputs.
- Task 4 operation manifest and `services/inspection/internal/platform/graphqlcontract/` — schema hash, operation ownership, and validation.
- `services/inspection/internal/platform/graphql/` contract tests and `internal/platform/database/integration_test.go` — GraphQL and forced-RLS evidence.
- `services/inspection/internal/integration/harness/` — process, fixture, GraphQL, event, provider, observability, and wait support.
- `services/inspection/internal/features/development/seed_qa/setup.go` and `services/inspection/cmd/inspection-seed/` — deterministic product/persona scenario graph.
- `services/inspection/internal/features/audit/`, `platform/graphql/errors.go`, and `platform/observability/sanitize.go` — correlation, audit, and redaction evidence.
- `apps/admin/tests/e2e/`, `apps/dashboard/tests/e2e/`, and `apps/capture/tests/e2e/` — authenticated product journeys and artifacts.
- `apps/dashboard/tests/e2e/recapture-cross-product.spec.ts` — recommended Dashboard-to-Capture journey for E2E-027 and E2E-060.
- `.compozy/tasks/graphql-frontend-capability-parity/_capability_matrix.md` — canonical owner/journey/evidence source.
- `apps/web/` — complete legacy route, document, behavior, test, package, generated, configuration, asset, and removal surface.
- `docs/qa/2026-09-08/` — existing evidence and failure-artifact expectations.
- `docs/parity-release-gate.md` — recommended repeatable operator runbook.

### Dependent Files

- `README.md`, `AGENTS.md`, `docs/development.md`, `docs/frontend.md`, `docs/configuration.md`, `docs/README.md`, `docs/operations.md`, `deploy/README.md`, and `scripts/README.md` — remove active legacy guidance after green parity.
- `.gitignore` and `.dockerignore` — remove obsolete apps/web-specific entries after cutover.
- `apps/admin/next.config.ts`, Dockerfile and environment examples — intentional historical route handling and current product ownership.
- Task 5 through Task 7 routes and browser suites — compose existing journeys; do not reimplement product behavior in the gate.
- Product generated operations and the Task 4 manifest — consume as generated evidence and never edit manually during gate execution.

### Related ADRs

- [ADR-001: Full Capability Parity and Backend-First Delivery](adrs/ADR-001-full-capability-parity-and-delivery-order.md)
- [ADR-003: Tenant Isolation and Delegated Administration](adrs/ADR-003-tenant-isolation-and-delegated-administration.md)
- [ADR-005: Customer Publication and Communication Policy](adrs/ADR-005-customer-publication-and-communication-policy.md)
- [ADR-008: Generated GraphQL Operations](adrs/adr-008-generated-graphql-operations.md)
- [ADR-009: Domain-Specific Observable Operation State](adrs/adr-009-domain-operation-state.md)
- [ADR-010: Executable Parity Release Gate](adrs/adr-010-parity-release-gate.md)
- [ADR-011: Fixed Operational Boundaries](adrs/adr-011-operational-boundaries.md)

## Deliverables

- Deterministic isolated parity stack, multi-persona fixtures, blocking CI gate, authenticated browser execution, evidence artifacts, and cleanup.
- Fresh machine-checkable coverage for every canonical GraphQL operation and required backend capability.
- Cross-product recapture evidence linked to delivery, object storage, lineage, operation state, audit, and correlation.
- Versioned legacy inventory, intentional historical URL behavior, fail-closed cutover procedure, and operator runbook.
- Removal of apps/web and obsolete active references only after the complete parity gate passes.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract. These IDs are exclusive to the release gate and cross-product cutover.

- [x] Unit baseline: UT-129 — legacy inventory detects every route, document, script, package, generated artifact, and deployment reference.
- [x] Unit edge cases: UT-165.01, UT-165.02, UT-165.03, UT-165.04, UT-165.05, UT-165.06, UT-165.07, UT-165.08, UT-165.09, UT-165.10 — US-033 EC-1 through EC-10.
- [x] Integration baseline: IT-033 — removal gate fails while any active apps/web dependency remains.
- [x] Integration edge cases: IT-066.01, IT-066.02, IT-066.03, IT-066.04, IT-066.05, IT-066.06, IT-066.07, IT-066.08, IT-066.09, IT-066.10 — US-033 EC-1 through EC-10.
- [x] E2E parity and cutover: E2E-001, E2E-027, E2E-033, E2E-034.01, E2E-060, E2E-066 — owned-operation evidence, cross-product recapture, complete gate, US-001 edge evidence, US-027 edge evidence, and legacy edge evidence.

## Success Criteria

- Every assigned test case implemented and passing.
- The blocking job starts, seeds, exercises, captures artifacts from, and tears down an isolated complete stack.
- Every canonical operation and mandatory backend gap has fresh owner/persona/journey/evidence tied to the final schema hash.
- Customer withdrawal, tenant isolation, delegated denial, async operation state, and cross-product recapture are proven through authenticated execution.
- Legacy inventory contains no unclassified or active blocking reference, and historical URLs have intentional safe outcomes.
- apps/web is removed only after the green gate and no active repository workflow depends on it afterward.
