---
status: completed
title: Record component coverage and handover data during capture
type: backend
complexity: high
---

# Task 2: Record component coverage and handover data during capture

## Overview
Deliver the capture behavior that records component-level gaps and required key/meter declarations without discarding incomplete inspections. The existing media verification gate remains authoritative, while a confirmed incomplete v3 submission becomes reportable.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- 1. MUST store participant component answers and typed handover declarations against the pinned scope and reject foreign evidence/record keys.
- 2. MUST require reasons for `NOT_APPLICABLE` and `IMPOSSIBLE`, and preserve missing components in the coverage denominator.
- 3. MUST allow incomplete v3 submission only with explicit confirmation, while blocking pending or failed attached media.
- 4. MUST retain idempotent upload/answer retries, capture-source and timestamp provenance, and link-scoped authorization.
- 5. MUST include new tenant tables in RLS and keep all user-facing validation in Portuguese.
- 6. MUST expose a per-inspection coverage summary that the Dashboard can read before a report exists, and derive recapture suggestions deterministically from the pinned component views.
</requirements>

## Subtasks
- [x] 2.1 Add component answer and handover-record slices with revision checks.
- [x] 2.2 Expose current scope, answers, and records through Capture GraphQL.
- [x] 2.3 Change v3 completeness evaluation and submission to retain room/component gaps.
- [x] 2.4 Keep media receipt and verification blocking behavior distinct from absent evidence.
- [x] 2.5 Add `received_at` and a capture-time source (`DEVICE`, `EXIF`, `SERVER_FALLBACK`), read EXIF time before normalization without keeping GPS, and flag gallery/file timestamp conflicts.
- [x] 2.6 Verify tenant isolation and retries across new capture storage.
- [x] 2.7 Derive recapture suggestions from pinned views, reuse `declare_impossibility` for room-level impossibility, and expose the coverage summary.

## Implementation Details
Follow `_techspec.md` System Architecture, Implementation Design, and Development Sequencing for contracts and compatibility rules. Implement in the existing vertical slices; new operations expose only `Setup`.

### Relevant Files
- `services/inspection/internal/features/capture/core/core.go` — current completeness calculation.
- `services/inspection/internal/features/capture/core/service.go` — submission transaction.
- `services/inspection/internal/features/capture/submit/setup.go` — submit GraphQL edge.
- `services/inspection/internal/features/capture/manage_environment/setup.go` — environment state pattern.

### Dependent Files
- `services/inspection/internal/platform/database/models.go` — component/handover persistence.
- `services/inspection/internal/platform/database/migrations/planner.go` — tenant RLS migration.
- `services/inspection/schema.graphqls` — Capture answer and record types.
- `services/inspection/internal/features/capture/save_metadata/setup.go` — timestamp provenance.
- `services/inspection/internal/features/capture/declare_impossibility/setup.go` — existing room-level impossibility.
- `services/inspection/internal/features/media/normalize/normalize.go` — strips EXIF today.

### Related ADRs
- [ADR-002](adrs/adr-002.md) — Binding decision for this slice.
- [ADR-003](adrs/adr-003.md) — Binding decision for this slice.
- [ADR-005](adrs/adr-005.md) — Binding decision for this slice.
- [ADR-006](adrs/adr-006.md) — Binding decision for this slice.

## Deliverables
- Completed record component coverage and handover data during capture behavior through the public GraphQL/UI surface.
- Updated implementation and documentation of any changed public contract.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**.

## Tests

Cases assigned from `_tests.md`; read each full definition there before writing tests.
- [x] UT-015, UT-016, UT-017, UT-018, UT-019, UT-020, UT-021, UT-022, UT-023, UT-024, UT-025, UT-026, UT-027, UT-028, UT-109 — unit/component rules.
- [x] IT-004, IT-005, IT-006, IT-007, IT-025, IT-026, IT-032, IT-033, IT-034, IT-035, IT-036, IT-037, IT-038, IT-053, IT-054 — integrated boundary behavior.

## Success Criteria
- Every assigned test case implemented and passing.
- The record component coverage and handover data during capture behavior meets the PRD acceptance criteria under the declared scope and permission boundaries.
- Existing historical data, tenant isolation, and Brazilian Portuguese user-facing errors remain correct.
