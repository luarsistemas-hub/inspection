---
status: completed
title: Complete evidence-backed move-out comparison
type: backend
complexity: high
---

# Task 5: Complete evidence-backed move-out comparison

## Overview
Deliver comparison conclusions and a v3 report that explain the approved baseline against current views. The slice distinguishes lack of current evidence from lack of baseline evidence, retains all item counts, and versions any later pairing correction.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- 1. MUST compare only included items from the pinned approved baseline and validate room/item/evidence identities.
- 2. MUST require adequate paired views for unchanged/change outcomes and sufficient room coverage for proven absence of movable items.
- 3. MUST expose distinct unchanged, changed, improved, missing, not-observed, and no-basis outcomes with stable denominators.
- 4. MUST retain readable legacy media-only reports without permitting new media-only comparison creation.
- 5. MUST issue a new report version after a changed pairing or decision rather than rewriting the prior result.
</requirements>

## Subtasks
- [x] 5.1 Apply strict pair/coverage validation to existing comparison jobs.
- [x] 5.2 Derive no-basis and not-observed outcomes without inventing baseline evidence.
- [x] 5.3 Assemble complete item outcomes and accurate filter counts in snapshot v3.
- [x] 5.4 Preserve old comparison snapshot readers and historical media-only access.
- [x] 5.5 Version revised pairings and protect previous reports.
- [x] 5.6 Verify movable-item absence, improvement, unchanged and cross-room rejection.

## Implementation Details
Follow `_techspec.md` System Architecture, Implementation Design, and Development Sequencing for contracts and compatibility rules. Implement in the existing vertical slices; new operations expose only `Setup`.

### Relevant Files
- `services/inspection/internal/features/analysis/process_inventory_job/compare.go` — item comparison normalization.
- `services/inspection/internal/features/analysis/process_inventory_job/input.go` — accepted baseline input.
- `services/inspection/internal/features/reports/handle_classified/inventory.go` — comparison report projection.
- `services/inspection/internal/features/origins/resolve_reference/setup.go` — pinned baseline and legacy reference.

### Dependent Files
- `services/inspection/internal/features/reports/core/core.go` — v3 comparison state.
- `services/inspection/internal/features/reports/customer/core/inventory.go` — customer comparison projection.
- `services/inspection/internal/features/reports/generate_snapshot/setup.go` — versioned correction.
- `services/inspection/schema.graphqls` — comparison report fields.

### Related ADRs
- [ADR-004](adrs/adr-004.md) — Binding decision for this slice.
- [ADR-005](adrs/adr-005.md) — Binding decision for this slice.
- [ADR-007](adrs/adr-007.md) — Binding decision for this slice.
- [ADR-008](adrs/adr-008.md) — Binding decision for this slice.
- [ADR-010](adrs/adr-010.md) — Binding decision for this slice.

## Deliverables
- Completed complete evidence-backed move-out comparison behavior through the public GraphQL/UI surface.
- Updated implementation and documentation of any changed public contract.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**.

## Tests

Cases assigned from `_tests.md`; read each full definition there before writing tests.
- [x] UT-071, UT-072, UT-073, UT-074, UT-075, UT-076, UT-077, UT-078, UT-079, UT-080, UT-081, UT-082, UT-083, UT-084, UT-117, UT-118 — unit/component rules.
- [x] IT-019, IT-020, IT-021, IT-022, IT-023, IT-049, IT-050, IT-051 — integrated boundary behavior.

## Success Criteria
- Every assigned test case implemented and passing.
- The complete evidence-backed move-out comparison behavior meets the PRD acceptance criteria under the declared scope and permission boundaries.
- Existing historical data, tenant isolation, and Brazilian Portuguese user-facing errors remain correct.
