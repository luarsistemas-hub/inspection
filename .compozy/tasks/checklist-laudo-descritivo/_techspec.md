# TechSpec: Descriptive Checklist, Report, and Move-Out Comparison

## Executive Summary

Extend the existing environment-based vertical slices and immutable report pipeline. Template schema v3 describes expected components, useful views, asset profile, and supplemental records; inspection creation pins a validated effective scope that can be explicitly revised only before first capture activity. Capture records component coverage and handover declarations. Catalog analysis becomes descriptive under a versioned output schema, while current and comparative findings use separate pinned prompt tasks. A new report snapshot schema carries the complete checklist/comparison facts and feeds explicit internal/customer projections for GraphQL, Dashboard, HTML, and Maroto PDF.

The principal trade-off is compatibility work: old template versions, prompt snapshots, report snapshots, and legacy comparisons must remain readable while new move-out creation requires an approved inventory baseline. The user chose automatic issue for the new report workflow, so schema-v3 reports publish from the classified event even when AI observations remain unreviewed or coverage/required records are incomplete. A terminal AI failure or absent consent produces a limited report; a pending/failed media receipt still cannot be passed off as usable evidence.

## Verified Current State

Checked against the code on 2026-11-03. Items marked *new work* are assumed by earlier drafts of this spec but do not exist yet.

- Template schema accepts only v1 and v2 (`templates/catalog/compiler.go`). Nothing for v3, profile, expected rooms/components or handover records exists. Latest migration at review: 116.
- `capture/core.Evaluate` blocks an expected `env:` requirement even with `confirmIncomplete=true`, unless the participant gave an impossibility reason. Media not `READY` blocks submission in `capture/core/service.go`.
- `ParseTask` validates the pinned definition against the *current* fixed schema in `analysis/prompt/definition.go`, so a schema change would break old snapshots. Selecting a reader by pinned version/digest is *new work*.
- Catalog output is capped at 80 candidates per environment (`maxItemsPerEnvironment`). `origins.inventory_items` has no `description` column; `material` and `color` exist and are AI-filled.
- Report snapshots are v1 and v2; publication policy defaults to manual; automatic publication happens in the classified transaction.
- AI consent is mandatory at invitation acceptance and submission, and classification returns a permanent error when there are no jobs. A report without AI consent or jobs is *new work* only for the `FAILED`/`INCONCLUSIVE` case; PRD D-1 keeps consent mandatory.
- There is no "move-out" concept: it is an inspection with comparison mode `FIXED_ORIGIN`. Creation paths: `createInspection`, `planInspection`, `createSchedule` (materialized by the scheduler), onboarding completion; all call `inspections/core.Service.Create`. The reference resolver accepts `ACTIVE` and `SUPERSEDED` versions and media-only origins.
- No `verifyReport`, verification code or QR code exists. `media.media_objects.captured_at` has no source marker and no `received_at`; EXIF is stripped during normalization.
- Already present and reusable: `capture/declare_impossibility` (with reason code), the `recapture/` domain, `inventory.item_attribute_sources`, `counterparty/` baseline acknowledgment/contestation.

## System Architecture

### Component Overview

| Component | Responsibility | Stories |
| --- | --- | --- |
| `templates/catalog` and template publish/resolve slices | Validate v3 component/view/handover definitions while preserving v1/v2 | US-001, US-002 |
| Inspection scope slice and `inspections/core` | Validate manager override, pin scope/digest in creation transaction, gate new move-out reference | US-002, US-010 |
| Capture component/handover slices and `capture/core` | Record participant answers, allow explicitly incomplete v3 submission, preserve media checks | US-003, US-004 |
| Catalog and findings analysis slices | Use profile independent of voice, pin schema/prompt/model revision, describe supported attributes, distinguish terminal outcomes | US-005, US-013 |
| Inventory review/activation slices | Retain item decision and immutable baseline rules; expose new descriptive fields | US-006 |
| Report generation/classification/publication slices | Compose v3 snapshot, condition/coverage/conflict rules, automatic publication, revision idempotence | US-007, US-009 |
| Customer projection and PDF renderer | Audience allowlist, report-safe evidence metadata, descriptive grouping, comparison appendix, short/long pagination | US-008, US-012 |
| Dashboard and Capture PWA | Scope setup, participant coverage, manager review, report and comparison views | US-001–US-012 |
| Reference resolver and inventory comparison | Approved-baseline eligibility, pinned version, evidence pairing and explicit states | US-010–US-012 |
| Schedules, project planning and onboarding creation | Same baseline guard at schedule creation, materialization and `planInspection`/onboarding | US-010 |
| Media and recapture | Capture-time provenance; deterministic recapture suggestions from the pinned scope | US-003 |

Data flow: published template v3 → manager override/default resolution → immutable inspection scope and capture draft → participant component answers, handover records, and media → catalog/findings/compare generations → classification with terminal-state facts → immutable report snapshot v3 → audience projection → screen/HTML/PDF and stable verification metadata. Review decisions may activate a separate immutable baseline. Later corrections/reanalysis create new report versions; they never mutate issued JSON or PDF.

### Architectural boundary

Each new operation is a sibling slice under `services/inspection/internal/features/{domain}/{operation}` with one exported `Setup` function; the composition root wires dependencies and the mediator. Domain validation stays with the operation/core, not GraphQL resolvers. New persistence uses tenant-scoped GORM calls with `WithContext`, additive migrations registered in `internal/platform/database/migrations/planner.go`, and existing `libs/identity` UUID helpers. No service imports another service's `internal` package. Frontends retain their existing feature and Playwright structure.

## Implementation Design

### Core Interfaces

The following Go contracts illustrate the boundaries; fields may be placed in the existing packages named in the component table.

```go
type EffectiveScope struct {
    Profile string
    TemplateVersionID identity.ID
    Rooms []ExpectedRoom
    Records []RequiredRecord
    Digest string
}
func ResolveScope(template catalog.TemplateDocument, override *ScopeOverride) (EffectiveScope, error)
func (s Service) ConfirmScope(ctx context.Context, inspectionID identity.ID, override ScopeOverride, expectedVersion int) (EffectiveScope, error)
```

```go
type ComponentAnswer struct {
    RequirementKey string // capture requirement key, e.g. env:<identity>
    ComponentKey string
    ViewKey string // optional; empty answers the component as a whole
    State string
    Reason string
    EvidenceIDs []identity.ID
    Version int
}
func EvaluateCoverage(scope EffectiveScope, answers []ComponentAnswer, evidence []EvidenceState) (Coverage, error)
```

```go
type ReportableState struct {
    AnalysisStatus string
    CatalogStatus string
    MediaReady bool
    ConsentGranted bool
}
func BuildChecklist(scope EffectiveScope, catalog CatalogGeneration, findings []Finding, state ReportableState) (core.ChecklistSection, error)
func ResolveAudience(snapshot core.Snapshot, audience string) (core.Snapshot, error)
```

`ResolveScope` rejects duplicate/foreign keys, unknown profiles, and invalid view requirements. `ConfirmScope` uses an optimistic inspection/draft version and refuses any change after first consent, answer, upload, or submission; scheduled inspections use the template default unless explicitly revised before capture. `EvaluateCoverage` never divides by zero and never treats an unconfirmed AI detection as expected scope. `BuildChecklist` fails closed on foreign evidence references and preserves a terminal failure/consent state rather than making a positive summary. All errors exposed through GraphQL become stable Portuguese `userErrors`.

### Data Models

| Model | New or changed fields | Invariant |
| --- | --- | --- |
| `TemplateDocument` schema v3 | `assetProfile`, `expectedRooms[{key,typeCode,name,components[{key,typeCode,label,views[]}]}]`, `handoverRecords[{key,kind,required}]` | Unique stable keys, valid vocabulary, bounded by current template/media limits; v1/v2 compile as before. |
| `inspections.scope_snapshots` (new) | `id`, `tenant_id`, `inspection_id` unique, `template_version_id`, `profile`, `document` JSONB, `digest`, `version`, `confirmed_by`, timestamps | One effective scope per inspection, updated only before first capture activity; report pins its digest/document revision. |
| `capture.component_answers` (new) | tenant/draft/requirement key (`env:<identity>`)/component key/optional view key, state, reason, evidence IDs JSONB, version, actor, updated time | Keys must exist in pinned scope; `NOT_APPLICABLE` and `IMPOSSIBLE` require a reason; current row uses optimistic update and audit event. |
| `capture.handover_records` (new) | tenant/draft/record key, kind, typed payload JSONB, media IDs, declarant, version, updated time | Kinds are key/control/meter/note; required absence remains a pending projection; no OCR claim without confirmation. |
| `origins.inventory_items` | `description`, `assessed_scope`, `not_assessed` JSONB, nullable `component_key` (link to the pinned expected component; null means observed outside scope); existing `condition`, `observation`, `limitation`, review/voice provenance retained | AI description and attributes cite received evidence or declaration; old rows decode with empty optional fields. Existing `material`/`color` values must record a source in `inventory.item_attribute_sources` or stay empty. |
| `analysis.inventory_jobs` and prompt snapshot metadata | Pinned schema version/digest, effective model revision or `UNKNOWN`, media transform/input digest, candidate/drop/truncation counts | Reanalysis uses a new generation; old task/schema readers stay available. |
| `reports.report_snapshots` canonical JSON v3 | `checklist`, `processing`, `scope`, `handover`, complete `comparison`, `provenance`, report-safe verification code | Immutable per version; no signed URLs, image bytes, raw transcripts, or storage keys. |
| Media capture metadata | New `received_at`; new `captured_at_source` (`DEVICE`, `EXIF`, `SERVER_FALLBACK`); optional EXIF capture time read before normalization strips metadata, never GPS or device identifiers | Today a missing client time is silently replaced by the server clock; that must become `SERVER_FALLBACK`. Date conflict is a review flag, never a fraud conclusion. |

`ChecklistSection` contains ordered rooms, each with expected component status and observed item entries. An entry contains stable item identity, visual description, assessed extent, condition, review status, evidence IDs, limitations, and attribute-source summary. Out-of-scope entries have no condition or baseline membership. `Coverage` has `expected`, `fullyAssessed`, `partiallyAssessed`, `notObserved`, `notApplicable`, `impossible`, `unknownScope`; its denominator is `expected - justifiedNotApplicable`. No percentage exists when that denominator is zero. `Processing` distinguishes `COMPLETED`, `FAILED`, `INCONCLUSIVE`, and `NOT_AUTHORIZED`. `Handover` holds required/completed/pending records. `Comparison` retains the baseline reference and every included item outcome, including unchanged, improved, missing, not observed, and no basis. Current `InventorySection` remains the v2 decoder.

Use additive schema changes and RLS for each new tenant table. Preserve existing table/column names. The migration catalog derives its latest version from `LatestVersion()` (latest at review: 116). Backfill is not required for historical report JSON; v1/v2 readers render the issued facts. Existing v2 templates receive no fabricated expected components.

### API Endpoints

The API is GraphQL (`services/inspection/schema.graphqls`); new fields use lower camel case. All mutation inputs retain `clientMutationId` and return `userErrors`.

| Operation | Request / response | Success and failure shapes |
| --- | --- | --- |
| Existing `publishTemplateVersion` | Definition JSON accepts schema v3 scope/records | Returns a version; invalid keys, duplicate components, unsupported profile, or size limit yield field-specific `userErrors`. |
| Existing `createInspection` plus `scopeOverride` | Optional structured override and existing reference fields | Returns pinned scope digest/version; move-out without approved same-asset baseline yields `userErrors` and no inspection. Duplicate `clientMutationId`/source key returns the existing inspection. |
| New `confirmInspectionScope` | `inspectionId`, override, `expectedVersion`, `clientMutationId` | Returns effective scope; stale version returns conflict; started capture returns invalid state. |
| Existing `externalCapture` plus scope/answers/records | Query with external session | Returns only the linked inspection's effective requirements and current values; expired link yields safe authorization error. |
| New `recordComponentAnswer` | `requirementKey`, `componentKey`, optional `viewKey`, `state`, `reason`, `evidenceIds`, `expectedVersion` | Returns saved answer; unknown key/evidence, missing reason, stale version, or closed draft yield `userErrors`. |
| New `upsertHandoverRecord` | `recordKey`, typed values, media IDs, `expectedVersion` | Returns saved declaration; invalid reading/quantity or foreign media yields `userErrors`. |
| Existing `submitCapture` | `confirmIncomplete=true` for incomplete v3 scope | Returns submitted version with gaps; unconfirmed incomplete or media still failed/processing returns safe `userErrors`. |
| Existing `inventoryDraft`/review mutations | Descriptive fields added to `InventoryItem` | Returns item/review state; foreign evidence or stale item version yields `userErrors`; activation still excludes pending/rejected items. |
| New `reissueReport` | `inspectionId`, `expectedVersion`, `clientMutationId` | Returns the new version, or the current one when canonical facts are unchanged; stale version returns a conflict `userError`; caller without staff permission is refused. |
| Existing `report`, `customerReport`, `reportDownload` | Typed `checklist`, `processing`, `comparison`, `scope`, `handover` projections | Returns requested pinned version; unauthorized data/media or unknown version is absent/safe error; PDF job status remains retryable. |
| New public `verifyReport(code, version)` | Opaque report code and integer version | Returns report number, version, digest, status and issued date only; unknown code returns no private existence details. |

### Move-out creation guard

Every creation path with `FIXED_ORIGIN` converges on `inspections/core.Service.Create`; the guard lives there and is verified from each adapter: `createInspection`, `planInspection` (including project stages), `createSchedule` (validated when the schedule is created and again when `materialize_due` creates the occurrence) and onboarding completion. A materialization that fails the guard is recorded and surfaced to the manager as an attention item, never dropped silently. Schedules created before rollout follow the same rule. Version eligibility follows PRD D-5; video inspections are excluded (D-2).

### Verification code and QR

`verifyReport` is public and unauthenticated. The code is a random URL-safe token of at least 128 bits, generated once per report, stored with the report, compared in constant time and never logged. The operation is rate limited per client, returns an identical empty shape for an unknown code or version, and is listed in the public-operation manifest. The PDF QR encodes the verification URL; `boombuler/barcode` is already an indirect dependency.

### Recapture suggestions

A suggestion for a `PARTIAL` or `NOT_OBSERVED` component is derived deterministically from the pinned component's `views[]` and the participant's answers: it names the room, component and missing view, states no condition, and never blocks submission. Reuse `capture/declare_impossibility` for room-level impossibility; it does not need a second path. The `recapture/` domain keeps handling post-submission replacement requests.

Extend `AnalysisPromptTask` for `FINDINGS_CURRENT` and `FINDINGS_COMPARE`; the existing `analysisPrompt`/`updateAnalysisPrompt` operations remain platform-scoped. Regenerate gqlgen, operation manifest, and `src/graphql/generated.ts` with `scripts/generate-graphql-contracts.sh`. Update query documents in Admin, Dashboard, and Capture where the fields are consumed.

### Analysis contracts and compatibility

`FINDINGS_CURRENT` and `FINDINGS_COMPARE` share the current findings output shape but have separate instructions and snapshots. `FINDINGS` remains available for old inspections. `INVENTORY_CATALOG` v2 adds supported `description`, `assessedScope`, and `notAssessed`; expected-component coverage is computed after model output from pinned scope and participant answers. `INVENTORY_COMPARE` preserves accepted baseline item IDs and confirmed pairs, with stricter instructions for `PRESENT_UNCHANGED` and `MISSING`; a `NO_BASIS` projection is derived when the baseline item lacked a usable reference view, without inventing a new item identity. Parser selection is `(task, schemaDigest/version)` from the pinned generation, never the current global schema alone. This is new work: `ParseTask` currently rejects any definition that differs from the code's present schema, so the first schema change without it would fail every old snapshot. The 80-candidate cap is per environment, and diagnostics must count candidates by reason (returned, invalid, unsupported evidence, truncated) without private content. Use the existing strict JSON subset and keep old schema readers.

The request builder uses the pinned profile even with zero voice notes; voice adds labeled declarations only. A deterministic validator rejects evidence IDs outside the request/room, separates visual from declared attributes, and flags unverified material/function/ownership terms for review. Candidate counts include model-returned, invalid, unsupported-evidence, and truncated items. An explicit schema-compatible prompt experiment precedes activation of v2 catalog output, using sanitized fixtures and the product LiteLLM path; changing a prompt or code default never silently replaces database definitions or old snapshots.

### Reportability and publication

Modify `capture/core.Evaluate` and Capture PWA so an expected v3 room/component gap can be submitted with `confirmIncomplete=true`; keep current hard rejection for unverified/failed media still attached to the submission. If no usable photo exists, the submitted room remains `NOT_OBSERVED`. Analysis/classification must emit a terminal reportable result for `FAILED` or `INCONCLUSIVE` instead of waiting forever or treating it as success; today `classify_inspection` returns a permanent error when there are no jobs, so this is new work. `NOT_AUTHORIZED` has no producer because AI consent stays mandatory (PRD D-1): keep the state in the schema and projections only. Snapshot v3 is used for every inspection created after rollout (each gets a scope row; v1/v2 templates get an empty scope that reports as "Escopo não definido"); inspections already in flight keep v1/v2 behavior. The report is first issued from the classified event, and later facts reissue under PRD D-4: a manager-triggered `reissueReport(inspectionId, expectedVersion, clientMutationId)` and each new analysis generation produce a new version only when the canonical facts differ. The handler builds a v3 snapshot from the selected terminal generations and creates its `PUBLISHED` row in the same transaction using the existing lock/input-digest idempotence. PDF rendering stays asynchronous and retryable. Existing v1/v2 policy behavior remains intact; Admin copy must explain that the manual policy does not gate the new report workflow.

### Presentation and provenance

The report projector groups items by existing `typeCode` for reading, without changing per-component counts. It includes apparently sound components, relevant findings, incomplete coverage, unreviewed labels, processing status, and separate handover records. A generic positive phrase is suppressed when coverage is unknown, incomplete, or analysis failed. Findings that overlap a positive catalog entry by room/evidence are retained and flagged; no last-writer-wins merge. Customer projection uses a field allowlist for descriptions and limitations, withholding internal confidence, rejected suggestions, raw notes, and backstage findings. PDF and Dashboard consume the same audience projection; UI layout can differ but factual lists/counts must match. A stable verification code identifies a published version without granting evidence access; media URLs are authorized and resolved at request time.

## Integration Points

| Boundary | Use and authorization | Failure handling |
| --- | --- | --- |
| LiteLLM structured vision | Existing worker adapter with pinned task, schema, model alias/revision, and authorized image set | Existing retry policy; terminal failure/inconclusive state appears in report, raw provider error only in logs. |
| MinIO/private media | Existing upload, derivative, retention, and signed download flow | Failed/pending upload cannot count as evidence; expired/blocked source becomes unavailable label without changing report JSON. |
| Keycloak/session and invite links | Existing role/tenant scopes for manager/Admin and link-scoped Capture auth | Reject cross-tenant/expired access at each query/mutation and at download time; safe Portuguese error. |
| RabbitMQ/outbox | Existing durable analysis/classified/PDF jobs | Duplicate events dedupe by source key, generation/input digest, snapshot lock, and publication identity. |

PostgreSQL is the existing persistence boundary; new tables use tenant RLS and the privileged migrator. No new external provider is introduced.

## Impact Analysis

| Component | Impact Type | Description and Risk | Required Action |
| --- | --- | --- | --- |
| Template compiler/seeds/Admin | Modified | v3 shape and old-version compatibility; medium risk | Validate and expose scope editor without tenant prompt editing. |
| Inspection core/reference resolver | Modified | New baseline gate touches manual/scheduled/project paths; high risk | Centralize transactional validation and preserve legacy reads. |
| Capture core/PWA | Modified | Incomplete submission changes a current gate; high risk | Distinguish missing component from failed media; test mobile/offline retry. |
| Database/migrations | New/modified | Scope, answer, handover, description, provenance storage; medium risk | Add RLS and planner migration; preserve old columns. |
| Analysis prompt/request/parser | Modified | Mode split and versioned schema; high risk | Retain old readers and run controlled corpus comparison. |
| Inventory review | Modified | New fields and source validation; medium risk | Preserve existing decision semantics and audit. |
| Classification/report/publication | Modified | v3 snapshot and automatic path; high risk | Terminal fallback, transactional idempotence, immutable versions. |
| Customer projection/PDF | Modified | Content equivalence and private data protection; high risk | Explicit allowlist and short/long render checks. |
| Schedules/onboarding/project creation | Modified | Guard must hold in every adapter, and schedules already created can fail later; medium risk | Test each entry point and surface materialization failures. |
| Media/recapture | Modified | Capture-time source, EXIF read, deterministic suggestions; low risk | Add `received_at` and source columns; keep GPS out. |
| Dashboard/GraphQL generated clients | Modified | New scope, coverage, report fields; medium risk | Regenerate and update existing views/tests. |

## Testing Approach

Use Go table-driven/unit tests in slice packages, integration tests with existing PostgreSQL/tenant harness for RLS, snapshot, and publication boundaries, Dashboard/Capture unit tests in their current framework, and Playwright E2E in each app. Fake only LiteLLM, object storage, time, and external auth at I/O boundaries; recorded sanitized model responses exercise request → catalog → snapshot → projection without live model calls. A separate product LiteLLM corpus evaluation checks semantic description quality, uncertain attributes, and false absence before prompt activation; it does not replace deterministic CI tests. Render PDF fixtures for one photo, many photos, long text, missing media, and short QR layout; inspect text extraction/page boundaries as well as image snapshots. The concrete contract is [_tests.md](_tests.md).

Run the repository's required Go checks (`go test ./...`, `go vet ./...`, `go build ./...`) for implementation changes, the affected frontend test suites and E2E, GraphQL contract generation check, and Compozy task validation. New slices require setup happy/invalid/dependency-failure/missing-dependency tests per AGENTS.md.

## Development Sequencing

### Build Order

1. Template v3, scope snapshot/confirmation, GraphQL contract, and baseline eligibility — establishes the immutable input contract.
2. Capture component/handover answers and incomplete submission — consumes the scope contract.
3. Catalog/finding prompt versioning, descriptive item fields, provenance, and terminal-state analysis — consumes scope/evidence and retains historical parsers.
4. Snapshot v3 assembly, coverage/conflict rules, and automatic publication — consumes completed capture/analysis; old snapshots remain readable.
5. Customer projection, Dashboard and Maroto PDF presentation, and verification endpoint — consumes the snapshot contract.
6. Comparison completeness and reader experience — consumes approved baseline, item evidence, and v3 projection.

### Technical Dependencies

- No new infrastructure is required. PostgreSQL, RabbitMQ, MinIO, LiteLLM, Keycloak, and Maroto are already present.
- Template v3 and generated GraphQL types precede Capture and UI consumers; report snapshot v3 precedes PDF/Dashboard projection work.
- Use sanitized fixtures rather than the real external report's token, personal data, address, or image in tests and documentation.
- Before changing the active prompt, inspect the actual persisted revision/model alias/media transform used by a local fixture; embedded defaults alone are not proof of current runtime behavior.

## Monitoring and Observability

- Log structured IDs (tenant, inspection, generation, report version) and status transitions, never raw media/transcripts or public verification codes.
- Count catalog candidates returned, rejected by schema/evidence, truncated, and persisted; count expected/fully/partly/not-observed components and terminal AI failure/consent states.
- Track snapshot/publication deduplication, PDF render failures/retries, and customer media unavailable counts. Alert on stuck terminal-to-report transitions and repeated PDF failure using existing operational alerting conventions.
- Record effective prompt/schema/model revision and media transform digest for each generation. Unknown provider model revision is recorded as `UNKNOWN` rather than guessed.

## Technical Considerations

### Key Decisions

- Pin a validated template v3 scope per inspection, then store participant answers against stable keys (ADR-006). This avoids deriving expectations from AI output.
- Separate current and comparative findings while keeping old readers and evaluating prompt changes before activation (ADR-007). This favors compatibility over a single mutable schema.
- Store full report facts once in v3 and apply audience allowlists before all renderers (ADR-008). This adds projection code but prevents live-draft drift and customer leaks.
- Publish v3 automatically despite the existing manual default, scoped to this workflow (ADR-009). The Admin must clearly distinguish policy applicability.
- Gate new move-out creation at the shared core and resolver, not only in the UI (ADR-010).

### Known Risks

- Current v2 capture submission rejects expected missing rooms even with `confirmIncomplete`, unless an impossibility reason was recorded; changing both backend and PWA is necessary while retaining pending-media rejection.
- Existing schedules and in-flight projects can start failing at materialization once the baseline guard ships; they must surface as attention items.
- Video inspections are outside this spec (PRD D-2); the guard and v3 projections must not regress video behavior.
- A counterparty-contested baseline is still eligible under PRD D-5; this may surprise reviewers.
- The classification path may not currently emit a report for no AI consent or no job; add an explicit terminal state/event so requests cannot hang.
- Catalog candidate truncation at 80 per room can hide apparently sound items. Expose counts/limitation and validate against a furnished corpus; do not state complete coverage after truncation.
- Schema evolution could break old prompt snapshots if the parser uses only the latest task schema. Dispatch by pinned schema digest/version and test old fixtures.
- HTML, GraphQL, customer view, and PDF may diverge when each renders independently. Render from the same audience projection and compare semantic fixtures.
- Report-safe data and public verification must never provide an accidental media bearer token. Authorization is checked on every private media request.

## Product Decisions Applied

PRD D-1 to D-5 are closed: AI consent stays mandatory (`NOT_AUTHORIZED` has no producer), photo inspections only, scope frozen at first capture activity, manager-triggered reissue, and eligibility of the active or an explicitly selected superseded approved version regardless of counterparty contestation. Keep each rule in the shared core so a later change does not rework the slices.

## Architecture Decision Records

- [ADR-001: Focus the workflow on checklist, descriptive report, and move-out comparison](adrs/adr-001.md) — Product scope.
- [ADR-002: Publish reports automatically with explicit uncertainty](adrs/adr-002.md) — Product publication rule.
- [ADR-003: Use a confirmed template scope for component coverage](adrs/adr-003.md) — Product denominator/profile rule.
- [ADR-004: Require an approved inventory baseline for new move-out inspections](adrs/adr-004.md) — Product eligibility rule.
- [ADR-005: Separate coverage, condition, and comparison conclusions](adrs/adr-005.md) — Product report semantics.
- [ADR-006: Version the template scope and pin an inspection-specific copy](adrs/adr-006.md) — Scope storage and compatibility.
- [ADR-007: Separate current findings from comparison and version structured schemas](adrs/adr-007.md) — Analysis contract evolution.
- [ADR-008: Build one immutable report snapshot with explicit audience projections](adrs/adr-008.md) — Report data flow.
- [ADR-009: Use automatic publication for the new report workflow](adrs/adr-009.md) — Transactional issue and policy boundary.
- [ADR-010: Enforce approved-baseline eligibility in every new move-out entry point](adrs/adr-010.md) — Shared creation invariant.
