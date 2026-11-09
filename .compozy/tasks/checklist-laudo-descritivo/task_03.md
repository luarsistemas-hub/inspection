---
status: completed
title: Produce descriptive and versioned analysis results
type: backend
complexity: high
---

# Task 3: Produce descriptive and versioned analysis results

## Overview
Deliver evidence-backed current-state descriptions and mode-specific finding instructions on the existing analysis jobs. This slice preserves old prompt/schema readers and the separate manager review-to-baseline boundary.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- 1. MUST apply the pinned property/furnished/selected profile independently of voice-note presence.
- 2. MUST store description, assessed portion, limitations, evidence and attribute provenance without inventing material, function, age, or ownership.
- 3. MUST separate current and comparison findings prompts while retaining old `FINDINGS` snapshots and parser compatibility.
- 4. MUST distinguish completed, failed, inconclusive, and not-authorized analysis and expose candidate rejection/truncation diagnostics.
- 5. MUST preserve accepted/edited/added-only baseline activation and prevent late model/voice results from overwriting human decisions.
- 6. MUST link catalog items to pinned components (`component_key`, null when outside scope) and route `material`/`color` through recorded attribute sources.
</requirements>

## Subtasks
- [x] 3.1 Inspect the effective persisted prompt and media-input path for a sanitized baseline case.
- [x] 3.2 Add versioned catalog schema and mode-specific findings definitions, and make `ParseTask` select its reader from the pinned version/digest (today it validates against the current code schema).
- [x] 3.3 Build catalog requests from pinned scope and labeled voice declarations.
- [x] 3.4 Normalize descriptions, evidence, provenance, and unsupported claims into inventory items.
- [x] 3.5 Persist generation diagnostics and terminal-state facts for report composition.
- [x] 3.6 Extend review mutation/view fields without changing activation semantics.
- [x] 3.7 Prepare the corpus-evaluation harness and sanitized fixtures; the evaluation itself needs the product LiteLLM path and is a release gate for activating the new schema, not part of this task's definition of done.

## Implementation Details
Follow `_techspec.md` System Architecture, Implementation Design, and Development Sequencing for contracts and compatibility rules. Implement in the existing vertical slices; new operations expose only `Setup`.

### Relevant Files
- `services/inspection/internal/features/analysis/prompt/definition.go` — task and schema registry.
- `services/inspection/internal/features/analysis/prompt/inventory.go` — catalog and compare output schema.
- `services/inspection/internal/features/analysis/build_request/setup.go` — findings request construction.
- `services/inspection/internal/features/analysis/process_inventory_job/catalog.go` — catalog scope, candidate normalization and truncation.
- `services/inspection/internal/features/inventory/decide_items/setup.go` — human item decisions.

### Dependent Files
- `services/inspection/internal/features/analysis/resolve_prompt/setup.go` — snapshot selection.
- `services/inspection/internal/features/analysis/classify_inspection/setup.go` — terminal status handoff.
- `services/inspection/internal/platform/database/models.go` — descriptive fields and generation metadata.
- `services/inspection/internal/features/inventory/activate_version/setup.go` — baseline activation guard.

### Related ADRs
- [ADR-002](adrs/adr-002.md) — Binding decision for this slice.
- [ADR-003](adrs/adr-003.md) — Binding decision for this slice.
- [ADR-005](adrs/adr-005.md) — Binding decision for this slice.
- [ADR-007](adrs/adr-007.md) — Binding decision for this slice.

## Deliverables
- Completed produce descriptive and versioned analysis results behavior through the public GraphQL/UI surface.
- Updated implementation and documentation of any changed public contract.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**.

## Tests

Cases assigned from `_tests.md`; read each full definition there before writing tests.
- [x] UT-029, UT-030, UT-031, UT-032, UT-033, UT-034, UT-035, UT-036, UT-037, UT-038, UT-039, UT-040, UT-041, UT-042, UT-085, UT-086, UT-087, UT-088, UT-089, UT-090, UT-091, UT-111, UT-112, UT-114, UT-119 — unit/component rules.
- [x] IT-008, IT-009, IT-010, IT-024, IT-039, IT-040, IT-052 — integrated boundary behavior.

## Success Criteria
- Every assigned test case implemented and passing.
- The produce descriptive and versioned analysis results behavior meets the PRD acceptance criteria under the declared scope and permission boundaries.
- Existing historical data, tenant isolation, and Brazilian Portuguese user-facing errors remain correct.
