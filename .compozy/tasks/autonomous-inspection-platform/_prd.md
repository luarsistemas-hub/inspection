# Product Requirements Document: Autonomous Inspection Platform

## Overview

The Autonomous Inspection Platform is a multi-tenant SaaS product for organizations that need recurring, milestone-based, or ad hoc visual inspections without sending an internal employee to every capture. It initially serves property managers, construction operations, and cleaning operations while using one declarative inspection model that can support future segments.

The product separates two responsibilities. Internal tenant users configure assets, participants, inspection templates, references, schedules, projects, and review scope. An invited external participant authenticates without creating an account and captures evidence through a guided mobile web experience. The platform preserves evidence provenance, compares the captured state with the applicable reference, produces structured AI-assisted findings, and generates an internal immutable report for triage.

The primary value is continuous, comparable evidence at lower operational friction. Property managers can request periodic condition evidence against a fixed photographic origin. Construction managers can follow planned progress and nonconformities across stages. Cleaning managers can compare an origin with one or more completed stages and review checklist-based quality. The product helps internal reviewers focus attention but never treats AI as proof of authenticity, assigns fault, or automatically imposes a consequence on an external participant.

The existing repository contains only a minimal contract endpoint and does not implement these capabilities. This PRD therefore defines a new product rather than an extension of an existing inspection workflow.

## Goals

- Allow a tenant to represent its organizational units, users, participants, assets, and inspection responsibilities without exposing another tenant's data.
- Let internal users initiate inspections through recurrence, construction or service milestones, and authorized manual creation.
- Let an external participant create an origin or complete one assigned inspection without creating a dashboard account.
- Guide participants through comparable visual evidence, required descriptions, checklists, impossibility reasons, location capture, and resumable uploads.
- Preserve immutable origin, evidence, analysis, and report versions so an internal reviewer can reproduce what each result used.
- Support fixed-origin, planned-stage, previous-inspection, before/after, and checklist-only comparison modes through versioned declarative templates.
- Make multi-stage capture and consolidated-versus-historical report presentation configurable for any segment.
- Detect evidence quality and provenance concerns, expose them as neutral risk flags, and avoid unsupported authenticity or fraud-proof claims.
- Permit directed recapture of specific deficient evidence while preserving every prior submission.
- Always finalize an inspection report once analysis and correction reach a terminal outcome, including a useful inconclusive report when evidence or analysis fails.
- Classify reports deterministically as `NORMAL`, `ATTENTION`, or `CRITICAL` and notify selected internal users of new critical findings.
- Keep reports and findings internal and advisory; make automatic penalties, charges, liability, maintenance actions, or external disclosure impossible in the MVP.
- Apply explicit privacy disclosure, required participant acceptance, sensitive-content prevention, configurable retention, and audited deletion.
- Demonstrate that the same product model supports periodic property, construction, and cleaning inspections without a separate application per segment.

## User Stories

- `US-001`–`US-004`: tenant structure, hierarchical access, participant channels, and auditability.
- `US-005`–`US-007`: immutable templates, segment-specific assets, and effective capture/comparison policies.
- `US-008`–`US-010`: invited origin capture, described reference evidence, and immutable origin versioning.
- `US-011`–`US-014`: recurring, milestone, manual, deadline, reminder, cancellation, and invalidation behavior.
- `US-015`–`US-020`: scoped external authentication, consent, guided capture, provenance, resumable upload, submission, and sensitive-content prevention.
- `US-021`–`US-022`: planned and exceptional stages, skipping, closure, and audited reopening.
- `US-023`–`US-024`: system- or reviewer-directed recapture and deadline finalization.
- `US-025`–`US-030`: structured findings, deterministic classification, immutable reports, portfolio triage, and critical alerts.
- `US-031`: configurable retention, refusal of premature deletion, expiry processing, and deletion audit.
- `US-032`–`US-034`: complete property, construction, and cleaning segment journeys.

[Full user stories](_user_stories.md)

## Core Features

### 1. Tenant, Business-Unit, and Access Administration

The product organizes every resource under exactly one tenant. A tenant can contain multiple business units, and internal users receive both a role and an operational scope.

- `TENANT_ADMIN` administers and reviews the entire tenant.
- `MANAGER` operates assigned business units.
- `EMPLOYEE` operates assigned assets or inspections.
- `VIEWER` reads data in assigned business units without changing product state.
- External participants never receive tenant dashboard access through an inspection invitation.

Role and scope apply to lists, direct links, media access, reports, exports, notifications, and background outcomes. An empty assignment produces an explanatory empty state rather than tenant-wide access.

### 2. Participants and Multi-Channel Delivery

A participant represents a person connected to an asset, occupancy, construction project, or cleaning service. Templates can declare segment-specific roles such as tenant participant, property owner, construction responsible party, contractor, cleaning executor, or cleaning supervisor.

Internal users register email, WhatsApp, and SMS contacts and mark one or more verified channels. There is no single preferred channel. Invitations, reminders, and recapture requests are sent simultaneously to every marked channel. OTP authentication uses the same code across all marked channels, with one shared expiry, resend policy, and attempt counter.

### 3. Declarative Segments and Immutable Templates

A segment version defines the asset-specific information and available inspection behavior. A template version defines:

- segment and permitted participant roles;
- sections, categories, fields, and checklists;
- capture requirements and descriptions;
- applicable comparison mode;
- whether multi-stage capture is enabled;
- planned stages and stage-order rules;
- GPS and other capture-policy defaults;
- analysis-profile reference;
- consolidated or historical report presentation;
- any specialized interaction identifier required beyond the generic renderer.

Published versions are immutable. A newly activated template version affects new occurrences only. Existing inspections and multi-stage projects keep the template, reference, analysis profile, and policy snapshot selected when they started. The MVP provides curated templates through validated administrative publication and does not include a visual template editor.

### 4. Generic Assets with Segment-Specific Data

An asset is any inspectable object or location. Universal identity, tenant, business unit, status, and location information remain consistent. Segment-specific attributes are validated against the selected segment version.

Initial asset types are:

- residential or managed property;
- construction site or construction project scope;
- cleaning location or cleaning service scope.

An asset can define coordinates, a geofence radius, participant relationships, effective template, and policy overrides. Historical inspections keep their captured asset and policy context after later asset changes.

### 5. Photographic Origin and Reference Versioning

An origin is the immutable reference against which later evidence may be compared. An internal user or any person who receives the scoped origin link and can validate its OTP may create it without a dashboard account.

Each origin image requires a nonblank description and category. The product records the original file, capture source, time, GPS, accuracy, device context, hash, quality signals, and risk flags. Live camera is the default. Gallery selection is allowed but flagged.

An origin becomes active automatically when it contains at least one valid described image and all blocking policies pass. Nonblocking quality, gallery, or geofence flags do not prevent activation. An origin is never edited in place; replacement creates a new active version. Inspections already created remain tied to their prior origin version.

### 6. Recurring, Milestone, and Manual Occurrences

An inspection occurrence can start from:

- a recurring schedule interpreted in the tenant's timezone;
- a planned project or service milestone;
- an authorized manual request with a reason.

At creation, an occurrence fixes its tenant, business unit, asset, external responsible participant, template version, origin or other reference, analysis profile, capture policy, report policy, deadline, and reminder schedule. Processing the same trigger repeatedly must produce at most one occurrence.

The tenant defines default deadlines and reminders. A schedule, milestone, or manual occurrence can override those defaults. Cancellation is allowed only before external evidence is submitted. After any evidence exists, an authorized user may invalidate the inspection with a reason; evidence and audit history remain, and the inspection is excluded from valid portfolio indicators.

### 7. Scoped Link and OTP Authentication

Every external capture responsibility uses an individual random, expiring link. Opening the link is not sufficient: the participant must validate the OTP sent to all marked channels. The resulting short session can access only the invited origin, inspection, stage, or recapture requirements.

OTP has bounded validity, resend controls, attempt limits, and rate limiting. Invitation secrets are not retained beyond their security lifetime. A completed, canceled, invalidated, or expired responsibility cannot be mutated with an old link.

Each inspection has one external participant responsible for all external evidence. Other interested people may receive internal notifications but cannot collaborate in the same external capture session.

### 8. Privacy Disclosure and Required Acceptance

Before capture, the participant sees a Portuguese-language disclosure covering:

- why photos and location are collected;
- that evidence is analyzed with AI;
- who controls the data and who can see internal outputs;
- the applicable retention policy;
- that the report supports internal triage and does not automatically impose a consequence.

The participant must explicitly accept photo processing, AI analysis, and GPS use. Refusal blocks the inspection. Device GPS must still satisfy the effective policy described below.

### 9. Guided Mobile Capture

The mobile web flow renders template sections and requirements. Where a visual reference exists, the participant sees its image, description, category, and an optional overlay that helps reproduce framing.

For every requirement, the participant must either:

- capture corresponding evidence; or
- enter a nonblank impossibility reason.

The participant can add extra photos when the template permits it, and every extra photo requires a description. The flow shows overall progress, incomplete items, pending uploads, blocking validations, and the difference between required corrections and optional warnings.

The first cleaning stage can establish an origin. Later cleaning stages compare their evidence and checklist with that origin or another template-selected reference. Construction stages can combine planned-stage expectations, prior evidence, progress fields, and quality or nonconformity checks. Property inspection occurrences compare against the origin fixed at occurrence creation.

### 10. Evidence Provenance, GPS, and Geofence

Every media item preserves its immutable original and associated capture context. The product describes these facts as provenance and risk signals, not as conclusive proof that a scene or actor is authentic.

GPS is required by default for every segment. A template sets the policy, and an asset may override it for future occurrences. When GPS is required, absence of sufficiently accurate location after guided retries blocks capture. When optional, missing or low-accuracy GPS permits capture and creates a flag.

The default geofence radius is 150 meters unless the asset configures another allowed value. Capturing outside the radius never blocks the flow. The evidence records measured distance and accuracy and receives an out-of-geofence flag.

Gallery evidence remains permitted and receives a source flag. Quality concerns, missing optional location, poor accuracy, gallery origin, geofence distance, and other provenance signals remain visible to internal reviewers and can affect classification.

### 11. Sensitive-Content Prevention

Photos must not contain faces, documents, or other identifiable personal information unrelated to the inspection. The product warns participants before capture and evaluates every image before final submission.

Detected prohibited content blocks that item. The participant can retake the image, provide an impossibility reason when the requirement allows it, or declare that detection is a false positive. A false-positive declaration allows the item to proceed, creates a visible internal flag, and records the declaration in the audit trail.

### 12. Resumable Direct Media Capture

Evidence uploads show per-file progress and survive connection loss or page refresh while the invitation remains valid. Pending media is restored from local browser state and resumes from reusable progress when possible.

The product validates size, actual type, hash, stored-object existence, and required capture metadata before marking evidence ready. Retry, completion replay, or recapture never overwrites an original. Read access uses short-lived tenant-scoped authorization and does not make original media public.

### 13. Complete and Incomplete Submission

A participant can submit when uploads and blocking checks settle. A complete submission contains evidence or a valid impossibility reason for every requirement. An incomplete submission is allowed only after the participant explicitly confirms the missing requirements and always classifies at least `ATTENTION`.

Submission makes the evidence set immutable. The participant then sees confirmation only; they cannot browse their submitted evidence, internal findings, classification, or report. A later recapture request uses a new controlled access and exposes only the affected requirements.

### 14. Directed Recapture

The system may request recapture for objective quality or policy failures. An authorized internal user may request it for any specific submitted evidence and must provide an item-level reason.

Recapture:

- reopens only selected requirements;
- preserves and displays prior evidence internally;
- sends a new controlled notice to all marked participant channels;
- lets the participant replace only the selected items;
- preserves each replacement as another immutable evidence version;
- delays the final report until correction completes or its deadline expires.

If the deadline expires with requested evidence uncorrected, the inspection finalizes using available evidence, identifies each unresolved request or inconclusive comparison, and receives at least `ATTENTION`.

### 15. Multi-Stage Inspection Projects

Any segment template can enable multi-stage behavior. On project creation, planned stage names, sequence, requirements, expected references, and dates are copied from the immutable template version.

An authorized internal user may add an exceptional stage with a reason. A planned stage may be skipped with a reason; the skipped stage remains in history and makes the consolidated outcome at least `ATTENTION` until the current-state rules no longer treat it as an active pendency.

An authorized user explicitly closes the project after every planned stage has a terminal decision. Closure blocks new stages and capture. The same authorized scope can reopen the project with a reason; the prior closure and reopening remain visible and audited.

### 16. Structured AI-Assisted Analysis

Analysis compares the applicable reference and submitted evidence under the inspection's immutable analysis-profile version. Each accepted finding contains:

- category;
- title and neutral description;
- severity: `NONE`, `LOW`, `MEDIUM`, `HIGH`, or `CRITICAL`;
- confidence from 0 through 1;
- reference media and current media identifiers;
- evidence quality;
- observations;
- recommended action.

The product accepts only schema-valid structured results. It never tolerantly interprets free-form text as a finding. Invalid responses and transient failures retry only within a bounded policy. A permanent failure becomes an explicit inconclusive comparison and does not prevent final reporting.

Each analysis run retains its evidence identifiers and hashes, template and profile versions, logical model alias and actual model identity when available, prompt version, output schema, usage, latency, reported cost, structured result, errors, and correlation context. These details support reproducibility and usage review, not participant billing in the MVP.

### 17. Deterministic Classification

Classification is evaluated outside generative model prose under the fixed analysis-profile version:

- `CRITICAL`: at least one accepted current finding has `CRITICAL` severity.
- `ATTENTION`: no critical finding exists, but at least one noncritical change, missing or impossible evidence, incomplete submission, uncorrected recapture, skipped stage, gallery/geofence/GPS/quality/sensitive-content flag, or inconclusive analysis exists.
- `NORMAL`: every required comparison completed, no relevant change exists, and there are no flags, missing evidence, skipped requirements, or inconclusive results.

An inspection with no usable evidence can never be `NORMAL`. Replaying the same analysis cannot create duplicate findings or alter deterministic classification.

### 18. Immutable Reports and Stage History

Every inspection produces an internal immutable HTML and PDF report after analysis and correction reach terminal states. Permanent analysis failures appear as inconclusive items rather than blocking the report.

The report includes:

- overall classification and advisory status;
- asset, occurrence, stage, reference, and version context;
- side-by-side reference and current evidence;
- descriptions, impossibility reasons, and extra evidence;
- capture source, location, quality, and risk flags;
- structured findings, severity, confidence, and recommended action;
- inconclusive and uncorrected items;
- invalidation status when applicable.

When a template selects historical mode, every completed stage has its own report in chronological history. When it selects consolidated mode, each stage completion creates a new immutable consolidated report version. The dashboard opens the latest version by default and retains all prior versions.

The consolidated headline classification reflects the latest stage and pendencies still observable in the current state. Prior stage classifications remain visible in a timeline and are never rewritten.

### 19. Internal Dashboard and Critical Alerts

The dashboard shows scoped totals and trends by classification, prioritizes `CRITICAL`, then `ATTENTION`, then `NORMAL`, and supports filters for business unit, asset, segment, period, status, classification, and flags. Reviewers can open evidence comparisons, findings, flags, analysis state, report history, and PDF download.

Invalidated inspections remain discoverable only through explicit history/status filtering and do not count as valid portfolio outcomes.

When a report version first contains a current `CRITICAL` finding, the platform alerts selected internal recipients through their configured internal channels and highlights the inspection in the dashboard. External participants never receive automated finding or classification messages.

### 20. Retention and Audited Deletion

The tenant configures retention by data class within platform policy. Without an override:

- original and derived media, inspections, analysis results, and reports remain for five years after the associated occupancy, service relationship, or multi-stage project closes;
- operational records and separately usable location data remain for one year;
- invitation and OTP secrets remain only until their security expiry.

The MVP does not delete data before its configured retention period ends. An early deletion request is recorded and answered with the active retention restriction. When data becomes eligible, the product deletes or irreversibly de-identifies every applicable original, derivative, report artifact, and personal association and records the outcome in the audit trail.

## Business Rules

### Tenant and Ownership Invariants

1. Every business unit, internal user assignment, participant, asset, template availability, origin, schedule, project, inspection, media item, analysis run, finding, report, notification, usage record, and audit record belongs to exactly one tenant.
2. No query, direct URL, media authorization, notification, export, or background outcome may reveal cross-tenant existence or data.
3. A business-unit, asset, participant, origin, or template that is inactive cannot be selected for a new occurrence.
4. Historical inspection ownership and version context do not change when current tenant configuration changes.

### Internal Permission Rules

1. `TENANT_ADMIN` has tenant-wide administrative and review access.
2. `MANAGER` can manage inspections only inside assigned business units.
3. `EMPLOYEE` can operate only assigned assets or inspections.
4. `VIEWER` can read authorized dashboard and report data but cannot mutate any resource.
5. Only authorized internal roles may create manual occurrences, add exceptional stages, skip stages, close or reopen projects, cancel or invalidate inspections, or request human-directed recapture.
6. All sensitive state changes record actor, reason when required, time, target, tenant, and outcome.

### External Participant Rules

1. One inspection has exactly one external evidence contributor.
2. Possession of an invitation link is insufficient; valid OTP is always required.
3. The same OTP is sent to every marked verified channel and shares expiry, resend, rate, and attempt limits across those channels.
4. A participant session can access only its invited capture responsibility.
5. Required privacy acceptance precedes capture; refusal blocks participation.
6. After submission, the participant sees confirmation only.
7. Recapture uses a new controlled access limited to selected requirements.

### Template and Version Rules

1. Segment definitions, inspection templates, analysis profiles, origins, evidence, analysis results, and reports are immutable after publication or completion.
2. Changing any of them creates a new version.
3. An occurrence fixes all applicable versions and policies when it is created; a later stage additionally fixes its applicable stage reference at stage start.
4. New active versions affect only new occurrences or new projects, never work already started.
5. Multi-stage behavior and report presentation are independent template configuration flags available to all segments.
6. Exactly one comparison mode applies to a capture requirement unless the template explicitly defines separate subrequirements.

### Origin Rules

1. Each origin item has one immutable original, a nonblank description, and a category.
2. At least one valid item is required to activate an origin.
3. Every blocking capture policy must pass before activation.
4. Gallery, quality, and geofence flags do not by themselves block activation.
5. An inspection retains the origin version selected at occurrence creation.

### Scheduling and Lifecycle Rules

1. A schedule creates at most one inspection for each recurrence occurrence.
2. A milestone creates at most one inspection for the same project milestone unless an authorized exceptional stage is deliberately added.
3. Manual creation requires an authorized user and a nonblank reason.
4. Tenant deadline and reminder defaults apply unless the occurrence overrides them.
5. Cancellation is allowed only before evidence submission.
6. After evidence submission, removal is prohibited; an authorized user may invalidate with a reason.
7. Invalidated inspections remain retained and audited but do not count in valid dashboard indicators.
8. Planned stages require a terminal decision before project closure: completed, skipped with reason, canceled before evidence, or invalidated after evidence.
9. Closed projects reject new stages and capture until authorized reopening with a reason.

### Capture and Evidence Rules

1. Every capture requirement has evidence or a nonblank impossibility reason before it is complete.
2. An incomplete submission is permitted after explicit confirmation and can never classify `NORMAL`.
3. Every extra photo has a nonblank description.
4. Live camera is the default; gallery is allowed and always flagged.
5. GPS is required by default. The template sets the default and the asset may override it for future occurrences.
6. Required GPS blocks without a sufficiently valid reading; optional unavailable or low-accuracy GPS adds a flag.
7. The default geofence is 150 meters. Being outside it never blocks and always adds distance and accuracy context plus a flag.
8. Original media is never overwritten. Retake and recapture add evidence versions.
9. Faces, documents, and other unrelated identifiable personal information are prohibited.
10. Sensitive-content detection blocks the item unless the participant records a false-positive declaration; the override is flagged and audited.
11. Submission is idempotent and makes the submitted set immutable.

### Correction and Deadline Rules

1. System-directed recapture is allowed for objective eligible evidence problems.
2. Human-directed recapture requires an authorized internal user, selected evidence, and a reason.
3. Only selected requirements reopen.
4. Prior evidence remains internally visible and immutable.
5. Final reporting waits for recapture completion or deadline.
6. Deadline expiry finalizes with available evidence, labels every unresolved item, and results in at least `ATTENTION`.

### Analysis and Classification Rules

1. Only schema-valid structured analysis can create findings.
2. Every finding has evidence references, severity, confidence, and neutral descriptive language.
3. AI never assigns blame, price, legal responsibility, or an automatic operational action.
4. Permanent analysis failure becomes an inconclusive item and never prevents a report.
5. `CRITICAL` takes precedence over all other classifications.
6. Any noncritical change, missing evidence, applicable flag, skipped stage, incomplete correction, or inconclusive result requires `ATTENTION` when no critical finding exists.
7. `NORMAL` requires complete analyzable evidence, no relevant change, no flags, and no inconclusive result.
8. Classification is reproducible from the immutable analysis-profile version and accepted results.

### Report and Visibility Rules

1. Every terminal valid or invalidated submission receives an immutable report version; cancellation before evidence does not.
2. Reports, findings, classification, and evidence comparison are internal only.
3. External participants do not automatically or manually retrieve reports in the MVP.
4. Consolidated mode creates a new immutable report version after each completed stage.
5. Historical mode creates a separate report per completed stage.
6. Consolidated headline classification reflects current latest-stage state and currently observable pendencies; the timeline preserves previous classifications.
7. A new current critical finding triggers alerts only to configured internal recipients.

### Retention Rules

1. Tenant-specific periods override platform defaults only for future retention evaluation and within allowed policy boundaries.
2. Media, inspections, analysis, and reports default to five years after the related occupancy, service relationship, or project closes.
3. Operational records and separately usable location default to one year.
4. Invitation and OTP secrets expire at their security deadline.
5. Early deletion is not allowed in the MVP, even when requested; the request and response are recorded.
6. Expiry deletion covers originals, derivatives, report artifacts, and applicable personal associations and is audited.

## User Experience

### Tenant Onboarding

1. A Tenant Administrator signs in through the organization's identity provider.
2. The administrator confirms tenant name, default language, timezone, notification defaults, retention periods, and business units.
3. The administrator invites internal users and assigns roles and scope.
4. Managers register participants and verify the email, WhatsApp, or SMS channels that can receive invitations.
5. Managers select curated segment templates and register assets with their segment-specific details.

Configuration screens must explain inheritance clearly: platform default, tenant default, template default, and asset or occurrence override. Historical snapshots must be visible as fixed rather than presented as stale editable values.

### Origin Journey

1. A manager selects an eligible asset and creates an origin invitation or assigns an internal employee.
2. An external contributor receives the link on all marked channels, validates the OTP, reads the disclosure, and accepts required processing.
3. The mobile flow guides photo capture, category selection, description, GPS, quality, and sensitive-content checks.
4. Upload progress survives refresh and connection loss.
5. The contributor finishes with at least one valid described item.
6. The new immutable origin activates automatically and is ready for future occurrences.

### Periodic Property Journey

1. A manager creates a recurring schedule using the tenant timezone, participant, template, active origin, deadlines, and reminders.
2. At each due occurrence, the platform fixes the active versions and sends one invitation across all marked channels.
3. The tenant participant authenticates and sees each origin photo, its description, and optional alignment overlay.
4. The participant captures a corresponding image or explains why it is impossible and may add described extras.
5. The participant submits complete or explicitly incomplete evidence and sees confirmation only.
6. Analysis and any directed correction complete before the internal report becomes final.

### Construction Journey

1. A manager creates a construction project from a versioned template with planned stages, progress expectations, and quality checks.
2. A milestone or authorized manual action creates a stage inspection for one responsible external participant.
3. The participant captures evidence against the planned stage, previous stage, checklist, or configured reference.
4. Analysis identifies observed progress and potential nonconformities without deciding fault or cost.
5. Authorized users may add exceptional stages, skip a planned stage with a reason, or request recapture.
6. Reports appear as a consolidated evolving view or stage history according to the template.
7. An authorized user explicitly closes the project and can later reopen it with an audited reason.

### Cleaning Journey

1. A cleaning template defines an origin stage, later inspection stages, quality checklist, and report mode.
2. The responsible participant records the described origin.
3. One or more later stages record after evidence and checklist responses.
4. The product compares execution and quality and highlights missing or deficient work internally.
5. Reports show either the latest consolidated state with immutable versions or a chronological stage history.
6. The product never measures hours, individual productivity, or worker performance scores.

### Recapture Journey

1. The system flags an eligible objective deficiency or an authorized internal user selects deficient evidence and provides a reason.
2. The participant receives a new notice on all marked channels.
3. After link, OTP, and valid access, only the requested items and reasons appear.
4. Replacement evidence uploads without overwriting the original.
5. Completion resumes analysis; expiry finalizes the report with unresolved items and at least `ATTENTION`.

### Internal Review Journey

1. A scoped internal user opens a dashboard ordered by current classification.
2. Filters narrow the portfolio by unit, asset, segment, period, status, flags, and classification.
3. The reviewer opens a report and compares reference and current evidence side by side.
4. Findings show evidence, severity, confidence, quality, and recommended action in advisory language.
5. The reviewer downloads PDF when needed and takes any operational action outside this MVP.
6. A new critical finding alerts configured internal recipients and deep-links authorized users to the relevant review.

### Mobile, Accessibility, and Failure Guidance

- The capture experience is mobile-first, installable as a PWA, and usable without a native application.
- Camera, location, notification, and storage permission prompts explain why access is needed and what happens if it fails.
- The primary capture, OTP, consent, upload, correction, and submission flows must support keyboard use, screen readers, visible focus, readable contrast, clear labels, and non-color-only status cues.
- Progress is expressed as completed, pending, blocked, and upload state rather than an ambiguous spinner.
- Connection loss, expired access, provider failure, GPS failure, validation failure, and incomplete submission each produce specific recovery guidance.
- The product does not promise fully offline operation; it preserves local pending work and resumes uploads when connectivity returns.
- Portuguese (Brazil) is the initial product language; dates and times use the tenant timezone, defaulting to `America/Sao_Paulo`.

## High-Level Technical Constraints

These constraints describe required product boundaries. The TechSpec owns protocol, framework, package, database, queue, and deployment design.

- The product must provide strict multi-tenant isolation across interactive requests, media, events, notifications, reports, exports, and asynchronous processing.
- External access must use short-lived, inspection-scoped authorization and store no reusable plaintext invitation or OTP secret.
- Dashboard users must authenticate through an OIDC-compatible identity provider and receive server-enforced role and scope checks.
- The product must integrate with SMTP email and Twilio-compatible WhatsApp and SMS delivery; local operation uses safe fake providers and observable mail delivery.
- Original media and every replacement must be immutable, privately stored, hash-addressable for verification, and accessible only through short-lived tenant-scoped authorization.
- Upload must support direct resumable transfer from the PWA and verification before evidence becomes ready.
- Asynchronous occurrence, media, analysis, report, and notification work must be idempotent and recover from duplicates, process restarts, and bounded provider failures without duplicate business results.
- AI access must go through a provider-neutral gateway, accept image inputs, require schema-valid structured output, and record the actual provider/model outcome needed for reproducibility and usage review.
- Report generation must produce stable HTML and PDF artifacts and preserve all information required by the immutable report contract.
- The product must expose health, readiness, metrics, trace correlation, audit, and delivery status sufficient to diagnose user-visible failures.
- The complete product must run in a local development environment with safe local equivalents for identity, messaging, object storage, email, AI gateway, database, frontend, API, background work, scheduling, and PDF generation.
- Production cloud orchestration is outside the MVP, but product boundaries must not assume that all processes share one runtime instance.
- Personal data handling must be reviewed against applicable Brazilian data-protection obligations before production, especially required GPS/AI acceptance, refusal of deletion before configured expiry, international AI/notification subprocessors, and the five-year default.
- Product copy must avoid `fraud-proof`, `tamper-proof`, or equivalent absolute authenticity claims.
- All user-visible behavior is initially Portuguese (Brazil), while identifiers and versioned domain values remain locale-independent.

## Non-Goals (Out of Scope)

- Automatic charges, penalties, liability decisions, maintenance orders, or other participant consequences derived from AI or report classification.
- Internal report approval, assignment, commenting, issue resolution, remediation tracking, or operational case management.
- Automatic or participant-initiated access to reports, findings, classifications, or submitted evidence after confirmation.
- Automatic report sharing with property owners, tenants, contractors, cleaning staff, or other external participants.
- A visual segment, template, prompt, or report designer.
- An end-user marketplace for templates or inspection providers.
- Subscription billing, invoicing, or usage-based charging; usage is recorded for internal visibility only.
- White labeling or tenant-specific branded applications.
- Native iOS or Android applications.
- Fully offline inspection completion; resumable local work is included, but final verification and submission require connectivity.
- Real-time human-assisted video inspections or live inspector fallback.
- Multiple external contributors collaborating in one inspection.
- External integrations that automatically trigger occurrences; the MVP supports recurrence, milestones, and authorized manual creation.
- Multiple simultaneously active physical AI providers selected by end users.
- Automated tolerant parsing of free-form AI responses.
- Proof of scene authenticity, fraud determination, or legal conclusiveness based on GPS, hashes, device metadata, or AI.
- Individual cleaning-worker productivity, timekeeping, or performance scoring.
- Early deletion before the configured retention period ends.
- Broad reuse of the existing contract POC or migration of its incomplete photo behavior.
- Production Kubernetes or cloud rollout orchestration.

## Architecture Decision Records

- [ADR-001: Use a Declarative Multi-Segment Inspection Product Model](adrs/adr-001.md) — One versioned product model serves property, construction, cleaning, and future declarative segments.
- [ADR-002: Treat Evidence Metadata as Risk Signals, Not Proof of Authenticity](adrs/adr-002.md) — Capture context informs internal review without absolute authenticity claims.
- [ADR-003: Support Recurring, Milestone, Manual, and Multi-Stage Inspection Lifecycles](adrs/adr-003.md) — Occurrences and staged projects share explicit, auditable lifecycle rules.
- [ADR-004: Keep AI Reports Internal, Immutable, and Advisory](adrs/adr-004.md) — AI supports internal triage and never causes automatic participant consequences.
- [ADR-005: Use Scoped, Passwordless External Participation with Directed Recapture](adrs/adr-005.md) — Link and OTP provide low-friction capture with targeted correction and preserved evidence.
- [ADR-006: Enforce Configurable Retention with Fixed Default Periods](adrs/adr-006.md) — Tenant policies use explicit defaults and no early deletion.
- [ADR-007: Apply Hierarchical Tenant and Business-Unit Access](adrs/adr-007.md) — Role and scope govern every internal and external access path.

## Open Questions

No unresolved product-direction question remains from this PRD session. The following values are intentionally delegated to the TechSpec because they are operational safeguards or implementation thresholds rather than product-direction choices:

- Exact OTP lifetime, resend interval, attempt count, and rate-limit values.
- Supported media types, per-file size, evidence count, storage quota, and text-length limits.
- GPS accuracy threshold and guided acquisition retry duration for the `required GPS` rule.
- Bounded analysis, media-processing, report-generation, and notification retry schedules.
- Permitted minimum recurrence interval and maximum reminder count.

Legal validation may require a later PRD update if applicable obligations conflict with required consent, the no-early-deletion rule, or the selected default retention periods.
