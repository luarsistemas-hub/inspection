---
status: completed
title: Canonical GraphQL, Typed Operations and UI Foundation
type: chore
complexity: critical
---

# Task 4: Canonical GraphQL, Typed Operations and UI Foundation

## Overview

Finalize the canonical GraphQL surface produced by Tasks 1 through 3, wire thin mediator-backed resolvers, and regenerate the server contract before any product UI consumes it. Establish executable capability and schema-hash checks, generate result/variable types from product-owned documents while preserving separate transports, and add only the neutral accessible primitives needed by the three frontend tasks.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. `services/inspection/schema.graphqls` MUST remain the only canonical schema and MUST expose production-shaped collection, detail, history, conflict, unavailable, and recovery states from Tasks 1 through 3.
- R2. Resolver wiring MUST stay thin, derive tenant only from validated request context, dispatch to owning slices, and preserve stable userErrors, correlation, idempotency, and optimistic-version inputs.
- R3. Canonical mutations MUST carry client mutation identity where applicable; versioned mutations SHOULD require expected version, and all generated Go artifacts MUST be reproducible from gqlgen.yml.
- R4. The capability contract MUST map every root operation exactly once to owner, persona, journey or internal step, and verification evidence, rejecting missing, duplicate, undocumented, or stale rows.
- R5. A deterministic generated operation manifest MUST record the canonical schema hash and per-product operation inventory and MUST reject partial generation or any server/client hash mismatch.
- R6. Admin, Dashboard, and Capture MUST own their GraphQL documents and generate operation result and variable types with typescript-operations.
- R7. No shared GraphQL runtime or transport MUST be introduced; Admin/Dashboard bearer transports and Capture cookie plus memory-CSRF transport MUST remain distinct.
- R8. The design system MUST add neutral breadcrumbs, filter bars, semantic table/mobile-record, pagination, recovery, version-conflict, and high-impact confirmation primitives without exporting a product shell, authorization, routing, or domain logic.
- R9. Shared primitives MUST preserve native semantics, 44-pixel targets, visible 3-pixel focus, status and alert announcements, responsive reflow, and non-color-only states.
- R10. Backend generation, all three frontend codegen checks, design-system verification, and every assigned contract case MUST pass from a clean checkout.
</requirements>

## Subtasks

- [x] 4.1 Reconcile the completed backend surface with every required operation, read model, lifecycle state, and capability-matrix row.
- [x] 4.2 Complete thin resolver and composition-root wiring and remove placeholder or persistence-owning resolver behavior.
- [x] 4.3 Regenerate gqlgen server/model/resolver artifacts and establish reproducible generation checks.
- [x] 4.4 Make capability ownership, persona, journey, evidence, and US-001 edge behavior machine-checkable.
- [x] 4.5 Deliver the deterministic schema-hashed operation manifest and actionable validator diagnostics.
- [x] 4.6 Complete Admin-owned GraphQL documents and generated result/variable types.
- [x] 4.7 Complete Dashboard-owned GraphQL documents and generated result/variable types.
- [x] 4.8 Complete Capture-owned GraphQL documents and generated result/variable types without changing its trust boundary.
- [x] 4.9 Deliver neutral accessible data, recovery, conflict, and confirmation primitives for later product composition.
- [x] 4.10 Implement all assigned contract tests and run server/client generation and clean-build checks.

## Implementation Details

Follow the TechSpec “API Endpoints,” “Frontend Design,” and build-order steps 4 and 5. The manifest path and serialization are not prescribed; choose one deterministic tracked representation and keep its validator and check command authoritative. The current capability matrix lacks explicit persona/evidence fields, codegen emits schema types only, and runtime code still duplicates operation strings; fix those contract gaps without moving product-specific shell behavior into the design system.

### Relevant Files

- `services/inspection/schema.graphqls` — canonical root operations, types, inputs, and outputs.
- `services/inspection/gqlgen.yml` — Go generation source and output mapping.
- `services/inspection/internal/platform/graphql/generated.go` and `models_gen.go` — tracked generated server artifacts.
- `services/inspection/internal/platform/graphql/resolvers/schema.resolvers.go` — resolver boundary to reduce to mapping and dispatch.
- `services/inspection/internal/platform/graphql/resolvers/resolver.go`, `helpers.go`, and `task06_helpers.go` — dependency aggregation and mappings.
- `services/inspection/cmd/inspection-api/main.go` — completed slice registration and resolver composition.
- `services/inspection/internal/platform/graphql/catalog_contract_test.go`, `capture_contract_test.go`, and `lifecycle_contract_test.go` — current contract-test patterns.
- `.compozy/tasks/graphql-frontend-capability-parity/_capability_matrix.md` — parity source to reconcile with the final schema.
- `apps/admin/codegen.ts`, `apps/dashboard/codegen.ts`, and `apps/capture/codegen.ts` — add operation generation per product.
- `apps/admin/src/features/admin/operations.graphql`, `apps/dashboard/src/features/dashboard/operations.graphql`, and `apps/capture/src/graphql/documents/capture.graphql` — owned documents.
- `apps/admin/src/graphql/generated.ts`, `apps/dashboard/src/graphql/generated.ts`, and `apps/capture/src/graphql/generated.ts` — generated result and variable types.
- `apps/admin/package.json`, `apps/dashboard/package.json`, and `apps/capture/package.json` plus lockfiles — typescript-operations dependency and check scripts.
- `packages/inspection-design-system/src/index.ts` and `src/styles.css` — neutral exports and accessible shared styles.
- `packages/inspection-design-system/src/primitives/` — breadcrumbs, filters, data table, pagination, recovery, conflict, and confirmation primitives.
- `packages/inspection-design-system/tests/design-system.test.tsx` — semantic and interaction coverage.
- `.compozy/tasks/graphql-frontend-capability-parity/referencia-mercado-admin-inspection.md` — product/design reference; only neutral primitives belong here.

Recommended new contract files are `services/inspection/internal/platform/graphqlcontract/validator.go`, its tests, a tracked deterministic GraphQL operation manifest, and an integration test comparing schema, gqlgen, and all three product artifacts. Exact names may adapt to the code present after Tasks 1 through 3.

### Dependent Files

- `apps/admin/src/features/admin/admin-shell.tsx` — Task 5 replaces raw operations with generated Admin contracts.
- `apps/dashboard/src/features/dashboard/dashboard-shell.tsx` and `use-notifications.ts` — Task 6 consume generated operations.
- `apps/capture/app/capture/[linkToken]/page.tsx` and `apps/capture/src/pwa/uploads.ts` — Task 7 consume generated Capture contracts.
- `.github/workflows/ci.yml` and `scripts/verify.sh` — Task 8 promotes these checks into the seeded parity release gate.
- Task 1 through Task 3 slice setup files and application contracts — inputs to schema and resolver wiring; business logic MUST remain in their owning slices.

### Related ADRs

- [ADR-001: Full Capability Parity and Backend-First Delivery](adrs/ADR-001-full-capability-parity-and-delivery-order.md)
- [ADR-002: Admin Product and Design Direction](adrs/ADR-002-admin-product-and-design-direction.md)
- [ADR-008: Generated GraphQL Operations](adrs/adr-008-generated-graphql-operations.md)
- [ADR-010: Executable Parity Release Gate](adrs/adr-010-parity-release-gate.md)

## Deliverables

- Complete canonical schema and thin resolver mapping over every Task 1 through Task 3 application contract.
- Reproducible gqlgen artifacts, product-owned typed operations, deterministic schema-hashed manifest, and capability validator.
- Neutral accessible design-system primitives without a shared product shell or GraphQL runtime.
- Contract, generation, schema-drift, matrix, and design-system verification.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — the stable suffixes correspond one-to-one with US-001 EC-1 through EC-10.

- [x] Unit baseline: UT-001, UT-002, UT-003, UT-004 — capability ownership, unmapped diagnostics, schema-hash drift, and mutation identity.
- [x] Unit edge cases: UT-133.01, UT-133.02, UT-133.03, UT-133.04, UT-133.05, UT-133.06, UT-133.07, UT-133.08, UT-133.09, UT-133.10 — US-001 EC-1 through EC-10.
- [x] Integration baseline: IT-001 — canonical schema, gqlgen, and all product operation artifacts agree.
- [x] Integration edge cases: IT-034.01, IT-034.02, IT-034.03, IT-034.04, IT-034.05, IT-034.06, IT-034.07, IT-034.08, IT-034.09, IT-034.10 — US-001 EC-1 through EC-10 across tracked generation and contract boundaries.

## Success Criteria

- Every assigned test case implemented and passing.
- Every canonical query and mutation has exactly one machine-validated owner and evidence mapping.
- No placeholder customer/governance resolver or new resolver-local business logic remains.
- Generated Go and TypeScript artifacts reproduce from the canonical schema with matching hashes.
- All runtime product operations are sourced from owned GraphQL documents and generated types.
- Shared primitives satisfy accessibility contracts without coupling the three product shells.
