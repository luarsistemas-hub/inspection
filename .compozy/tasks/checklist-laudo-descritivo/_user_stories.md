# User Stories: Descriptive Checklist and Report

Canonical behavior catalog for the move-in checklist, descriptive report, and move-out comparison. Companion to [_prd.md](_prd.md). The scope decisions in this catalog supersede conflicting publication and legacy-comparison behavior in the source prototype.

## Personas

- **Template administrator** — maintains inspection models and required records for a tenant.
- **Inspection manager** — confirms scope, reviews inventory, publishes a baseline, and initiates move-out work within an authorized asset scope.
- **Capture participant** — records room evidence and declared information through the link-scoped Capture PWA.
- **Report reader** — reads an authorized internal or customer report and needs to distinguish facts, AI suggestions, and gaps.
- **Platform operator** — maintains versioned analysis behavior and diagnoses processing without changing issued reports.

## Story Index

| ID | Area | Persona | Story |
| --- | --- | --- | --- |
| US-001 | Scope | Template administrator | Define expected components, views, and records in a model |
| US-002 | Scope | Inspection manager | Confirm a property-specific scope before capture |
| US-003 | Capture | Capture participant | Record room evidence against expected coverage |
| US-004 | Capture | Capture participant | Record keys, controls, and meters as separate declarations |
| US-005 | Analysis | Inspection manager | See descriptive item suggestions with evidence and provenance |
| US-006 | Review | Inspection manager | Correct suggestions and activate an approved baseline separately |
| US-007 | Report | Inspection manager | Receive an automatic versioned checklist report |
| US-008 | Report | Report reader | Read the authorized customer report with the same permitted facts |
| US-009 | Report | Inspection manager | Preserve issued versions after corrections |
| US-010 | Comparison | Inspection manager | Start move-out work only with an approved baseline |
| US-011 | Comparison | Inspection manager | Inspect item-level change with paired evidence and limits |
| US-012 | Comparison | Report reader | Read a comparison without unsupported absence or blame |
| US-013 | Operations | Platform operator | Release analysis changes without rewriting history |

## Scope and capture

### US-001: Configure a model's expected scope

**As a** template administrator, **I want** a model to name expected room components, useful views, asset profile, and required supplemental records, **so that** coverage has a declared meaning.

Acceptance criteria:

- AC-1: Given a new model, when I save a room plan, then its expected components and views are visible before an inspection uses it.
- AC-2: Given a model that requires keys or meters, when an inspection uses it, then those records appear in their own sections rather than as rooms.
- AC-3: Given a furnished profile, when the model is used with or without voice notes, then the same visual asset policy applies.

Edge cases:

- EC-1 (invalid input): An expected component without a room or name is rejected with a Portuguese validation message.
- EC-2 (empty/missing): A model with no expected component list remains usable, but its reports show observed items without a completeness percentage.
- EC-3 (limits): A model exceeding supported component/view limits is rejected without partial publication of the new version.
- EC-4 (permissions): An administrator outside the tenant cannot read or change its models.
- EC-5 (concurrency/repetition): A stale or repeated save cannot silently overwrite a newer model version.
- EC-6 (ordering/state): Changing a model after an inspection starts affects new inspections only; the started inspection keeps its confirmed scope.

### US-002: Confirm the inspection scope

**As an** inspection manager, **I want** to adjust and confirm the model's component list and asset profile before capture, **so that** the checklist matches the real property.

Acceptance criteria:

- AC-1: Given a model, when I prepare an inspection, then I can choose property components, furnished property, or selected items and see the effective list.
- AC-2: Given a selected-items profile, when I confirm it, then each selected item is part of the coverage denominator and the applied profile is shown later.
- AC-3: Given an authorized adjustment, when capture starts, then the participant sees the confirmed room requirements.

Edge cases:

- EC-1 (invalid input): An unknown profile or component identity is rejected before capture starts.
- EC-2 (empty/missing): No expected list yields an “observed items” report without an invented completion score.
- EC-3 (permissions): A manager without asset scope cannot confirm or change its inspection scope.
- EC-4 (concurrency): Two managers confirming different scope revisions receive a conflict rather than silently replacing each other.
- EC-5 (interruption/repetition): Retrying the same confirmation after a lost response returns the same effective scope.
- EC-6 (ordering/state): Once capture has begun, an unannounced template edit does not change the inspection's scope; a later scope change is refused and requires a new inspection.

### US-003: Capture visible coverage

**As a** capture participant, **I want** to see which room views are expected and record what I could capture, **so that** missing evidence is explicit while I am on site.

Acceptance criteria:

- AC-1: Given a room, when I add a general view and details, then the checklist distinguishes observed, partial, not observed, not applicable, and impossible to capture.
- AC-2: Given an incomplete view, when the product suggests a supplement, then it names the region and the useful next view without claiming the component is sound.
- AC-3: Given incomplete coverage, when I submit, then the inspection can proceed and the missing component remains a visible gap in the report.

Edge cases:

- EC-1 (invalid input): “Not applicable” or “impossible” without a reason is rejected with a Portuguese prompt for the reason.
- EC-2 (empty/missing): A room with no usable media is recorded as not observed and cannot be reported as evaluated.
- EC-3 (limits): More media than the capture limit produces the existing limit message and preserves already accepted media.
- EC-4 (permissions): An expired or wrong-tenant capture link cannot add room media.
- EC-5 (interruption/repetition): Retrying a received upload after reconnecting does not create duplicate evidence.
- EC-6 (ordering/state): A supplemental photo arriving after final submission creates an explicit new analysis/report version or is refused; it never changes a published version in place.

### US-004: Record nonvisual checklist information

**As a** capture participant, **I want** separate fields for keys, controls, meter readings, and general notes required by the model, **so that** the document does not hide missing handover data inside a room.

Acceptance criteria:

- AC-1: Given a required key record, when I enter its type and handed-over quantity, then the report identifies it as a declaration and links its photo if one exists.
- AC-2: Given a meter record, when I enter type, reading, unit, date, and available evidence, then the report shows those values separately from visual condition.
- AC-3: Given a missing required record, when the report is issued, then it shows a pending item in that section and does not block publication.

Edge cases:

- EC-1 (invalid input): A negative key quantity or malformed meter reading is rejected with a field-specific Portuguese message.
- EC-2 (empty/missing): An absent required record becomes a pending entry; an optional absent record may be omitted.
- EC-3 (limits): Overlong notes are rejected without truncating a previously saved value.
- EC-4 (permissions): A participant can change only records in the inspection named by the capture link.
- EC-5 (interruption/repetition): Retried record submission returns the saved value once.
- EC-6 (state): A correction after issuance creates a new report version; the previous reading remains in the old version.

## Analysis and review

### US-005: See descriptive observations

**As an** inspection manager, **I want** the visual inventory to describe observed components even when they appear sound, **so that** the checklist documents the current property rather than only exceptions.

Acceptance criteria:

- AC-1: Given a visible component, when analysis finishes, then its description, apparent condition, assessed portion, unassessed portion, review state, and evidence are available.
- AC-2: Given an uncertain material, function, age, or ownership, when analysis finishes, then the claim remains unknown or marked as a declaration rather than a visual fact.
- AC-3: Given an object outside the confirmed profile, when it is clearly visible, then it may appear in a separate “observed outside scope” section without condition or baseline membership.
- AC-4: Given conflicting voice and visual statements, when results are presented, then both sources and the unresolved difference remain visible.

Edge cases:

- EC-1 (invalid input): An AI result citing an unknown evidence ID or another room is excluded or marked invalid; it is never shown as supported.
- EC-2 (empty/missing): No detected items does not become “all expected items checked”; expected components remain gaps.
- EC-3 (limits): If model output exceeds the item limit, the report exposes a truncation/processing limitation rather than implying complete cataloging.
- EC-4 (permissions): A customer sees only the approved customer projection, never internal confidence or review backstage.
- EC-5 (interruption): Failed or inconclusive analysis is shown distinctly from an empty successful result.
- EC-6 (ordering): A late voice transcript never silently replaces a manager's decision or an issued report.

### US-006: Review and activate a baseline

**As an** inspection manager, **I want** to correct AI suggestions and separately activate an approved inventory, **so that** later comparisons use deliberate decisions.

Acceptance criteria:

- AC-1: Given an unreviewed report, when I inspect an item, then I can see original suggestion, source evidence, and my correction without changing the issued version.
- AC-2: Given reviewed items, when I activate a baseline, then only accepted, edited, or added items enter the immutable version.
- AC-3: Given partial approval, when I activate, then pending items are recorded as not reviewed and never masquerade as approved inventory.

Edge cases:

- EC-1 (invalid input): An edit with an unsupported condition or foreign evidence ID is rejected.
- EC-2 (empty/missing): Activation without any included item is rejected; the automatic report remains readable.
- EC-3 (permissions): A participant or customer cannot activate a baseline.
- EC-4 (concurrency): Competing item decisions or activation attempts cannot create two active versions for one asset.
- EC-5 (repetition): Retrying successful activation returns the active version without duplicating it.
- EC-6 (ordering/state): A superseded baseline cannot be edited; corrections create a new draft/version.

## Descriptive report

### US-007: Receive an automatic internal report

**As an** inspection manager, **I want** a report to be issued automatically from the available inspection results, **so that** I can deliver evidence promptly and see unresolved limits.

Acceptance criteria:

- AC-1: Given completed analysis, when the report is generated, then each room includes observed components, limitations, numbered evidence, and findings even if there are no alerts.
- AC-2: Given unreviewed AI items or coverage gaps, when the report is issued, then those states and the exact denominator or “unknown scope” appear without blocking issue.
- AC-3: Given missing AI consent or terminal processing failure, when the report is issued, then it says “not analyzed by AI” or “analysis failed” and never claims normality.
- AC-4: Given required keys or meters missing, when the report is issued, then the corresponding pending records are visible.

Edge cases:

- EC-1 (empty/missing): A report with no usable room evidence says “no evidence recorded”, not “no defects”.
- EC-2 (limits): A long report keeps headings, captions, and legible photos together without a QR-only page.
- EC-3 (permissions): An unauthorized user cannot open the internal document or its private media.
- EC-4 (concurrency/repetition): Repeated completion events with identical inputs issue one version, not duplicate versions.
- EC-5 (interruption): A PDF render failure leaves a visible retryable artifact state while the snapshot remains intact.
- EC-6 (state): AI catalog and findings that disagree show the damage finding and an unresolved conflict, not a positive summary.

### US-008: Read a customer-safe report

**As a** report reader, **I want** the customer report to show the same permitted facts as its on-screen view, **so that** the PDF is understandable without internal-only data.

Acceptance criteria:

- AC-1: Given authorization for a customer report, when I open HTML or PDF of the same version, then descriptions, limitations, dates, evidence identifiers, and revision labels agree.
- AC-2: Given internal-only findings or review metadata, when I open the customer report, then they are absent while customer-safe descriptive information remains.
- AC-3: Given unavailable media, when I open the report later, then its historical conclusion stays fixed and media availability is accurately indicated.

Edge cases:

- EC-1 (invalid input): An altered or unknown report-version reference is rejected without exposing another report.
- EC-2 (empty/missing): No customer-visible evidence yields an explicit unavailable/no-evidence state, not an empty image slot.
- EC-3 (permissions): A revoked or cross-tenant reader cannot fetch the document or media.
- EC-4 (limits): A many-photo room uses an indexed annex while keeping a readable room summary.
- EC-5 (interruption): A failed media fetch does not change the version's text or leak a storage URL.
- EC-6 (state): An expired media link can be refreshed only for a still-authorized reader; the issued PDF is not silently rewritten.

### US-009: Keep issued versions stable

**As an** inspection manager, **I want** later corrections to create a new report version, **so that** recipients can identify exactly which document they received.

Acceptance criteria:

- AC-1: Given an issued report, when I correct an item or supplemental record, then the old report remains retrievable with its original content.
- AC-2: Given a new version, when I view its history, then I can see version, issuance time, effective scope, review state, and prior version.
- AC-3: Given a digital verification link, when someone checks it, then it identifies a specific issued version without granting private photos or personal data by itself.

Edge cases:

- EC-1 (invalid input): An unknown prior version cannot be overwritten or selected as an existing version.
- EC-2 (empty/missing): A correction that changes no report facts produces no duplicate version.
- EC-3 (permissions): Only authorized staff can request a correction; the reader sees only versions they are allowed to see.
- EC-4 (concurrency): Simultaneous corrections serialize into distinct versions or an explicit conflict.
- EC-5 (interruption/repetition): Retrying a version request after timeout does not issue a duplicate artifact.
- EC-6 (ordering/state): A newly published prompt or template never changes an already issued version.

## Move-out comparison

### US-010: Start an eligible move-out inspection

**As an** inspection manager, **I want** the product to require a compatible approved baseline before a new move-out inspection, **so that** every new comparison has a reliable reference.

Acceptance criteria:

- AC-1: Given an approved baseline, when I start move-out work, then its selected version and asset are shown and pinned to the inspection.
- AC-2: Given only a media-only legacy origin, when I attempt a new move-out, then I am directed to catalog and approve that origin first.
- AC-3: Given an old legacy comparison, when I open it, then its issued report remains readable without migration.
- AC-4: Given a scheduled, planned or onboarding move-out, when it is created or materialized, then the same baseline rule applies, and a refusal is visible to the manager rather than silent.

Edge cases:

- EC-1 (invalid input): A baseline for another asset or tenant is rejected.
- EC-2 (empty/missing): No approved baseline blocks creation with a Portuguese explanation and path to prepare one.
- EC-3 (permissions): A manager lacking the asset scope cannot use its baseline.
- EC-4 (concurrency): A baseline superseded during creation yields an explicit version choice or conflict; no silent switch occurs.
- EC-5 (repetition): Retrying creation does not create duplicate move-out inspections.
- EC-6 (ordering/state): Later baseline activation never changes the version pinned by an existing move-out inspection.

### US-011: Review an explainable comparison

**As an** inspection manager, **I want** each baseline component compared with current evidence and explicit limitations, **so that** I can distinguish change, improvement, absence, and missing coverage.

Acceptance criteria:

- AC-1: Given equivalent entry and current views, when comparison finishes, then each conclusion opens both linked evidence views and identifies the baseline version.
- AC-2: Given insufficient current or baseline coverage, when comparison finishes, then the item is “not observed” or “no basis to compare”, never “missing” or “unchanged”.
- AC-3: Given an apparently missing movable item, when only its old location is empty, then the product asks for adequate room coverage before claiming absence.
- AC-4: Given a prior damage, when current evidence shows the same damage, then current condition and historical change remain separate.

Edge cases:

- EC-1 (invalid input): An AI comparison citing another room's item or evidence cannot support a conclusion.
- EC-2 (empty/missing): An item without an equivalent baseline view is labeled “no basis to compare”.
- EC-3 (limits): Many unchanged items remain accessible through filters without hiding the total denominator.
- EC-4 (permissions): Only authorized staff can revise pairings or comparison decisions.
- EC-5 (interruption): Failed comparison processing is shown as failure, not “no changes”.
- EC-6 (ordering/state): Correcting a pairing after issue creates a new report version; the old conclusion remains available.

### US-012: Read a customer comparison

**As a** report reader, **I want** the move-out report to show reference/current evidence and measured change without assigning blame, **so that** I can understand what is documented and what remains uncertain.

Acceptance criteria:

- AC-1: Given an authorized comparison report, when I read a changed item, then I can see baseline version, current evidence, prior evidence, status, and limitation.
- AC-2: Given an improvement, proven absence, not-observed item, or no-basis item, when I filter or read the report, then each has its own label and count.
- AC-3: Given a visual difference, when I read the report, then it does not assert tenant responsibility, normal wear, or repair cost without a separately approved basis.

Edge cases:

- EC-1 (invalid input): An invalid filter or version reference yields a safe error without data from another report.
- EC-2 (empty/missing): A comparison with no supported change shows coverage and unresolved items rather than “everything unchanged” by default.
- EC-3 (limits): A large baseline displays total and displayed counts consistently across filters and PDF sections.
- EC-4 (permissions): A customer cannot see internal confidence, rejected items, or evidence outside the authorized report.
- EC-5 (interruption): Temporarily unavailable prior media is labeled unavailable while its historical conclusion and version remain.
- EC-6 (state): A later baseline version does not change the baseline number or text in the issued comparison.

## Operations

### US-013: Evolve analysis safely

**As a** platform operator, **I want** to distinguish prompt/model/media revisions and processing outcomes, **so that** a new analysis behavior can be evaluated without erasing historical reports.

Acceptance criteria:

- AC-1: Given a report version, when I inspect its provenance, then I can identify effective analysis mode, prompt revision, model revision, applied scope, and evidence set.
- AC-2: Given a new analysis definition, when it is released, then existing issued reports and pinned analyses remain unchanged.
- AC-3: Given dropped, invalid, or truncated catalog candidates, when I diagnose a run, then I can see counts by reason without seeing private image or transcript content in logs.

Edge cases:

- EC-1 (invalid input): A response incompatible with its pinned schema is rejected as a processing failure, not rendered as success.
- EC-2 (empty/missing): Missing model revision metadata is recorded as unknown rather than invented.
- EC-3 (limits): Candidate and image limits produce explicit diagnostics and visible report limitations where coverage is affected.
- EC-4 (permissions): Tenant administrators cannot edit platform-wide prompt definitions through tenant preferences.
- EC-5 (interruption/repetition): Retrying the same analysis attempt does not replace an issued snapshot; a requested reanalysis creates a new version.
- EC-6 (ordering/state): Rolling back the active prompt applies to future analyses only and preserves old schema readers.

## Edge-case sweep

Every story was probed for invalid input, empty or missing data, limits, permissions, concurrency, interruption, repetition, ordering, state transitions, and scale. The EC lists record every distinct observable result. The following cells identify classes that share a recorded EC or have no separate action at this story boundary; those classes are still covered at the producing story or component boundary.

| Story | Invalid | Empty | Limits/scale | Permission | Concurrency | Interruption | Repetition | Ordering | State |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| US-001 | EC-1 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-002 | EC-1 | EC-2 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-003 | EC-1 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-004 | EC-1 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-005 | EC-1 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-006 | EC-1 | EC-2 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-007 | EC-6 | EC-1 | EC-2 | EC-3 | EC-4 | EC-5 | EC-4 | EC-6 | EC-6 |
| US-008 | EC-1 | EC-2 | EC-4 | EC-3 | EC-6 | EC-5 | EC-6 | EC-6 | EC-6 |
| US-009 | EC-1 | EC-2 | EC-4 | EC-3 | EC-4 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-010 | EC-1 | EC-2 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-6 | EC-6 |
| US-011 | EC-1 | EC-2 | EC-3 | EC-4 | EC-6 | EC-5 | EC-6 | EC-6 | EC-6 |
| US-012 | EC-1 | EC-2 | EC-3 | EC-4 | EC-6 | EC-5 | EC-6 | EC-6 | EC-6 |
| US-013 | EC-1 | EC-2 | EC-3 | EC-4 | EC-5 | EC-5 | EC-5 | EC-6 | EC-6 |

For read-only US-008 and US-012, concurrent/repeated reads have the same pinned content (EC-6). For US-005, repeated or concurrent model attempts use one selected generation (EC-5); they do not create a second user action. At 100× normal item volume, pages, PDFs, and filters must retain the same totals and labels; the relevant limits/scale EC for each story applies.
