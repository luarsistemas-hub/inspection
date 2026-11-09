---
status: completed
title: Pin template scope and guard new move-out inspections
type: backend
complexity: high
---

# Task 1: Pin template scope and guard new move-out inspections

## Overview
Deliver the immutable scope contract that makes component coverage meaningful and prevents new move-out work from using an unapproved reference. This slice owns template v3 validation, manager scope confirmation, and the shared inspection creation invariant.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- 1. MUST compile template schema v3 with stable expected room/component/view keys, asset profile, and handover definitions while reading v1/v2 unchanged.
- 2. MUST pin the effective scope and digest transactionally when an inspection is created and reject cross-tenant or stale overrides.
- 3. MUST permit a manager revision only before first capture activity and preserve the old scope for already-started capture.
- 4. MUST reject every new `FIXED_ORIGIN` (move-out) creation path (`createInspection`, `planInspection` including project stages, `createSchedule` and its materialization, onboarding completion) without a compatible approved same-asset inventory baseline; a failed materialization must surface to the manager; historical media-only reports remain readable. Eligibility follows PRD D-5; video inspections are excluded (D-2). Scope is frozen at first capture activity (D-3).
- 5. MUST use `WithContext`, tenant RLS, `libs/identity`, Portuguese `userErrors`, and the canonical migration planner.
</requirements>

## Subtasks
- [x] 1.1 Define and validate template v3 room, component, view, profile, and record fields.
- [x] 1.2 Persist and expose the effective inspection scope with optimistic revision.
- [x] 1.3 Extend manual, planned/project, scheduled and onboarding creation to pin scope and approved baseline, validating schedules at creation and at materialization.
- [x] 1.4 Preserve v1/v2 template compilation and historical report/reference reads.
- [x] 1.5 Expose scope and eligibility through the canonical GraphQL schema.
- [x] 1.6 Add tenant/RLS and creation-boundary verification.

## Implementation Details
Follow `_techspec.md` System Architecture, Implementation Design, and Development Sequencing for contracts and compatibility rules. Implement in the existing vertical slices; new operations expose only `Setup`.

### Relevant Files
- `services/inspection/internal/features/templates/catalog/compiler.go` — template validation and schema-version dispatch.
- `services/inspection/internal/features/inspections/core/core.go` — common creation transaction and pinned snapshots.
- `services/inspection/internal/features/origins/resolve_reference/setup.go` — baseline resolution.
- `services/inspection/schema.graphqls` — canonical API contract.

### Dependent Files
- `services/inspection/internal/platform/database/models.go` — scope snapshot model.
- `services/inspection/internal/platform/database/migrations/planner.go` — additive storage and RLS.
- `services/inspection/internal/features/inspections/create_manual/setup.go` — manual creation adapter.
- `services/inspection/internal/features/inspections/create_occurrence/setup.go` — scheduled/project creation adapter.
- `services/inspection/internal/features/schedules/core/service.go` — schedule creation and materialization.
- `services/inspection/internal/features/onboarding/complete/core.go` — onboarding creation path.
- `services/inspection/internal/features/templates/publish_template/setup.go` — v3 publish operation.

### Related ADRs
- [ADR-001](adrs/adr-001.md) — Binding decision for this slice.
- [ADR-003](adrs/adr-003.md) — Binding decision for this slice.
- [ADR-004](adrs/adr-004.md) — Binding decision for this slice.
- [ADR-006](adrs/adr-006.md) — Binding decision for this slice.
- [ADR-010](adrs/adr-010.md) — Binding decision for this slice.

## Deliverables
- Completed pin template scope and guard new move-out inspections behavior through the public GraphQL/UI surface.
- Updated implementation and documentation of any changed public contract.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**.

## Tests

Cases assigned from `_tests.md`; read each full definition there before writing tests.
- [x] UT-001, UT-002, UT-003, UT-004, UT-005, UT-006, UT-007, UT-008, UT-009, UT-010, UT-011, UT-012, UT-013, UT-014, UT-064, UT-065, UT-066, UT-067, UT-068, UT-069, UT-070, UT-107, UT-108 — unit/component rules.
- [x] IT-001, IT-002, IT-003, IT-017, IT-018, IT-028, IT-029, IT-030, IT-031, IT-045, IT-046, IT-047, IT-048, IT-057, IT-058 — integrated boundary behavior.

## Success Criteria
- Every assigned test case implemented and passing.
- The pin template scope and guard new move-out inspections behavior meets the PRD acceptance criteria under the declared scope and permission boundaries.
- Existing historical data, tenant isolation, and Brazilian Portuguese user-facing errors remain correct.
