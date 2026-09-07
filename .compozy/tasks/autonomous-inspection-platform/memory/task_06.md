# Task 06 Memory

## Current state

- Added the analysis-domain foundation: strict provider result validation, explicit terminal inconclusive fallback, and deterministic `NORMAL`/`ATTENTION`/`CRITICAL` classification with reason codes.
- Added canonical internal advisory report JSON/HTML utilities, retention clock policy logic, and provider-neutral LiteLLM/PDF ports.
- Added schema migration 11 for analysis, report, and retention base records with RLS, append-only guards, finding confidence/severity checks, and final-classification constraint.
- Added strict JSON parsing and neutral-language rejection for provider findings, plus a provider-neutral comparison consumer seam and LiteLLM/Gotenberg HTTP adapters with no public media URLs.
- Added immutable snapshot generation with pinned template/reference/profile versions and idempotent content identity, and pure deterministic dashboard, critical-alert, usage, and retention-manifest rules.
- Added local `litellm-stub` and `gotenberg-stub` Compose services with pinned WireMock images, healthchecks, deterministic success mappings, provider URLs/alias/prompt/timeout configuration, and adapter tests for malformed, unauthorized, non-PDF, and external-URL cases.
- Registered concrete request/process/classification/report-render slices, wired them into the worker and scheduler, propagated broker attempt context for bounded retries, persisted provider usage/daily summaries, projected dashboard/report-ready state, and added purge/hold/blob reconciliation paths.
- Extended GraphQL with authorized report/download/dashboard/triage/timeline/notification/retention/usage reads and retention mutations, added projection/usage/purge models plus migration 12/RLS, and added provider-neutral report/download/dashboard/notification/usage/retention helper slices.
- Added migration 13/14 for internal notification recipients, purge-only immutable-trigger overrides, and delivery-to-inspection linkage; migration 15 grants the non-privileged runtime role read access to the schema compatibility table.
- Completed report snapshot findings/evidence projections, critical-first cursor pagination, policy-aware retention scheduling, channel-status consumption, persisted critical-alert retries, and deidentified purge completion audit.
- Added local contract coverage for Task 06 analysis/report/retention event envelopes (`IT-567..IT-576`, `IT-583..IT-586`) including duplicate suppression and invalid-major rejection. The worker now rebuilds the retention clock from the authoritative inspection timestamp and tenant policy before purging, and validates `retention.purged.v1` payloads.
- Added a 148-ID `Task06AssignedCases()` manifest and GraphQL transport coverage for report/download/dashboard/triage/notification/retention/usage reads plus retention policy, deletion, and legal-hold mutations. Fixed stable invalid-input mapping, GORM session reuse between scoped mutation queries, deletion request lookup arguments, and HTML artifact presigning.

## Decisions

- Task 05 remains marked pending in its tracking file, but task 06 was explicitly assigned; its published capture and lifecycle contracts are therefore treated as the integration boundary rather than a blocker.
- Any failed or exhausted comparison is represented as terminally inconclusive and classifies at least `ATTENTION`; PDF failure must not alter an immutable snapshot or classification.

## Remaining work

- The external harness now supports runtime-RLS credentials, an isolated Task 06 tenant/catalog/capture/project fixture with a private MinIO PNG, gated GraphQL test principals, outbox/inbox/queue waits, provider reset, and optional API/worker/scheduler process lifecycle. A clean migration run reaches schema version `15`, grants the runtime role compatibility-table access, and the API `/readyz` was verified with that role. The latest unit, integration, vet, build, formatting, diff, and Compose validations pass. The tracking task remains pending until the external assigned IT-241..IT-586 contract suite is supplied and executed; the repository harness is now ready to run that suite but does not itself contain the full 148-case fixture corpus.
