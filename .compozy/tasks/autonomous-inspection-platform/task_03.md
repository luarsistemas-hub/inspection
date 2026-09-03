---
status: pending
title: "Declarative Catalog, Participants & Assets"
type: backend
complexity: high
---

# Task 3: Declarative Catalog, Participants & Assets

## Overview

Deliver the tenant-managed configuration and inspectable-asset catalog shared by property, construction, cleaning, and future declarative segments. This task establishes immutable published definitions and policies plus participant/channel and asset context that later occurrence and capture slices snapshot rather than reinterpret.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- Participant slices MUST store normalized contacts, verification history, segment roles, lifecycle, and every marked delivery channel without defining a single preferred channel.
- A selected delivery channel MUST be verified and active, and historical delivery records MUST remain unchanged after contact updates.
- Segment definitions, templates, and analysis profiles MUST validate versioned schemas, semantic references, safe applicability expressions, fixed platform limits, and immutable canonical digests before publication.
- Template versions MUST declare comparison mode, capture requirements, GPS/default geofence behavior, stage behavior, report-mode flag, participant roles, and analysis-profile reference.
- Activation MUST select exactly one effective published version for future work while existing snapshots remain reproducible.
- Assets MUST use universal tenant/business-unit identity plus segment-versioned attributes, participant relationships, coordinates, geofence, template selection, and policy overrides.
- Asset, participant, and configuration reads/writes MUST enforce hierarchical scope, RLS, optimistic concurrency, cursor pagination, and idempotency.
- Property, construction, and cleaning seed definitions/templates MUST use the same generic model and no visual template editor.
- The implementation MUST use mediator queries rather than cross-capability repository access.
</requirements>

## Subtasks

- [ ] 3.1 Deliver participant, contact, verification, channel-selection, and lifecycle vertical slices.
- [ ] 3.2 Deliver segment-definition publication, version validation, safe expression compilation, and catalog queries.
- [ ] 3.3 Deliver immutable template-version publication, activation, retirement, and analysis-profile publication.
- [ ] 3.4 Deliver policy validation and deterministic template-default/asset-override resolution contracts.
- [ ] 3.5 Deliver generic asset registration, segment attributes, location/geofence, relationships, updates, archive, and scoped queries.
- [ ] 3.6 Deliver curated property, construction, and cleaning seeds with independent multi-stage and report-mode flags.
- [ ] 3.7 Deliver GraphQL schema fragments, resolver registration, connections, mutations, and generated contract fixtures.
- [ ] 3.8 Deliver schema, concurrency, idempotency, permission, scale, and tenant-isolation tests for the catalog.

## Implementation Details

Create one directory per operation under the owning capability and keep GORM models/repository ports private to those operations or to demonstrated capability-level models. Published JSON uses explicit schema versions and canonical digests. New capability tables and advanced constraints are registered through the Task 1 migrator; Task 3 never migrates from slice `Setup`.

### Relevant Files

- `AGENTS.md` — VSA, UUID, context propagation, and testing constraints.
- `libs/identity/id.go` — shared UUID values for catalog entities.
- `services/contract/internal/features/contracts/create/` — existing slice shape, not reusable domain behavior.
- `services/inspection/internal/platform/{graphql,mediator,tenanttx,auth}/` — Task 1 boundaries consumed here.
- `.compozy/tasks/autonomous-inspection-platform/_user_stories.md` — US-003 and US-005–US-007 behavior.

### Dependent Files

- `services/inspection/internal/features/participants/` — participant and channel operations.
- `services/inspection/internal/features/segments/` — definition publication and queries.
- `services/inspection/internal/features/templates/` — templates, policies, profiles, activation, and seed behavior.
- `services/inspection/internal/features/assets/` — asset lifecycle and segment attributes.
- `services/inspection/internal/platform/database/migrations/` — capability schemas, indexes, constraints, and RLS.
- `services/inspection/internal/platform/graphql/` — schema composition and generated boundary types.
- `services/inspection/cmd/inspection-api/main.go` — slice registrations.

### Related ADRs

- [ADR-001: Use a Declarative Multi-Segment Inspection Product Model](adrs/adr-001.md) — shared configurable domain.
- [ADR-003: Support Recurring, Milestone, Manual, and Multi-Stage Inspection Lifecycles](adrs/adr-003.md) — template snapshots consumed by later lifecycle slices.
- [ADR-007: Apply Hierarchical Tenant and Business-Unit Access](adrs/adr-007.md) — catalog scope.
- [ADR-008: Build a New Inspection Service as Vertical Slices](adrs/adr-008.md) — operation packaging.
- [ADR-009: Isolate Tenants with PostgreSQL Row-Level Security](adrs/adr-009.md) — tenant persistence.
- [ADR-010: Use GORM with a Dedicated Schema Migrator](adrs/adr-010.md) — models and migrations.
- [ADR-011: Coordinate Slices Through a Typed In-Process Bus](adrs/adr-011.md) — prerequisite lookup.
- [ADR-012: Use gqlgen and Browser-Appropriate Session Models](adrs/adr-012.md) — schema-first GraphQL boundary.
- [ADR-013: Use Transactional Outbox, RabbitMQ, and Idempotent Consumers](adrs/adr-013.md) — channel-verification event intent without adapter coupling.
- [ADR-017: Design for the Confirmed MVP Capacity and Latency Envelope](adrs/adr-017.md) — catalog scale.

## Deliverables

- Scoped participant directory with verified multi-channel selections.
- Immutable segment, template, and analysis-profile publication/activation APIs.
- Generic assets with schema-validated segment data, geofence, participant relationships, and effective policies.
- Curated seed configuration for property, construction, and cleaning journeys.
- Generated GraphQL contracts and migrated capability schemas.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there before writing tests.

- [ ] Unit: UT-024, UT-025, UT-026, UT-027, UT-062, UT-072, UT-073
- [ ] Integration: IT-021, IT-022, IT-023, IT-024, IT-025, IT-026, IT-027, IT-028, IT-029, IT-030, IT-041, IT-042, IT-043, IT-044, IT-045, IT-046, IT-047, IT-048, IT-049, IT-050, IT-051, IT-052, IT-053, IT-054, IT-055, IT-056, IT-057, IT-058, IT-059, IT-060, IT-061, IT-062, IT-063, IT-064, IT-065, IT-066, IT-067, IT-068, IT-069, IT-070, IT-397, IT-398, IT-399, IT-400, IT-401, IT-402, IT-403, IT-404, IT-405, IT-406, IT-407, IT-408, IT-409, IT-410, IT-453, IT-454, IT-455, IT-456, IT-457, IT-458, IT-459, IT-460, IT-461, IT-462, IT-463, IT-464, IT-465, IT-466, IT-467, IT-468, IT-469, IT-470, IT-471, IT-472

## Success Criteria

- Every assigned test case implemented and passing.
- Published definitions, templates, and profiles cannot be mutated and future activation cannot rewrite existing work.
- Participant channels and asset/configuration resources remain tenant- and scope-isolated through lists and direct identifiers.
- All three initial segments compile through one versioned generic model.
- Catalog operations meet configured validation, pagination, concurrency, and capacity boundaries.
