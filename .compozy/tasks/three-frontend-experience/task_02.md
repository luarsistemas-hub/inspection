---
status: pending
title: Report Publication, Customer Views and Notifications
type: backend
complexity: critical
---

# Task 2: Report Publication, Customer Views and Notifications

## Overview

Deliver the backend visibility boundary that turns immutable internal results into explicitly published, customer-safe experiences. This slice owns tenant publication policy, audited publication lifecycle, customer portfolio/timeline/report/evidence projections, guarded media access, and recipient notifications over the existing outbox.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- Publication policy MUST default to manual and MUST apply the policy version effective when a report becomes final.
- Automatic mode MUST publish every final classification, including critical and inconclusive, in the finalization transaction.
- Report snapshots MUST remain immutable; visibility MUST live in a separate audited, versioned publication ledger.
- A published report MUST transition only to superseded or invalidated; correction MUST create and publish a new snapshot.
- Customer GraphQL models MUST be allowlisted and MUST NOT expose internal report JSON/HTML, object keys, reasoning, notes, or unpublished results.
- Simple evidence MUST show approved current evidence; advanced evidence MUST add replaced/discarded lineage while blocked visuals return metadata only.
- Media URLs MUST be short-lived, read-only, and minted only after current entitlement, scope, publication, and sensitivity checks.
- Customer portfolio and timeline reads MUST be cursor-paginated, stable, hierarchy-scoped, and safe under late event arrival.
- In-app notifications MUST be per-recipient, deduplicated by business event, cursor-paginated, and immediately subject to current access.
- Existing external notification delivery MUST continue through the transactional outbox and verified configured channels without rolling back publication on provider failure.
- Final snapshots MUST capture the effective policy version even in manual mode, and publication mutations MUST persist a tenant-unique client mutation key.
- Recipient channel storage MUST be membership-owned, verified, selected, versioned, and separate from internal alert recipients and Capture participant contacts.
- Dashboard users MUST be able to update their own verified channel preferences without disabling in-app notifications or changing prior deliveries.
- Customer report queries MUST support current and historically published versions; invalidated history may expose audit-safe metadata but no content.
- Inconclusive analysis MUST retain the existing `ATTENTION` classification with reason code `INCONCLUSIVE` and follow the same publication rule as every final report.
- The backend MUST close minimal Admin governance/catalog read gaps required by the PRD, including analysis-profile listing, actor/filter audit data, customer channel administration, and deletion/legal-hold status.
</requirements>

## Subtasks

- [ ] 2.1 Add publication-policy, publication-ledger, recipient-notification, constraint, index, and RLS migrations/models.
- [ ] 2.2 Deliver configure, publish, invalidate, supersede, and policy-at-finalization report behavior.
- [ ] 2.3 Deliver customer-safe portfolio and chronological asset/project timeline query slices.
- [ ] 2.4 Deliver allowlisted customer report/evidence mapping and guarded media URL issuance.
- [ ] 2.5 Deliver recipient notification fan-out, listing, unread count, and idempotent read state.
- [ ] 2.6 Integrate report/progress events with the existing outbox and external notification dispatcher.
- [ ] 2.7 Evolve GraphQL types/resolvers and composition roots for publication, customer, and notification contracts.
- [ ] 2.8 Cover races, idempotency, state transitions, leakage prevention, RLS, paging, and provider failures.
- [ ] 2.9 Deliver verified recipient-channel preference and historically published report GraphQL contracts.
- [ ] 2.10 Complete the minimal governance/catalog query fields required by Admin without moving business rules into resolvers.
## Implementation Details

Follow the TechSpec sections “Report Publication and Notification State,” “Data Models,” and “GraphQL API Surface.” Dedicated customer response types are a server security boundary; never reuse the existing internal `Report` GraphQL type or authorize media only when a page first loads.

### Relevant Files

- `services/inspection/internal/features/reports/generate_snapshot/setup.go` — immutable report finalization transaction.
- `services/inspection/internal/features/reports/get_report/setup.go` and `download_pdf/setup.go` — internal report and artifact access patterns.
- `services/inspection/internal/features/reports/core/` — report domain behavior.
- `services/inspection/internal/features/dashboard/summary/`, `list_triage/`, and `project_timeline/` — existing projection/query patterns.
- `services/inspection/internal/features/notifications/` — current intent, delivery, callback, and listing slices.
- `services/inspection/internal/features/messaging/dispatch_outbox/setup.go` and `consume_events/setup.go` — durable event boundaries.
- `services/inspection/internal/platform/objectstore/` — presigned object access.
- `services/inspection/internal/contracts/events/events.go` and `testdata/registry.json` — versioned publication/progress events without protected payloads.
- `services/inspection/internal/platform/database/models.go` and `migrations/planner.go` — report/dashboard/notification persistence and RLS.
- `services/inspection/schema.graphqls` and `internal/platform/graphql/resolvers/` — canonical customer-safe API boundary.
- `services/inspection/cmd/inspection-worker/main.go` — fan-out/projection consumer composition.

### Dependent Files

- `services/inspection/internal/platform/graphql/generated.go` and `models_gen.go` — regenerated after schema evolution.
- `services/inspection/cmd/inspection-api/main.go` — query/mutation setup and media URL dependencies.
- `services/inspection/internal/features/analysis/classify_inspection/setup.go` — terminal classification/report trigger.
- `apps/admin/src/graphql/generated.ts` — publication policy consumer.
- `apps/dashboard/src/graphql/generated.ts` — customer/report/notification consumer.
- `scripts/load-smoke.sh` and `scripts/security-smoke.sh` — final scale/leakage probes.

### Related ADRs

- [ADR-003: Tenant-Controlled Report Publication](adrs/adr-003.md) — effective publication policy.
- [ADR-004: Immutable Capture Is Separate from Result Review](adrs/adr-004.md) — evidence finality and result boundary.
- [ADR-008: Publish Reports Through an Audited Visibility Ledger and Customer-Safe Projections](adrs/adr-008.md) — persistence, projection, and notification design.

## Deliverables

- Tenant policy and audited publication state integrated with immutable report finalization.
- Dedicated customer portfolio, timeline, report, evidence, and authorized-media GraphQL contracts.
- Idempotent in-app notifications and existing external-channel fan-out.
- Leakage-negative, concurrency, ordering, RLS, projection, and GraphQL integration coverage.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; read each full definition before implementation.

- [ ] Unit: UT-024, UT-025, UT-026, UT-027, UT-028, UT-029, UT-030, UT-031, UT-032, UT-033, UT-034, UT-035, UT-036, UT-037, UT-038, UT-039, UT-040, UT-041, UT-042, UT-043, UT-044, UT-045, UT-046, UT-047 — publication, safe evidence/media, and recipient notification components.
- [ ] Integration: IT-186, IT-187, IT-188, IT-189, IT-190, IT-191, IT-192, IT-193, IT-194, IT-195, IT-196, IT-206, IT-207, IT-208, IT-209, IT-210, IT-211, IT-212, IT-213, IT-214, IT-215, IT-216, IT-217, IT-218, IT-220, IT-226, IT-227, IT-228, IT-229 — publication/customer GraphQL, safe-schema selection, finalization, notification fan-out, media authorization, and new-table RLS.

## Success Criteria

- Every assigned test case implemented and passing.
- Manual and automatic publication produce deterministic, audited outcomes under concurrency.
- Customer queries can never select or serialize internal-only report/evidence fields.
- Revoked access or invalidated publication prevents the next media URL from being issued.
- Notification replay creates no duplicate in-app or external business notice.
