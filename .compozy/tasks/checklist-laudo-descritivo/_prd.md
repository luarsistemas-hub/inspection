# PRD: Descriptive Checklist, Report, and Move-Out Comparison

## Overview

Inspection already drafts a structured inventory, but its current-state report omits the cataloged items. A cataloging inspection with no alerts can hide room evidence and claim all expected items were checked even when no expected-component denominator exists. The result documents exceptions but does not reliably show what the property looked like, which areas were seen, or what can be compared at move-out.

This feature gives property managers and capture participants a declared component scope, a room-by-room descriptive checklist, and a versioned report that separates visible condition from coverage. It also gives managers and authorized readers an explainable move-out comparison against an approved inventory baseline. Reports issue automatically with clear uncertainty and review labels; baseline activation remains a separate human decision. The [source study](../../../docs/estudo-checklist-laudo-flashvistoria.md) and [prototype](../../../docs/prototypes/checklist-laudo/README.md) inform the journey. Later user decisions in [ADRs](adrs/) supersede the prototype's publication block and its legacy comparison fallback.

The external example demonstrates user expectations for room-based capture, review, and versioned delivery ([Flash Vistoria capture](https://flashvistoria.com.br/ajuda/fazer-vistoria/criar-vistoria-app/), [report finalization](https://flashvistoria.com.br/ajuda/revisar-e-finalizar/finalizar-laudo/)); [Owli's inspection flow](https://imobiliarias.superlogica.com/hc/pt-br/articles/360033662994-Como-fazer-uma-Vistoria-pelo-app-Owli) also shows items by room and item-linked photos. These are product references, not evidence that another AI model is more accurate.

## Goals

- A manager can define the expected room components and views for an inspection and see which profile was applied.
- A participant can capture room evidence, identify impossible or inapplicable views, and complete submission with explicit gaps.
- Analysis describes visible components even when they appear sound, with evidence, assessed extent, uncertainty, and provenance.
- Coverage and conservation are reported independently; absence of alerts cannot imply complete inspection.
- An automatic internal and customer-safe report shows observations, findings, pending required records, processing state, and limitations from a fixed version.
- Later corrections produce new versions without changing an issued document.
- A new move-out inspection requires an approved inventory baseline and pins its version.
- Comparison explains each supported change or lack of evidence with reference/current views and no unsupported absence, responsibility, wear, or cost claim.
- Historical legacy reports remain readable; new legacy media-only comparisons cannot be started.

## User Stories

[Full user stories](_user_stories.md) is the canonical catalog of acceptance criteria and edge cases.

- US-001–US-002 — Template scope and manager confirmation.
- US-003–US-004 — Guided room capture and separate key/meter declarations.
- US-005–US-006 — Descriptive analysis, provenance, review, and approved baseline activation.
- US-007–US-009 — Automatic internal/customer report and immutable versions.
- US-010–US-012 — Eligible move-out creation, item-level comparison, and customer explanation.
- US-013 — Versioned analysis operations and diagnostics.

## Core Features

### F1. Confirmed inspection scope

- A model proposes expected environments, components, useful views, priorities, and required supplemental records according to property type and inspection purpose. There is no universal list of components that must exist in every property.
- An authorized manager can adjust and confirm the proposal before capture. The confirmed scope is frozen for that inspection; later model changes affect new inspections only.
- The manager selects **property components**, **furnished property**, or **selected items**. Voice consent and voice-note presence never choose or change this visual asset profile.
- A visible out-of-scope object may be listed separately as “observed outside scope”; it receives no condition assessment, completeness credit, or baseline membership merely by appearing in a photo.
- Every report identifies the applied profile. Selected-items scope names its selected components.

### F2. Coverage-guided capture

- Capture presents a general room view and any expected detailed views. It distinguishes **observed and assessable**, **partially observed**, **not observed**, **not applicable**, and **impossible to capture**, with a reason for the final two states.
- A suggested recapture names the region, uncertainty, and useful new view. Suggestions do not claim condition and do not prevent final submission.
- The participant can submit with component and room gaps; those gaps remain visible in the report. Media still undergoing receipt or verification is not represented as received evidence.
- Voice remains an optional declaration with separate consent and provenance. It can add context but cannot silently change the asset profile or prove a visual condition.
- A room with no evidence has an explicit not-evaluated state. A required room is never rendered as an empty, apparently completed block.

### F3. Supplemental handover records

- A model may require keys, controls, meter readings, and general observations. These have dedicated sections rather than fabricated room identities.
- Key/control records show type, declared handed-over quantity, responsible declarant, and photo when available. Meter records show type, identifier, reading, unit, date, declarant, and evidence where available.
- A missing required record is a visible pending item in the automatically issued report. The product does not invent a reading or quantity.

### F4. Descriptive current-state analysis and review

- The visual catalog records relevant observed components, including apparently sound ones, by room and component identity. Each item carries type, visual description, apparent condition, observation state, assessed portion, unassessed portion, evidence references, source for declared attributes, and review state.
- Descriptions use only observable attributes. Exact material, mechanism, age, function, measurements, and ownership stay unknown unless supported by an appropriate declaration or human confirmation. A face that appears sound is not described as a whole working item.
- Current-state findings remain visible beside the catalog. A damage finding that conflicts with a generic positive catalog description is preserved and flagged for review; neither task silently overwrites the other.
- Failed, inconclusive, not consented, empty successful, and truncated analyses have distinct states. The report never converts any of them to “no issues found”.
- A manager may review, edit, accept, reject, or add catalog items using the existing inventory workflow. An approved baseline contains only included decisions. A report can contain unreviewed AI suggestions; baseline activation requires human review decisions and remains separate.

### F5. Automatic versioned descriptive report

- Once the available inspection results reach a reportable state, the product automatically issues a new immutable report snapshot and its authorized presentations. Publication does not wait for every AI item to be reviewed, complete component coverage, or every required handover record.
- The current-state report begins with identity, inspection purpose, declared date, issue date, applied scope, version, and processing/review status. It summarizes expected/registered rooms, fully/partly/unassessed components, findings by severity, and pending records without conflating those quantities.
- Room sections present observed components in stable editorial groups with visual description, apparent condition, limitation, review label, and numbered evidence. A room's heading, first observation, and first image stay readable together; photos and captions stay associated. Long reports use an evidence index/annex rather than unreadable thumbnails.
- Internal and customer views of the same audience/version preserve the same permitted facts, limitations, dates, and identifiers across screen, HTML, and PDF. Customer output uses an explicit allowlist and does not reveal internal findings or review backstage through a general summary field.
- An unavailable image is labeled without changing the historical conclusion. A stable verification reference identifies report number and version without granting private evidence or personal data by itself.
- Corrections to source observations or records after issue create a new report version when visible facts change. The earlier version and its effective analysis/scope remain retrievable.

### F6. Approved-baseline move-out comparison

- Creating a new move-out inspection requires a compatible approved inventory baseline for the same asset and authorized scope. In this product a "move-out" inspection is any new inspection that compares against a fixed origin (comparison mode `FIXED_ORIGIN`); the rule applies to every creation path (manual, planned/project, scheduled, onboarding). The chosen version is displayed and pinned before capture. A legacy media-only origin must first be cataloged and approved.
- Existing media-only comparison inspections and issued reports remain readable under their historical interpretation. New media-only comparison creation is unavailable.
- Each approved baseline item receives a current-state observation and a separate historical outcome: unchanged, changed, improved, proven absent, not observed, or no basis to compare. Relevant current objects outside the baseline are informational, not automatically “newly added”.
- A conclusion cites corresponding baseline and current evidence for the same item and room. “Unchanged” needs adequate current evidence; “proven absent” needs enough room coverage, particularly for movable goods. A missing view, uncertain pairing, or baseline gap produces “not observed” or “no basis to compare”.
- Comparison shows the selected baseline version/date, current inspection, paired images, limits, and total item denominator. Filters for changed, improved, absent, not observed, no basis, and unchanged retain accurate counts.
- Visual change alone never assigns responsibility, normal wear, repair cost, or a legal conclusion. A revised pairing or decision produces a new report version.

### Feature interaction

F1 defines the denominator and drives F2/F3; F2/F3 supply evidence and declarations to F4; F4 feeds F5 and, after manager review, the baseline used by F6. F6 produces a comparison report through F5's immutable versioning and audience projection rules. A new prompt or model revision changes future analyses only.

## Business Rules

### Scope and coverage

- The confirmed expected-component set, not AI-detected item count, is the denominator. A component is fully covered only when the necessary portion/views are **observed and assessable**. Partial, not-observed, and impossible states remain separate counts and gaps.
- **Not applicable** removes a component from the applicable denominator only with a recorded reason. Zero applicable expected components yields “scope not defined/no applicable denominator”, never 100%.
- Coverage of rooms, coverage of components, quality of evidence, administrative completeness, and condition are separately labeled. A high coverage count can coexist with damage.
- A component's condition applies only to the assessed portion. An untested function is never implied by a visual “good” condition. `NEW` cannot be inferred solely from lack of visible wear.
- Multiple views of one physical item keep one identity. Similar names in separate rooms do not merge identities. Observed quantity and declared total remain distinguishable.
- Date declared for the inspection, device-supplied capture time, and server receipt time are distinct where available. A discrepancy is flagged for checking, not described as fraud.

### Lifecycle, validation, and permissions

- Only authorized tenant staff may edit a template, confirm an inspection scope, review inventory, activate a baseline, correct a report, or revise a comparison. Capture participants can supply only the evidence and declarations authorized by their link. Readers see only the audience projection and media they are authorized to access.
- Scope is confirmed before capture. Template or profile changes never silently alter a started inspection. A scope change after the first capture activity is not supported; a different scope requires a new inspection.
- Accepted, edited, and added inventory items may enter an approved baseline; rejected and pending items do not. A baseline requires at least one included item. Automatic report issue does not imply baseline approval.
- Report issue occurs automatically when available results are terminal or a documented no-consent/manual state is ready. A report can be incomplete and must label every known gap or failure. An unreceived/processing media upload cannot be treated as analyzed evidence.
- A failed or inconclusive AI job is a processing limitation; no consent is a separate state. Neither is equivalent to “no alerts”. Duplicate completion or issue requests with identical inputs do not create duplicate versions.
- An issued version fixes content, effective scope, baseline reference where present, evidence identifiers, review revision, analysis mode, prompt/model revisions, and issue time. Later media expiry may change access availability, not the version's historical text.
- A new move-out inspection is rejected before capture without a same-asset approved baseline. Existing legacy reports are read-only historical records. Baseline versions do not switch after creation.
- For comparison, **not observed** means insufficient current evidence; **no basis to compare** means the approved baseline did not show the component sufficiently. **Proven absent** requires adequate evidence of the relevant region and room, and a vacant old location alone is insufficient for a movable item.
- External error text shown to users is safe Brazilian Portuguese; provider, database, and raw model errors remain in logs only.

### Presentation

- A current-state report never uses “unchanged”, “all expected items verified”, or a comparison heading when no approved reference was used.
- A “no alerts” summary requires successful relevant analysis and adequate coverage under the confirmed denominator. Otherwise the report names the limitation and keeps observed items and evidence visible.
- A published customer report may label AI observations as **not reviewed**, but never calls them an **approved inventory**. Customer data and media projections honor their existing permissions.
- Keys, controls, and meters required by the model always appear as completed records or explicit pending entries; style choices cannot hide them.
- Same audience and version means equivalent substantive content in Dashboard, HTML, and PDF. The visual layout may differ. Grouping items never changes component counts or the denominator.

## User Experience

### Primary journeys

1. **Prepare** — A template administrator defines a room plan; a manager chooses the asset profile, adjusts expected components/views, and confirms the effective scope. The UI shows which level supplied each requirement.
2. **Capture** — A participant opens the Capture PWA, moves room by room, records a general view and details, sees remaining components, records impossible/inapplicable reasons, and enters required keys/meters in separate sections. Recapture help names the missing view; submission can retain gaps.
3. **Analyze and read** — The product processes evidence, describes observed items, records limits and findings, and automatically issues a versioned report. The manager sees review state and may correct the inventory; readers see the appropriate presentation. The report remains useful if IA is not authorized or processing fails.
4. **Approve baseline** — A manager reviews item decisions and activates a baseline for the asset, independent of automatic report issue.
5. **Move out** — A manager starts a new move-out inspection only after choosing an approved baseline. The capture participant records current views. The manager and authorized reader inspect item-by-item comparison with paired evidence, state, limits, and accurate counts.

### UI and accessibility

- Capture is a participant-facing mobile PWA, without the Dashboard's operations menu. Scope/review/report management lives in authorized back-office surfaces.
- All visible copy is Brazilian Portuguese. Use design-system typography and at least its 12 px minimum for supporting text; state cannot rely on color alone. Recapture guidance and error messages remain associated with the relevant room/component.
- The report's first screen and first PDF page explain purpose, audience, version, and the meaning of “AI suggestion/not reviewed”. The viewer can navigate from a component to its evidence and back without losing context.
- Help for framing, unobserved components, and comparison limitations is available at the relevant action. This is contextual guidance within the current journey, not a new help center.

## High-Level Technical Constraints

- Preserve existing inventory review/activation, tenant isolation, capture consent, report audience projection, media authorization, historical snapshots, and GraphQL contract compatibility.
- The canonical GraphQL schema remains the source for generated clients. Historical report and analysis schema versions must remain readable.
- Existing LiteLLM and media processing boundaries remain; any evaluation of prompt or model changes must distinguish prompt effect from image selection and output projection.
- Capture can be intermittent/offline; a retry must not duplicate evidence, decisions, or issued versions.
- Reports must avoid unsupported physical, functional, ownership, liability, and legal claims. Personal data, tokens, original images, and transcript text must not appear in operational diagnostics.
- PDF output must keep text selectable, captions associated with images, and short/long documents readable. Private media access follows current authorization and retention even when a historical report remains available.

## Non-Goals (Out of Scope)

The user selected the checklist, descriptive report, and move-out comparison for this workflow. The following study opportunities remain **explicit follow-up work after this PRD**, not task omissions:

- Tenant-level AI style preferences, short/long prose controls, free-form prompt customization, and a general preference preview. The confirmed asset scope in F1 is included.
- Company branding, configurable report layouts, editable terms, and a full PDF style preview. The legible descriptive layout in F5 is included.
- A redesigned home onboarding journey, saved inspection filters, folders, bulk operations, and a new asset profile/history workspace. Existing entry points may be linked for this journey.
- Contract-party relationship management, report acknowledgment/contestation, digital signatures, signing provider integration, and legal wording approval.
- Team-management redesign, tracked privacy requests/export, billing/quotas, referrals/rewards, and conversational support.
- Automatic incremental room analysis during capture, approximate numbered image markers, a new structured JSON/CSV export, imports of third-party PDFs, and a new AI-generated prose pass solely to format the report.
- Reworking the existing voice-capture feature; this workflow consumes its declarations and provenance when present.
- Video inspections and video origins (D-2), and submission without AI consent (D-1).

These items are highlighted for later product decisions, including their separate consent, access, legal, and commercial rules where relevant. They are not silently removed from the source study.

## Architecture Decision Records

- [ADR-001: Focus the workflow on checklist, descriptive report, and move-out comparison](adrs/adr-001.md) — Scope and deferred roadmap.
- [ADR-002: Publish reports automatically with explicit uncertainty](adrs/adr-002.md) — Issue promptly while distinguishing unreviewed, incomplete, and failed states.
- [ADR-003: Use a confirmed template scope for component coverage](adrs/adr-003.md) — Stable denominator and voice-independent asset profile.
- [ADR-004: Require an approved inventory baseline for new move-out inspections](adrs/adr-004.md) — Keep old reports readable and gate new comparisons.
- [ADR-005: Separate coverage, condition, and comparison conclusions](adrs/adr-005.md) — Evidence-backed labels and audience-consistent output.

## Resolved Decisions

Technical representation of component views, historical schema migration, and report projection are decided in the TechSpec. Legal terms and signature semantics belong to a future workflow. The following product decisions were made on 2026-11-03 after the documents were checked against the code.

- **D-1 — AI consent stays mandatory.** The invitation and submission flows keep requiring AI-analysis consent, so a participant cannot submit without it. This release produces `FAILED` and `INCONCLUSIVE` terminal reports. The `NOT_AUTHORIZED` state stays readable in schema and projections but has no producer; a "not analyzed by AI because of no consent" report is out of scope until the consent contract changes.
- **D-2 — Photo inspections only.** Video inspections, video origins and video recapture keep their current behavior. Scope, component answers, the descriptive catalog and the baseline gate apply to photo inspections; the F6 gate does not apply to video `FIXED_ORIGIN` creation.
- **D-3 — Scope is frozen at first capture activity.** After the first consent, answer, upload or submission the scope cannot change. A different scope requires a new inspection.
- **D-4 — Corrections reissue explicitly.** A manager triggers "Reemitir relatório" after correcting observations, records or pairings. A new analysis generation also reissues. A reissue creates a new version only when the canonical facts differ.
- **D-5 — Baseline eligibility.** A draft or rejected version is never eligible. The active version, or an explicitly selected superseded approved version, is eligible. Counterparty acknowledgment or contestation does not change eligibility.
