---
status: completed
title: Compose and publish immutable descriptive reports
type: backend
complexity: critical
---

# Task 4: Compose and publish immutable descriptive reports

## Overview
Deliver a complete v3 current-state report from pinned scope, catalog, findings, handover records, and processing state. Automatic publication and audience-safe PDF/verification behavior must remain idempotent while v1/v2 reports still decode.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- 1. MUST build component coverage from confirmed expected scope and preserve distinct condition, review, consent, failure, and handover states.
- 2. MUST publish v3 report snapshots automatically when inputs are terminal/reportable, even with unreviewed AI items or declared gaps.
- 3. MUST create a new immutable version for changed facts, dedupe identical events, and keep PDF rendering independently retryable.
- 4. MUST project customer-safe descriptions through an explicit allowlist and keep screen/HTML/PDF facts equivalent for the same audience and version.
- 5. MUST render readable short/long PDFs with associated captions, no QR-only page, and a verification reference that exposes no private evidence.
</requirements>

## Subtasks
- [x] 4.1 Add v3 snapshot structs, readers, and safe validation while preserving old versions.
- [x] 4.2 Assemble descriptive rooms, coverage, review labels, conflicts, handover entries, and report provenance.
- [x] 4.3 Emit limited reports for failed/inconclusive analysis, including the no-job case that classification rejects today, without positive condition claims; `NOT_AUTHORIZED` has no producer because consent stays mandatory (PRD D-1).
- [x] 4.4 Create automatic publication in the classified transaction with digest/version idempotence.
- [x] 4.5 Extend customer projection, report queries, PDF renderer, and the public `verifyReport` operation with its random per-report code, rate limit and PDF QR.
- [x] 4.6 Preserve historical artifacts when corrections trigger new versions, through the explicit `reissueReport` operation and new analysis generations (PRD D-4).
- [x] 4.7 Render and inspect short, long, unavailable-media, and failure fixtures.

## Implementation Details
Follow `_techspec.md` System Architecture, Implementation Design, and Development Sequencing for contracts and compatibility rules. Implement in the existing vertical slices; new operations expose only `Setup`.

### Relevant Files
- `services/inspection/internal/features/reports/core/core.go` — canonical snapshot structs.
- `services/inspection/internal/features/reports/handle_classified/inventory.go` — cataloging report gap.
- `services/inspection/internal/features/reports/handle_classified/setup.go` — classified event and publication.
- `services/inspection/internal/features/reports/generate_snapshot/setup.go` — immutable version creation.
- `services/inspection/internal/features/reports/customer/core/inventory.go` — customer allowlist.
- `services/inspection/internal/features/reports/render_pdf/maroto.go` — PDF presentation.

### Dependent Files
- `services/inspection/internal/features/reports/publication/core/core.go` — publication policy behavior.
- `services/inspection/internal/features/reports/render_pdf/setup.go` — audience and PDF jobs.
- `services/inspection/internal/features/reports/get_report/setup.go` — version read.
- `services/inspection/schema.graphqls` — v3 report and verification fields.

### Related ADRs
- [ADR-002](adrs/adr-002.md) — Binding decision for this slice.
- [ADR-005](adrs/adr-005.md) — Binding decision for this slice.
- [ADR-008](adrs/adr-008.md) — Binding decision for this slice.
- [ADR-009](adrs/adr-009.md) — Binding decision for this slice.

## Deliverables
- Completed compose and publish immutable descriptive reports behavior through the public GraphQL/UI surface.
- Updated implementation and documentation of any changed public contract.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**.

## Tests

Cases assigned from `_tests.md`; read each full definition there before writing tests.
- [x] UT-043, UT-044, UT-045, UT-046, UT-047, UT-048, UT-049, UT-050, UT-051, UT-052, UT-053, UT-054, UT-055, UT-056, UT-057, UT-058, UT-059, UT-060, UT-061, UT-062, UT-063, UT-110, UT-113, UT-115 — unit/component rules.
- [x] IT-011, IT-012, IT-013, IT-014, IT-015, IT-016, IT-041, IT-042, IT-043, IT-044, IT-055, IT-056, IT-059, IT-060 — integrated boundary behavior.
- [x] E2E-010 — end-to-end user journey.

## Success Criteria
- Every assigned test case implemented and passing.
- The compose and publish immutable descriptive reports behavior meets the PRD acceptance criteria under the declared scope and permission boundaries.
- Existing historical data, tenant isolation, and Brazilian Portuguese user-facing errors remain correct.
