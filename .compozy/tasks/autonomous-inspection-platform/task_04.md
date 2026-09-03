---
status: pending
title: "Scheduling, Inspection & Project Lifecycle"
type: backend
complexity: high
---

# Task 4: Scheduling, Inspection & Project Lifecycle

## Overview

Deliver creation and lifecycle management for recurring, milestone, manual, and multi-stage inspections. The task turns active configuration into immutable occurrence snapshots and provides scheduler-safe, auditable project/stage transitions without yet implementing participant evidence capture.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- Schedules MUST accept valid RRULEs at daily-or-slower cadence, preserve IANA timezone, resolve DST deterministically, and materialize each due instant at most once.
- Scheduled, milestone, and manual occurrence creation MUST use the same command contract and snapshot tenant, unit, asset, participant, template, policy, reference, analysis profile, deadline, reminders, and source.
- Scheduler replicas MUST claim due work safely and rely on unique business keys and outbox intent rather than process-local leases for correctness.
- At most three reminder instants MUST be snapshotted per occurrence; later configuration changes MUST affect only future unmaterialized work.
- Inspection state transitions MUST enforce cancellation only before evidence, invalidation with reason after evidence, immediate responsibility/session revocation, and valid history visibility.
- Multi-stage projects MUST snapshot planned `ORIGIN`, `INSPECTION`, and exceptional stages, configured ordering, effective reference, and consolidated-versus-historical report flag.
- Exceptional insertion, skip, close, and reopen MUST enforce authorization, reason, terminal-stage conditions, optimistic concurrency, and audit/event emission.
- Lifecycle slices MUST obtain asset/template/participant prerequisites through typed mediator queries and write only their local schema in one tenant transaction.
- Dashboard-facing occurrence/project queries MUST be cursor-paginated and scope-safe even before final report projections exist.
</requirements>

## Subtasks

- [ ] 4.1 Deliver schedule creation/update/cancel operations, RRULE validation, timezone behavior, and next-occurrence calculation.
- [ ] 4.2 Deliver scheduler claiming, JIT materialization, reminder planning, idempotency, and outbox publication.
- [ ] 4.3 Deliver manual and milestone occurrence creation using the shared immutable snapshot contract.
- [ ] 4.4 Deliver inspection state machine, cancellation, invalidation, responsibility state, and session-revocation events.
- [ ] 4.5 Deliver project creation and planned stage snapshots including origin-stage and report-mode configuration.
- [ ] 4.6 Deliver exceptional stage, start, skip, close, reopen, ordering, reason, and audit behavior.
- [ ] 4.7 Deliver GraphQL operations and event contracts for schedules, inspections, projects, and stage timelines.
- [ ] 4.8 Deliver scheduler replica, DST, concurrency, scale, lifecycle, and scope integration coverage.

## Implementation Details

Use operation slices under `schedules`, `inspections`, and `projects`; scheduled jobs register through the scheduler root using the same `Setup` convention as GraphQL operations. Store business time as `timestamptz` plus IANA timezone and preserve the resolved due instant. Occurrence creation writes a local immutable snapshot and outbox events; it never shares a database transaction with catalog or notification handlers.

### Relevant Files

- `services/inspection/internal/platform/{mediator,tenanttx,messaging,graphql}/` — foundations supplied by Tasks 1–2.
- `services/inspection/internal/features/{participants,templates,assets}/` — prerequisite query contracts supplied by Task 3.
- `services/inspection/cmd/inspection-scheduler/main.go` — scheduler composition root supplied by Task 1.
- `.compozy/tasks/autonomous-inspection-platform/_techspec.md` — scheduling/time and state-machine contracts.
- `.compozy/tasks/autonomous-inspection-platform/_user_stories.md` — US-011–US-014 and US-021–US-022.

### Dependent Files

- `services/inspection/internal/features/schedules/` — recurrence and scheduler-job slices.
- `services/inspection/internal/features/inspections/` — occurrence creation, state, responsibilities, and queries.
- `services/inspection/internal/features/projects/` — project and stage lifecycle slices.
- `services/inspection/internal/contracts/events/` — inspection/project event payloads and fixtures.
- `services/inspection/internal/platform/database/migrations/` — lifecycle schemas, uniqueness, indexes, and RLS.
- `services/inspection/cmd/{inspection-api,inspection-scheduler}/main.go` — API and job registration.

### Related ADRs

- [ADR-001: Use a Declarative Multi-Segment Inspection Product Model](adrs/adr-001.md) — lifecycle configuration.
- [ADR-003: Support Recurring, Milestone, Manual, and Multi-Stage Inspection Lifecycles](adrs/adr-003.md) — occurrence and stage semantics.
- [ADR-007: Apply Hierarchical Tenant and Business-Unit Access](adrs/adr-007.md) — lifecycle authorization.
- [ADR-008: Build a New Inspection Service as Vertical Slices](adrs/adr-008.md) — operation boundaries.
- [ADR-009: Isolate Tenants with PostgreSQL Row-Level Security](adrs/adr-009.md) — tenant-safe scheduler work.
- [ADR-010: Use GORM with a Dedicated Schema Migrator](adrs/adr-010.md) — lifecycle persistence.
- [ADR-011: Coordinate Slices Through a Typed In-Process Bus](adrs/adr-011.md) — prerequisite contracts.
- [ADR-013: Use Transactional Outbox, RabbitMQ, and Idempotent Consumers](adrs/adr-013.md) — materialization events.
- [ADR-017: Design for the Confirmed MVP Capacity and Latency Envelope](adrs/adr-017.md) — scheduler/history scale.

## Deliverables

- Recurring schedules and scheduler-safe due-occurrence materialization.
- Immutable manual, milestone, and scheduled inspection snapshots.
- Inspection cancellation/invalidation and external-responsibility lifecycle behavior.
- Multi-stage project/stage lifecycle with audited exceptional, skipped, closed, and reopened transitions.
- GraphQL queries/mutations and versioned lifecycle events.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there before writing tests.

- [ ] Unit: UT-022, UT-023, UT-028, UT-029, UT-050, UT-051, UT-064, UT-065
- [ ] Integration: IT-101, IT-102, IT-103, IT-104, IT-105, IT-106, IT-107, IT-108, IT-109, IT-110, IT-111, IT-112, IT-113, IT-114, IT-115, IT-116, IT-117, IT-118, IT-119, IT-120, IT-121, IT-122, IT-123, IT-124, IT-125, IT-126, IT-127, IT-128, IT-129, IT-130, IT-131, IT-132, IT-133, IT-134, IT-135, IT-136, IT-137, IT-138, IT-139, IT-140, IT-201, IT-202, IT-203, IT-204, IT-205, IT-206, IT-207, IT-208, IT-209, IT-210, IT-211, IT-212, IT-213, IT-214, IT-215, IT-216, IT-217, IT-218, IT-219, IT-220, IT-380, IT-381, IT-382, IT-413, IT-414, IT-415, IT-416, IT-417, IT-418, IT-419, IT-420, IT-421, IT-422, IT-479, IT-480, IT-481, IT-482, IT-483, IT-484, IT-485, IT-486, IT-487, IT-488, IT-489, IT-490, IT-491, IT-492, IT-493, IT-494, IT-495, IT-496, IT-497, IT-498, IT-499, IT-500, IT-501, IT-502, IT-551, IT-552, IT-553, IT-554, IT-581, IT-582

## Success Criteria

- Every assigned test case implemented and passing.
- Concurrent scheduler replicas create one occurrence/event for each due instant.
- Every occurrence and stage retains the exact template, reference, participant, policy, deadline, reminder, and report-mode snapshot it started with.
- Invalid lifecycle transitions, stale writes, and unauthorized cross-scope actions fail without partial state.
- Scheduled, milestone, manual, and multi-stage behaviors share contracts without global domain layers.
