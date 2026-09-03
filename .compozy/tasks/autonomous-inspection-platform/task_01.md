---
status: pending
title: "Foundation, Tenant Isolation, Access & Audit"
type: infra
complexity: critical
---

# Task 1: Foundation, Tenant Isolation, Access & Audit

## Overview

Establish the new inspection service and the security/runtime foundation on which every later slice depends. This task delivers executable composition roots, controlled schema evolution, tenant-safe persistence, internal identity and authorization, auditability, GraphQL/REST foundations, and operational diagnostics without extending the Contract Service POC.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- The implementation MUST create `services/inspection` as a modular monolith organized by operation-level vertical slices with one exported `Setup` entry point per operation.
- API, worker, scheduler, and migrator composition roots MUST share feature code without importing `internal` packages from `services/contract`.
- Only `inspection-migrate` MUST change schemas; additive GORM migration and checksum-protected advanced migrations MUST be separated, retry-safe, and compatibility-gated.
- Every tenant-owned table MUST have mandatory `tenant_id`, tenant-aware indexes, application authorization, and forced PostgreSQL RLS under a non-owner runtime role.
- The typed mediator MUST preserve request context, tenant, principal, correlation, cancellation, and typed errors while rejecting duplicate or missing handlers.
- Internal authentication MUST use OIDC identity mapped to local memberships, roles, and scopes evaluated on every protected action.
- GraphQL and operational REST boundaries MUST expose stable, redacted errors, cursor pagination, idempotency behavior, health, readiness, and private metrics.
- Tenant bootstrap MUST atomically create the first tenant, business unit, membership, and durable idempotency result without introducing a general RLS bypass.
- The foundation MUST expose migration/model registration and durable audit/outbox-intent contracts that later slices can use through their single `Setup` entry point.
- Tenant, business-unit, access, and audit slices MUST satisfy US-001, US-002, and US-004 without weakening historical ownership or cross-tenant isolation.
- Structured logs, traces, and metrics MUST exclude secrets and unbounded tenant labels while retaining correlation evidence.
- The Contract Service POC MUST remain untouched until the final acceptance gate in Task 7.
</requirements>

## Subtasks

- [ ] 1.1 Establish inspection service directories, four composition roots, dependency injection, lifecycle, and build wiring.
- [ ] 1.2 Establish pinned local/CI infrastructure definitions and configuration validation required by the foundation.
- [ ] 1.3 Deliver the dedicated migrator, model registration, schema registry, runtime compatibility gate, and migration test harness.
- [ ] 1.4 Deliver tenant transaction handling, runtime database roles, forced RLS, and adversarial isolation coverage.
- [ ] 1.5 Deliver the typed command/query mediator and operation-level registration contracts.
- [ ] 1.6 Deliver gqlgen/Chi boundaries, stable errors, pagination, idempotency, health, readiness, and metrics.
- [ ] 1.7 Deliver OIDC verification, local membership resolution, hierarchical authorization, and immediate revocation behavior.
- [ ] 1.8 Deliver tenant bootstrap, business-unit, internal-access, durable idempotency, and audit vertical slices with their GraphQL operations.
- [ ] 1.9 Deliver OpenTelemetry-compatible tracing, structured logging, safe metrics, and operational correlation.
- [ ] 1.10 Integrate CI verification for migrations, generated contracts, Go tests, vet, build, and schema compatibility.

## Implementation Details

Follow the repository VSA contract and the TechSpec sections “Repository Topology,” “Vertical Slice Boundaries,” “Data Model,” “GraphQL API,” and “Monitoring and Observability.” GORM models stay with their owning slices; `internal/platform` contains only process-wide mechanisms. Use explicit SQL inside versioned migrations for RLS, roles, locks, and constraints that GORM cannot safely express.

### Relevant Files

- `AGENTS.md` — mandatory repository architecture, testing, and build rules.
- `go.mod`, `go.sum`, `go.work`, `go.work.sum` — current single Go workspace and dependencies to evolve.
- `libs/identity/id.go` — existing UUID helper that the new service may reuse.
- `libs/identity/id_test.go` — current helper coverage; zero UUID emptiness requires explicit validation before `TenantTx` uses it.
- `services/contract/cmd/contract-service/main.go` — composition-root behavior to replace, not import.
- `services/contract/internal/features/contracts/create/setup.go` — canonical single-public-`Setup` slice example.
- `services/contract/internal/features/contracts/create/handler_test.go` — existing route-level slice test pattern.
- `services/contract/internal/platform/config/config.go` — legacy configuration behavior that must not be copied blindly.
- `deploy/docker-compose.yml` — local dependencies and unsafe public MinIO behavior to replace.

### Dependent Files

- `services/inspection/cmd/inspection-api/main.go` — API composition root created by this task.
- `services/inspection/cmd/inspection-worker/main.go` — worker root established for Task 2 registration.
- `services/inspection/cmd/inspection-scheduler/main.go` — scheduler root established for Task 4 registration.
- `services/inspection/cmd/inspection-migrate/main.go` — exclusive migration entry point.
- `services/inspection/gqlgen.yml` — schema-first generation configuration.
- `services/inspection/internal/platform/{config,database,tenanttx,mediator,graphql,auth,observability}/` — shared runtime mechanisms.
- `services/inspection/internal/features/{tenancy,access,audit}/` — operation-level vertical slices.
- `deploy/docker-compose.yml` — pinned local runtime topology.
- `.github/workflows/ci.yml` — generated contracts, migrations, isolation, tests, vet, and build gates.

### Related ADRs

- [ADR-007: Apply Hierarchical Tenant and Business-Unit Access](adrs/adr-007.md) — authorization semantics.
- [ADR-008: Build a New Inspection Service as Vertical Slices](adrs/adr-008.md) — service and slice boundaries.
- [ADR-009: Isolate Tenants with PostgreSQL Row-Level Security](adrs/adr-009.md) — database isolation.
- [ADR-010: Use GORM with a Dedicated Schema Migrator](adrs/adr-010.md) — persistence and migration ownership.
- [ADR-011: Coordinate Slices Through a Typed In-Process Bus](adrs/adr-011.md) — synchronous slice collaboration.
- [ADR-012: Use gqlgen and Browser-Appropriate Session Models](adrs/adr-012.md) — API/auth boundary.
- [ADR-017: Design for the Confirmed MVP Capacity and Latency Envelope](adrs/adr-017.md) — foundation performance targets.

## Deliverables

- Runnable inspection API, worker, scheduler, and migrator foundations with safe shutdown and readiness behavior.
- Migrated tenant/access/audit schemas with forced RLS and local authorization.
- Working internal identity, role/scope enforcement, tenant/business-unit administration, and audit APIs.
- Generated GraphQL foundation and operational REST endpoints with stable error contracts.
- CI and observability foundation suitable for every later task.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there before writing tests.

- [ ] Unit: UT-001, UT-002, UT-003, UT-004, UT-005, UT-006, UT-017, UT-018, UT-042, UT-043, UT-054, UT-055, UT-056, UT-057, UT-058, UT-059, UT-070, UT-071
- [ ] Integration: IT-001, IT-002, IT-003, IT-004, IT-005, IT-006, IT-007, IT-008, IT-009, IT-010, IT-011, IT-012, IT-013, IT-014, IT-015, IT-016, IT-017, IT-018, IT-019, IT-020, IT-031, IT-032, IT-033, IT-034, IT-035, IT-036, IT-037, IT-038, IT-039, IT-040, IT-341, IT-342, IT-343, IT-344, IT-345, IT-346, IT-347, IT-348, IT-349, IT-350, IT-351, IT-352, IT-353, IT-389, IT-391, IT-392, IT-393, IT-394, IT-395, IT-396, IT-431, IT-432, IT-439, IT-440, IT-441, IT-442, IT-443, IT-444, IT-445, IT-446, IT-447, IT-448, IT-449, IT-450, IT-451, IT-452, IT-535, IT-536, IT-537, IT-538, IT-539, IT-540, IT-587, IT-588, IT-589, IT-590, IT-591, IT-592, IT-593, IT-597, IT-598, IT-599, IT-600

## Success Criteria

- Every assigned test case implemented and passing.
- A non-owner runtime role cannot access tenant data without the correct transaction-local tenant context.
- API, worker, and scheduler refuse readiness against an incompatible schema while only the migrator can change it.
- All tenant/access/audit operations use vertical-slice `Setup` registration and stable GraphQL contracts.
- Go tests, vet, build, migration contracts, and tenant-isolation suites pass in CI.
