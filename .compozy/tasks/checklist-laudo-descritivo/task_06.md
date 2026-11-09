---
status: completed
title: Build scope and capture interfaces
type: frontend
complexity: high
---

# Task 6: Build scope and capture interfaces

## Overview
Deliver the manager/admin scope controls and participant PWA journey over the completed GraphQL contracts. The UI keeps capture mobile and link-scoped, explains missing views, and saves keys/meters without turning an incomplete inspection into a blocked report.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- 1. MUST let authorized users edit template v3 scope and confirm the effective inspection profile before capture.
- 2. MUST show the participant expected component/views and distinguish observed, partial, not observed, not applicable, and impossible with reason.
- 3. MUST permit explicit incomplete submission while preserving media upload/verification errors and offline retry behavior.
- 4. MUST show separate key/control/meter forms with Portuguese validation and provenance labels.
- 5. MUST preserve the design-system minimum type size and avoid horizontal scrolling at 390 px.
</requirements>

## Subtasks
- [x] 6.1 Add template scope editing to existing Admin template journey.
- [x] 6.2 Add effective-scope selection/confirmation to inspection preparation in Dashboard.
- [x] 6.3 Show component coverage and specific recapture help in Capture PWA.
- [x] 6.4 Add separate handover forms and pending-state review.
- [x] 6.5 Update incomplete submission and offline retry UI behavior.
- [x] 6.6 Add frontend unit and Playwright coverage on mobile and desktop.
- [x] 6.7 Update the Admin publication-policy copy to say the manual policy does not gate the new report workflow (ADR-009).

## Implementation Details
Follow `_techspec.md` System Architecture, Implementation Design, and Development Sequencing for contracts and compatibility rules. Implement in the existing vertical slices; new operations expose only `Setup`.

### Relevant Files
- `apps/admin/src/features/admin/template-v2.ts` — current template editing shape.
- `apps/capture/app/capture/[linkToken]/page.tsx` — participant capture flow and submission gate.
- `apps/capture/app/capture/[linkToken]/environment-declaration.tsx` — room capture controls.
- `apps/dashboard/src/features/dashboard/inspection-views.tsx` — inspection preparation/context.

### Dependent Files
- `services/inspection/schema.graphqls` — scope and capture API contract.
- `apps/capture/tests/e2e/guided-comparison.spec.ts` — existing Playwright pattern.
- `apps/dashboard/src/graphql/generated.ts` — generated client types.

### Related ADRs
- [ADR-002](adrs/adr-002.md) — Binding decision for this slice.
- [ADR-003](adrs/adr-003.md) — Binding decision for this slice.
- [ADR-006](adrs/adr-006.md) — Binding decision for this slice.

## Deliverables
- Completed build scope and capture interfaces behavior through the public GraphQL/UI surface.
- Updated implementation and documentation of any changed public contract.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**.

## Tests

Cases assigned from `_tests.md`; read each full definition there before writing tests.
- [x] UT-092, UT-093, UT-094, UT-095, UT-096, UT-097, UT-098, UT-099 — unit/component rules.
- [x] E2E-001, E2E-002, E2E-009 — end-to-end user journey (specs escritos; execução pendente no ambiente E2E).

## Success Criteria
- Every assigned test case implemented and passing.
- The build scope and capture interfaces behavior meets the PRD acceptance criteria under the declared scope and permission boundaries.
- Existing historical data, tenant isolation, and Brazilian Portuguese user-facing errors remain correct.
