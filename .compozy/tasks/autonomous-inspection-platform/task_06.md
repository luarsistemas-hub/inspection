---
status: pending
title: "Analysis, Reports, Dashboard & Retention"
type: backend
complexity: critical
---

# Task 6: Analysis, Reports, Dashboard & Retention

## Overview

Deliver the terminal processing pipeline that converts immutable evidence into neutral structured findings, deterministic classification, internal reports, portfolio projections, critical alerts, usage records, and retention outcomes. The task guarantees a useful immutable report even when analysis or PDF generation permanently fails.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- LiteLLM integration MUST use logical model aliases, versioned prompts, strict JSON Schema, authorized normalized images, and persisted actual model/usage/cost/latency metadata.
- Invalid structured output MUST follow bounded retry; permanent failure or insufficient evidence MUST become an explicit terminal inconclusive comparison.
- Findings MUST reference evidence, use neutral observed-change language, record severity/confidence/quality/action, and MUST NOT assign fault, cost, liability, or automatic consequence.
- A deterministic Go classifier MUST produce exactly `NORMAL`, `ATTENTION`, or `CRITICAL`; any missing, skipped, flagged, uncorrected, or inconclusive item MUST force at least `ATTENTION`.
- Every terminal inspection MUST create immutable canonical JSON and HTML report snapshots; Gotenberg MUST create private PDFs or an HTML-only fallback without changing classification.
- Consolidated mode MUST append a new immutable project report version after each terminal stage; historical mode MUST retain one immutable report per stage and both MUST preserve timeline context.
- Reports, findings, classifications, and evidence views MUST remain internal, advisory, tenant/scope authorized, and unavailable to external sessions.
- Dashboard projections MUST be rebuildable, ordered, cursor-paginated, scope-safe, and exclude invalidated inspections from valid indicators.
- A new critical finding MUST alert selected internal recipients once through all selected channels, while external participants are never automatic recipients.
- Retention MUST enforce configured/default five-year, one-year, and security-expiry classes, refuse premature deletion, honor legal hold, purge all derivatives/artifacts, and retain only deidentified audit.
- Property, construction, and cleaning terminal outputs MUST share the same analysis/report pipeline and retain segment-specific context.
</requirements>

## Subtasks

- [ ] 6.1 Deliver comparison-job creation, terminality coordination, LiteLLM adapter, structured validation, retries, and inconclusive fallback.
- [ ] 6.2 Deliver immutable findings, evidence references, provider usage, cost, latency, and reproducibility metadata.
- [ ] 6.3 Deliver deterministic classification and reason-code behavior for every evidence/flag/failure combination.
- [ ] 6.4 Deliver canonical report snapshots, internal HTML views, Gotenberg PDF artifacts, HTML-only fallback, and authorized download.
- [ ] 6.5 Deliver consolidated and historical project report versioning and timeline behavior.
- [ ] 6.6 Deliver ordered dashboard/triage projections, filters, counters, rebuild, and invalidation semantics.
- [ ] 6.7 Deliver first-critical transition detection, selected-recipient alerts, delivery visibility, and no external disclosure.
- [ ] 6.8 Deliver retention policy, clocks, requests, refusal, legal hold, scheduled purge, deletion manifest, and reconciliation.
- [ ] 6.9 Deliver usage summaries, operational replay/reconciliation, metrics, and runbooks for terminal processing.
- [ ] 6.10 Deliver end-state integration coverage for analysis, reports, projections, notifications, privacy, and all three segments.

## Implementation Details

Implement comparison, classification, report, projection, alert, and purge consumers as their own slices with inbox-backed local transactions. Store only immutable analysis/report versions; dashboard tables are disposable projections. Gotenberg and LiteLLM adapters stay provider-neutral behind the TechSpec ports, and report rendering must not embed durable storage credentials or public URLs.

### Relevant Files

- `services/inspection/internal/features/{capture,media,recapture}/` — immutable input and terminal events from Task 5.
- `services/inspection/internal/features/{inspections,projects}/` — lifecycle and report-mode snapshots from Task 4.
- `services/inspection/internal/platform/{messaging,objectstore,notifications,tenanttx}/` — async/provider foundations from Tasks 1–2.
- `services/inspection/internal/contracts/events/` — capture, analysis, report, notification, and retention contracts.
- `.compozy/tasks/autonomous-inspection-platform/_user_stories.md` — US-025–US-031 and US-033–US-034.

### Dependent Files

- `services/inspection/internal/features/analysis/` — comparison, validation, findings, and classification slices.
- `services/inspection/internal/features/reports/` — snapshot, HTML, PDF, download, and project report slices.
- `services/inspection/internal/features/dashboard/` — projection consumers and queries.
- `services/inspection/internal/features/{notifications,usage,retention}/` — alerts, metering, policies, purge, and reconciliation.
- `services/inspection/internal/platform/{llm,pdf}/` — LiteLLM and Gotenberg adapters.
- `services/inspection/internal/platform/database/migrations/` — analysis/report/projection/usage/retention schemas and RLS.
- `services/inspection/cmd/{inspection-api,inspection-worker,inspection-scheduler}/main.go` — query, consumer, and purge scheduling registration.
- `deploy/docker-compose.yml` — pinned LiteLLM and Gotenberg services.

### Related ADRs

- [ADR-001: Use a Declarative Multi-Segment Inspection Product Model](adrs/adr-001.md) — shared analysis pipeline.
- [ADR-002: Treat Evidence Metadata as Risk Signals, Not Proof of Authenticity](adrs/adr-002.md) — neutral flags.
- [ADR-004: Keep AI Reports Internal, Immutable, and Advisory](adrs/adr-004.md) — findings and visibility.
- [ADR-006: Enforce Configurable Retention with Fixed Default Periods](adrs/adr-006.md) — privacy lifecycle.
- [ADR-008: Build a New Inspection Service as Vertical Slices](adrs/adr-008.md) — consumer/query slices.
- [ADR-009: Isolate Tenants with PostgreSQL Row-Level Security](adrs/adr-009.md) — report/projection isolation.
- [ADR-013: Use Transactional Outbox, RabbitMQ, and Idempotent Consumers](adrs/adr-013.md) — processing/replay.
- [ADR-014: Keep Media Private and Detect Sensitive Content Locally](adrs/adr-014.md) — authorized analysis inputs.
- [ADR-016: Render Immutable PDFs Through Gotenberg](adrs/adr-016.md) — PDF rendering and fallback.
- [ADR-017: Design for the Confirmed MVP Capacity and Latency Envelope](adrs/adr-017.md) — history/dashboard performance.

## Deliverables

- Provider-neutral structured analysis with deterministic classification and explicit inconclusive outcomes.
- Immutable internal JSON/HTML/PDF or HTML-only reports with consolidated/history stage modes.
- Scope-safe dashboard/triage projections, filters, counts, and critical alerting.
- Actual provider usage visibility and idempotent replay/reconciliation operations.
- Configurable retention, refusal, legal hold, complete purge, and deidentified audit behavior.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there before writing tests.

- [ ] Unit: UT-011, UT-012, UT-015, UT-016, UT-034, UT-035, UT-036, UT-037, UT-044, UT-045, UT-048, UT-049, UT-066, UT-067
- [ ] Integration: IT-241, IT-242, IT-243, IT-244, IT-245, IT-246, IT-247, IT-248, IT-249, IT-250, IT-251, IT-252, IT-253, IT-254, IT-255, IT-256, IT-257, IT-258, IT-259, IT-260, IT-261, IT-262, IT-263, IT-264, IT-265, IT-266, IT-267, IT-268, IT-269, IT-270, IT-271, IT-272, IT-273, IT-274, IT-275, IT-276, IT-277, IT-278, IT-279, IT-280, IT-281, IT-282, IT-283, IT-284, IT-285, IT-286, IT-287, IT-288, IT-289, IT-290, IT-291, IT-292, IT-293, IT-294, IT-295, IT-296, IT-297, IT-298, IT-299, IT-300, IT-301, IT-302, IT-303, IT-304, IT-305, IT-306, IT-307, IT-308, IT-309, IT-310, IT-321, IT-322, IT-323, IT-324, IT-325, IT-326, IT-327, IT-328, IT-329, IT-330, IT-331, IT-332, IT-333, IT-334, IT-335, IT-336, IT-337, IT-338, IT-339, IT-340, IT-371, IT-372, IT-373, IT-374, IT-375, IT-390, IT-423, IT-424, IT-425, IT-426, IT-427, IT-428, IT-429, IT-430, IT-433, IT-434, IT-435, IT-436, IT-437, IT-438, IT-527, IT-528, IT-529, IT-530, IT-531, IT-532, IT-533, IT-534, IT-561, IT-562, IT-567, IT-568, IT-569, IT-570, IT-571, IT-572, IT-573, IT-574, IT-575, IT-576, IT-583, IT-584, IT-585, IT-586

## Success Criteria

- Every assigned test case implemented and passing.
- Every terminal inspection exposes one immutable internal report even after permanent AI or PDF failure.
- Classification is reproducible from the pinned profile and never delegates final state to prompt prose.
- Dashboard, reports, downloads, alerts, and purge remain tenant/scope safe and externally inaccessible.
- Retention reconciliation leaves no eligible original, part, derivative, PDF, or personal association behind.
