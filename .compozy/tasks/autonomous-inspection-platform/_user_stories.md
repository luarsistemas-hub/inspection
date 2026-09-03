# User Stories: Autonomous Inspection Platform

Canonical behavior catalog for the Autonomous Inspection Platform. Companion to
`_prd.md`; consumed by `_techspec.md` (component mapping) and `_tests.md`
(coverage matrix).

## Personas

- **Tenant Administrator** — owns the tenant configuration, business-unit structure, templates available to the tenant, retention rules, staff access, and cross-unit oversight.
- **Manager** — operates assigned business units, assets, schedules, projects, inspections, reports, and internal alerts.
- **Employee** — performs work on assigned assets or inspections, including origin capture, review, manual occurrence creation, and directed recapture.
- **Viewer** — reviews dashboards, evidence, findings, and reports for assigned business units without changing state.
- **Property Owner or Origin Contributor** — creates a property origin through a scoped invitation without a dashboard account.
- **Tenant Participant** — completes an autonomous periodic property inspection through a scoped invitation.
- **Construction Responsible Party** — records planned progress and observed defects for a construction project.
- **Cleaning Executor or Supervisor** — records origin and later cleaning stages and completes quality checklists.
- **Privacy Requester** — asks the tenant to explain or delete personal data associated with an inspection.

## Story Index

| ID | Feature Area | Persona | Story |
|---|---|---|---|
| US-001 | Tenant Administration | Tenant Administrator | Configure tenant and business units |
| US-002 | Tenant Administration | Tenant Administrator | Assign hierarchical internal access |
| US-003 | Tenant Administration | Manager | Manage external participants and delivery channels |
| US-004 | Tenant Administration | Tenant Administrator | Review the audit trail |
| US-005 | Templates and Assets | Tenant Administrator | Publish immutable template versions |
| US-006 | Templates and Assets | Manager | Register a segment-specific asset |
| US-007 | Templates and Assets | Manager | Configure comparison and capture policies |
| US-008 | Origin | Manager | Invite an origin contributor |
| US-009 | Origin | Origin Contributor | Capture a described origin |
| US-010 | Origin | Manager | Activate and supersede an origin version |
| US-011 | Occurrence Management | Manager | Configure recurring inspections |
| US-012 | Occurrence Management | Manager | Create milestone and manual inspections |
| US-013 | Occurrence Management | Manager | Configure deadlines and reminders |
| US-014 | Occurrence Management | Manager | Cancel or invalidate an inspection |
| US-015 | External Access | External Participant | Authenticate and accept required processing |
| US-016 | Capture | External Participant | Complete guided capture requirements |
| US-017 | Capture | External Participant | Record provenance, GPS, and geofence signals |
| US-018 | Capture | External Participant | Resume interrupted media uploads |
| US-019 | Capture | External Participant | Submit complete or incomplete evidence |
| US-020 | Capture | External Participant | Prevent accidental sensitive content |
| US-021 | Multi-Stage Inspections | Manager | Plan standard and exceptional stages |
| US-022 | Multi-Stage Inspections | Manager | Skip, close, and reopen a staged project |
| US-023 | Evidence Correction | Manager | Request directed recapture |
| US-024 | Evidence Correction | External Participant | Complete or miss a recapture request |
| US-025 | Analysis | Manager | Receive structured evidence findings |
| US-026 | Analysis | Manager | Apply deterministic inspection classification |
| US-027 | Reports | Viewer | Review an immutable inspection report |
| US-028 | Reports | Manager | Review consolidated or historical stage reports |
| US-029 | Dashboard | Manager | Triage the inspection portfolio |
| US-030 | Notifications | Manager | Receive critical-finding alerts |
| US-031 | Privacy | Tenant Administrator | Configure and enforce retention |
| US-032 | Property Inspections | Tenant Participant | Complete a periodic property inspection |
| US-033 | Construction Inspections | Construction Responsible Party | Report progress and nonconformities |
| US-034 | Cleaning Inspections | Cleaning Executor or Supervisor | Demonstrate cleaning execution and quality |

## Tenant Administration

### US-001: Configure tenant and business units

**As a** Tenant Administrator, **I want** to configure my organization and its business units, **so that** inspections and access follow the way my operation is divided.

Acceptance criteria:

- AC-1: Given a new tenant, when the administrator supplies its name, language, default timezone, and at least one business unit, then the tenant becomes available for configuration.
- AC-2: Given an active tenant, when the administrator adds or updates a business unit, then authorized users see the current structure without changing historical inspection ownership.
- AC-3: Given the initial defaults, when no override exists, then the tenant uses Portuguese (Brazil), `America/Sao_Paulo`, and platform notification and retention defaults.

Edge cases:

- EC-1 [Invalid input]: Invalid timezone or blank tenant name → the change is rejected with the invalid field identified.
- EC-2 [Empty / missing]: No business unit on initial setup → activation remains unavailable until one is added.
- EC-3 [Limits]: A configured organizational limit is reached → additional units are rejected without changing existing units.
- EC-4 [Permissions]: A non-administrator attempts tenant configuration → access is denied without revealing settings outside their scope.
- EC-5 [Concurrency]: Two administrators edit the same unit → the stale save is rejected and the current values are shown.
- EC-6 [Interruption]: Setup stops before completion → a draft remains resumable and the tenant is not presented as active.
- EC-7 [Repetition]: The same create request is retried → only one tenant or unit is created.
- EC-8 [Ordering]: A unit-dependent configuration is attempted before the unit exists → the user is directed to create the prerequisite.
- EC-9 [State transitions]: An archived unit is edited → the edit is rejected until the unit is restored.
- EC-10 [Scale]: Hundreds of units exist → lists remain paginated/searchable and counts remain accurate.

### US-002: Assign hierarchical internal access

**As a** Tenant Administrator, **I want** to assign roles and operational scope, **so that** each staff member sees and changes only what their responsibility requires.

Acceptance criteria:

- AC-1: Given an internal user, when the administrator assigns `TENANT_ADMIN`, `MANAGER`, `EMPLOYEE`, or `VIEWER`, then the product explains the resulting capabilities.
- AC-2: Given a manager or viewer, when business units are assigned, then the user can access only those units.
- AC-3: Given an employee, when assets or inspections are assigned, then the employee can operate only those assignments.
- AC-4: Given a viewer, when they open an allowed resource, then every mutating action is unavailable.

Edge cases:

- EC-1 [Invalid input]: Unknown role or resource identifier → assignment is rejected.
- EC-2 [Empty / missing]: A scoped role has no assignments → the user sees an explanatory empty state and no operational data.
- EC-3 [Limits]: A bulk assignment exceeds the supported batch size → the user is asked to split it and no partial batch is applied.
- EC-4 [Permissions]: A manager tries to grant tenant-administrator access → access is denied.
- EC-5 [Concurrency]: Scope changes while the user is active → the next protected action reflects the new scope.
- EC-6 [Interruption]: Bulk scope assignment is interrupted → completed changes are listed and unprocessed changes remain unapplied.
- EC-7 [Repetition]: The same role or assignment is applied twice → the result remains a single assignment.
- EC-8 [Ordering]: Scope is assigned before the user accepts an internal invitation → it becomes effective only after account activation.
- EC-9 [State transitions]: A disabled user attempts access with an existing session → access is denied immediately.
- EC-10 [Scale]: A user has thousands of assigned assets → authorization remains consistent across paginated lists, direct links, exports, and media.

### US-003: Manage external participants and delivery channels

**As a** Manager, **I want** to register external participants and select verified delivery channels, **so that** invitations reach the responsible person through every chosen channel.

Acceptance criteria:

- AC-1: Given a participant, when the manager records their segment role and contact information, then the participant can be linked to an asset, occupancy, service, or project.
- AC-2: Given verified email, WhatsApp, or SMS contacts, when the manager marks multiple channels, then future invitations use all marked channels simultaneously.
- AC-3: Given an updated contact, when it is verified, then it may replace or supplement existing marked channels without changing historical deliveries.

Edge cases:

- EC-1 [Invalid input]: Malformed email or phone number → that channel cannot be saved or marked.
- EC-2 [Empty / missing]: No verified channel is marked → invitation creation is blocked with a contact requirement.
- EC-3 [Limits]: More contacts are supplied than the participant limit → extras are rejected with the limit explained.
- EC-4 [Permissions]: An employee without participant-management scope edits a participant → access is denied.
- EC-5 [Concurrency]: Two managers change marked channels → the stale update is rejected.
- EC-6 [Interruption]: Verification is interrupted → the channel remains unverified and cannot receive invitations.
- EC-7 [Repetition]: The same contact is added again → it is reused rather than duplicated.
- EC-8 [Ordering]: A channel is marked before verification → marking is refused until verification succeeds.
- EC-9 [State transitions]: An inactive participant is selected for a new inspection → selection is blocked.
- EC-10 [Scale]: A tenant has a large participant directory → search and filtering identify the intended participant without cross-tenant results.

### US-004: Review the audit trail

**As a** Tenant Administrator, **I want** to review security and business-critical actions, **so that** I can understand who changed inspection evidence or configuration and when.

Acceptance criteria:

- AC-1: Given an audited action, when it completes, then the trail records actor, tenant, time, action, target, outcome, and available correlation context.
- AC-2: Given the audit view, when the administrator filters by actor, asset, inspection, action, or period, then matching records are returned in stable time order.
- AC-3: Given a recapture, false-positive declaration, invalidation, project reopening, stage insertion, or retention action, then its reason is visible in the audit trail.

Edge cases:

- EC-1 [Invalid input]: Invalid date range or filter → the query is rejected with correction guidance.
- EC-2 [Empty / missing]: No records match → an empty result is shown without implying audit is disabled.
- EC-3 [Limits]: A range contains more records than one page → pagination preserves stable ordering and filters.
- EC-4 [Permissions]: A user without audit permission requests records → access is denied.
- EC-5 [Concurrency]: New events arrive during navigation → existing pages remain stable and a refresh exposes newer records.
- EC-6 [Interruption]: Export or long query is interrupted → no corrupt artifact is presented and the user can retry.
- EC-7 [Repetition]: The same action is safely retried → each attempted outcome is distinguishable without duplicating the business result.
- EC-8 [Ordering]: Events arrive after related business data → correlation still groups them in causal order when available.
- EC-9 [State transitions]: The target is later archived or deleted → its audit records remain readable for their retention period.
- EC-10 [Scale]: Audit volume reaches 100× typical activity → filters, pagination, and tenant isolation remain correct.

## Templates and Assets

### US-005: Publish immutable template versions

**As a** Tenant Administrator, **I want** validated inspection templates to be versioned and activated, **so that** new inspections use predictable rules while existing work remains reproducible.

Acceptance criteria:

- AC-1: Given a valid administrative template definition, when a version is published, then its segment, roles, sections, requirements, policies, and report settings become immutable.
- AC-2: Given a newly activated version, when a new occurrence starts, then it uses that version.
- AC-3: Given an inspection or staged project already started, when another version activates, then the existing work retains its original version.
- AC-4: Given the MVP, then tenant users can select and configure published templates but cannot use a visual template editor.

Edge cases:

- EC-1 [Invalid input]: Definition violates its allowed schema or references an unknown component → publication is rejected with validation details.
- EC-2 [Empty / missing]: Template has no capture requirement → publication is rejected.
- EC-3 [Limits]: Definition exceeds configured section, field, or requirement limits → publication is rejected without truncation.
- EC-4 [Permissions]: Unauthorized user attempts activation → access is denied.
- EC-5 [Concurrency]: Two versions are activated at once → one active version is selected deterministically and the conflict is reported.
- EC-6 [Interruption]: Publication stops before completion → the version remains unpublished and cannot be selected.
- EC-7 [Repetition]: The same publication is retried → no duplicate version is created.
- EC-8 [Ordering]: Activation is requested before validation → validation must succeed first.
- EC-9 [State transitions]: An active version is retired → existing work remains accessible and new work requires another active version.
- EC-10 [Scale]: A tenant can choose among many versions → lists remain searchable and clearly distinguish status and effective version.

### US-006: Register a segment-specific asset

**As a** Manager, **I want** to register a property, construction site, cleaning location, or future inspectable asset, **so that** inspections are linked to validated business context.

Acceptance criteria:

- AC-1: Given a selected segment, when the manager enters universal and segment-specific required fields, then the asset is created in an assigned business unit.
- AC-2: Given location-enabled policies, when coordinates and geofence radius are set, then future capture can evaluate distance against that asset.
- AC-3: Given a segment-specific field definition, when the value changes, then the new value is validated without rewriting historical inspection snapshots.

Edge cases:

- EC-1 [Invalid input]: Segment-specific data violates its schema or geofence radius is out of range → save is rejected with field details.
- EC-2 [Empty / missing]: Required address, identifier, or segment field is absent → activation is unavailable.
- EC-3 [Limits]: Asset media, attribute, or text limits are exceeded → only the invalid change is rejected.
- EC-4 [Permissions]: User lacks scope for the business unit → creation or direct access is denied.
- EC-5 [Concurrency]: Two users edit the asset → stale changes are rejected.
- EC-6 [Interruption]: Creation stops mid-flow → a clearly marked draft may be resumed and cannot be scheduled.
- EC-7 [Repetition]: Same create request is retried → one asset results.
- EC-8 [Ordering]: Schedule creation is attempted before asset activation → the prerequisite is explained.
- EC-9 [State transitions]: Archived asset receives a new occurrence → creation is blocked.
- EC-10 [Scale]: A business unit has thousands of assets → list, filter, and selection behavior remain usable and isolated.

### US-007: Configure comparison and capture policies

**As a** Manager, **I want** template defaults and asset overrides to define capture behavior, **so that** each inspection uses the right reference and evidence requirements.

Acceptance criteria:

- AC-1: Given a template, when comparison mode is configured, then it uses exactly one of fixed origin, planned stage, previous inspection, before/after, or checklist-only.
- AC-2: Given a template, when multi-stage and report-mode flags are configured, then any segment can enable or disable those behaviors.
- AC-3: Given GPS required by default, when an asset override makes it optional, then missing valid GPS produces a flag instead of blocking.
- AC-4: Given a policy snapshot at occurrence creation, then later configuration changes do not alter that occurrence.

Edge cases:

- EC-1 [Invalid input]: Incompatible comparison or report settings are combined → save is rejected with the conflict identified.
- EC-2 [Empty / missing]: A reference-required mode has no eligible reference → occurrence creation is blocked.
- EC-3 [Limits]: A policy value such as radius or stage count exceeds its range → save is rejected.
- EC-4 [Permissions]: User outside asset scope changes an override → access is denied.
- EC-5 [Concurrency]: Template default and asset override change concurrently → the occurrence records the single resolved snapshot it actually used.
- EC-6 [Interruption]: Configuration save is interrupted → prior active settings remain effective.
- EC-7 [Repetition]: Same override is applied twice → one effective rule is shown.
- EC-8 [Ordering]: Asset override is configured before template selection → the user must choose the template first.
- EC-9 [State transitions]: Policy is changed after capture starts → current inspection remains unchanged.
- EC-10 [Scale]: Many assets inherit one template → changes apply to future occurrences without rewriting each historical asset occurrence.

## Origin

### US-008: Invite an origin contributor

**As a** Manager, **I want** to send a protected origin-capture link, **so that** any person with authorized channel access can create the reference without a dashboard account.

Acceptance criteria:

- AC-1: Given an eligible asset and marked verified channels, when the manager creates an origin invitation, then one expiring link is sent to all marked channels.
- AC-2: Given the link, when any recipient opens it, then OTP validation through the invitation's marked channels is still required.
- AC-3: Given a valid session, then its access is limited to that origin capture and cannot open tenant data or other assets.

Edge cases:

- EC-1 [Invalid input]: Unknown asset or participant channel → invitation is rejected.
- EC-2 [Empty / missing]: No marked verified channel → send is blocked.
- EC-3 [Limits]: Send or resend rate limit is reached → the user sees when another attempt is allowed.
- EC-4 [Permissions]: User lacks asset or invitation permission → send is denied.
- EC-5 [Concurrency]: Two invitations are created simultaneously → each remains identifiable and only valid active invitations are presented.
- EC-6 [Interruption]: Delivery fails on some channels → outcomes are recorded per channel and retry remains available.
- EC-7 [Repetition]: A send action is retried after success → it does not create duplicate business invitations.
- EC-8 [Ordering]: Link is opened before delivery status completes → valid access can still proceed once OTP is requested.
- EC-9 [State transitions]: Asset is archived or invitation expires → the link no longer grants access.
- EC-10 [Scale]: Bulk origin invitations are sent → every recipient remains isolated and delivery results remain attributable.

### US-009: Capture a described origin

**As an** Origin Contributor, **I want** to capture reference photos with descriptions, **so that** later inspections can reproduce and compare the intended view.

Acceptance criteria:

- AC-1: Given an authenticated origin session, when the contributor adds a photo, then they must select or confirm a category and provide a nonblank description.
- AC-2: Given each accepted photo, then capture source, time, GPS, precision, device context, hash, and quality signals are associated with it.
- AC-3: Given at least one described photo and every mandatory validation satisfied, when the contributor finishes, then the origin version becomes eligible for activation.
- AC-4: Given gallery media, when it is accepted, then it is visibly marked as gallery-originated.

Edge cases:

- EC-1 [Invalid input]: Unsupported media, blank description, or hostile text → the item is rejected with a corrective message.
- EC-2 [Empty / missing]: Finish is attempted with zero photos → completion is blocked.
- EC-3 [Limits]: A file or origin exceeds configured limits → the extra item is rejected without losing accepted items.
- EC-4 [Permissions]: Expired or wrong-origin token is used → access is denied.
- EC-5 [Concurrency]: Same origin session opens on two devices → conflicts are surfaced and no item is silently overwritten.
- EC-6 [Interruption]: Browser closes during capture → accepted local progress can resume while the invitation remains valid.
- EC-7 [Repetition]: Same completed upload is confirmed twice → one origin item results.
- EC-8 [Ordering]: Finish is attempted while uploads are pending → completion waits or identifies pending items.
- EC-9 [State transitions]: Session is used after completion → mutation is blocked and confirmation is shown.
- EC-10 [Scale]: Origin contains many allowed items → progress, categorization, and upload status remain understandable.

### US-010: Activate and supersede an origin version

**As a** Manager, **I want** origins to be immutable and versioned, **so that** every inspection remains tied to the reference that existed when it started.

Acceptance criteria:

- AC-1: Given a completed origin with at least one described item, when validation finishes, then it activates automatically even when nonblocking quality, gallery, or geofence flags exist.
- AC-2: Given an active origin, when a replacement is captured, then a new immutable version becomes active and the prior version remains readable.
- AC-3: Given an already created inspection, when a new origin activates, then that inspection keeps the origin version fixed at creation.

Edge cases:

- EC-1 [Invalid input]: Completed origin lacks a valid described item → activation is rejected.
- EC-2 [Empty / missing]: No prior origin exists → the first valid version becomes active.
- EC-3 [Limits]: Version history exceeds one page → versions remain paginated in activation order.
- EC-4 [Permissions]: Unauthorized user attempts manual version changes → access is denied.
- EC-5 [Concurrency]: Two origin versions complete simultaneously → one activation order is recorded without overwriting either version.
- EC-6 [Interruption]: Activation processing is interrupted → it resumes without creating a second version.
- EC-7 [Repetition]: Activation is retried → the same version remains active once.
- EC-8 [Ordering]: An inspection is created during origin activation → it either receives the previous or new complete version, never a partial version.
- EC-9 [State transitions]: Active origin is retired with dependent schedules → new occurrences are blocked until another valid origin is active.
- EC-10 [Scale]: Thousands of inspections reference older origins → activating a new version does not rewrite or slow historical access materially.

## Occurrence Management

### US-011: Configure recurring inspections

**As a** Manager, **I want** to schedule recurring inspections in the tenant timezone, **so that** expected property or service checks become occurrences automatically.

Acceptance criteria:

- AC-1: Given an active asset, participant, template, and reference when required, when the manager saves a valid recurrence and timezone, then the next occurrence is displayed.
- AC-2: Given a due recurrence, when it is processed, then at most one inspection is created for that occurrence with the then-active template and origin versions fixed.
- AC-3: Given an occurrence created successfully, then the schedule advances to the next valid time without duplicating the invitation.

Edge cases:

- EC-1 [Invalid input]: Invalid or impossible recurrence expression → save is rejected with an understandable schedule error.
- EC-2 [Empty / missing]: Participant, template, timezone, or required reference is absent → activation is blocked.
- EC-3 [Limits]: Recurrence is more frequent than the allowed minimum → save is rejected with the minimum explained.
- EC-4 [Permissions]: User lacks schedule permission for the asset → access is denied.
- EC-5 [Concurrency]: The same due occurrence is processed twice → one inspection is created.
- EC-6 [Interruption]: Processing stops after occurrence creation but before notification → notification resumes without duplicating the inspection.
- EC-7 [Repetition]: Manual technical retry runs after success → schedule and invitation remain single.
- EC-8 [Ordering]: Next occurrence is calculated before current creation commits → no time is skipped or duplicated.
- EC-9 [State transitions]: Schedule is canceled or asset archived → no later occurrence is created.
- EC-10 [Scale]: Many schedules become due together → every eligible occurrence is eventually created once and remains tenant-isolated.

### US-012: Create milestone and manual inspections

**As a** Manager, **I want** to initiate inspections from a project milestone or an authorized manual request, **so that** construction and ad hoc work do not depend on calendar recurrence.

Acceptance criteria:

- AC-1: Given a planned milestone becomes due, when an authorized user starts it, then an inspection is created for the milestone and expected stage.
- AC-2: Given a valid asset and participant, when an authorized user creates a manual inspection, then the user supplies a reason and applicable due-date settings.
- AC-3: Given either trigger, then the occurrence fixes its template, reference, capture policy, report policy, and responsible external participant.

Edge cases:

- EC-1 [Invalid input]: Unknown milestone, invalid due date, or incompatible template → creation is rejected.
- EC-2 [Empty / missing]: Manual reason or responsible participant is missing → creation is blocked.
- EC-3 [Limits]: Too many simultaneous open inspections for an asset → creation is rejected with the applicable limit.
- EC-4 [Permissions]: User lacks asset or manual-create permission → access is denied.
- EC-5 [Concurrency]: Two users start the same milestone → only one milestone occurrence results.
- EC-6 [Interruption]: Creation stops before the occurrence is complete → no participant receives a partial invitation.
- EC-7 [Repetition]: The same request is retried → one occurrence results.
- EC-8 [Ordering]: A later planned stage starts before a required prior stage decision → the template rule blocks or explicitly permits it.
- EC-9 [State transitions]: Closed project or archived asset receives a request → creation is blocked until an authorized reopening where applicable.
- EC-10 [Scale]: A manager creates occurrences across many assets → bulk status remains attributable and partial failures are explicit.

### US-013: Configure deadlines and reminders

**As a** Manager, **I want** tenant defaults with per-occurrence overrides for deadlines and reminders, **so that** participants receive appropriate notice for each inspection type.

Acceptance criteria:

- AC-1: Given tenant defaults, when a schedule, milestone, or manual occurrence omits its own settings, then those defaults apply.
- AC-2: Given valid overrides, when the occurrence is created, then its invitation, reminder times, and expiry are shown before activation.
- AC-3: Given multiple marked participant channels, when an invitation, reminder, or recapture notice is due, then the same notice is attempted on all of them and each outcome is recorded.

Edge cases:

- EC-1 [Invalid input]: Reminder occurs after expiry or dates are unparseable → save is rejected.
- EC-2 [Empty / missing]: No tenant default and no occurrence value exists → activation is blocked with the missing rule identified.
- EC-3 [Limits]: Reminder count exceeds the tenant limit → extra reminders are rejected.
- EC-4 [Permissions]: Unauthorized user changes deadlines → access is denied.
- EC-5 [Concurrency]: Deadline changes while delivery is being prepared → each notice uses one consistent saved schedule.
- EC-6 [Interruption]: Delivery provider fails → per-channel failure is visible and bounded retry remains possible.
- EC-7 [Repetition]: Reminder processing repeats → a due reminder is not intentionally sent more than once per channel except a recorded technical retry.
- EC-8 [Ordering]: A reminder becomes due before the invitation → invitation is sent first or the invalid configuration is rejected.
- EC-9 [State transitions]: Inspection is canceled, invalidated, submitted, or expired → inapplicable future notices stop.
- EC-10 [Scale]: Large reminder bursts occur → delivery status remains accurate per inspection and channel.

### US-014: Cancel or invalidate an inspection

**As a** Manager, **I want** to cancel unsubmitted inspections and invalidate submitted ones with a reason, **so that** mistakes do not contaminate operational indicators while evidence history remains intact.

Acceptance criteria:

- AC-1: Given no evidence has been submitted, when an authorized user cancels the inspection, then participant access and future notifications stop.
- AC-2: Given any evidence has been submitted, when an authorized user supplies a reason, then the inspection becomes invalidated rather than deleted.
- AC-3: Given an invalidated inspection, then it remains visible in history and audit but is excluded from valid-inspection dashboard indicators.

Edge cases:

- EC-1 [Invalid input]: Blank invalidation reason → action is rejected.
- EC-2 [Empty / missing]: Cancellation target does not exist → no data is changed and not-found is shown.
- EC-3 [Limits]: Reason exceeds its length limit → action is rejected without truncation.
- EC-4 [Permissions]: Viewer or out-of-scope user attempts action → access is denied.
- EC-5 [Concurrency]: Submission and cancellation race → submitted evidence wins finality and only invalidation remains possible.
- EC-6 [Interruption]: Action stops after state change → notifications and access eventually reflect the same final state.
- EC-7 [Repetition]: Cancel or invalidate is repeated → state remains unchanged and no duplicate audit reason is manufactured.
- EC-8 [Ordering]: Invalidation is attempted before evidence exists → the product offers cancellation instead.
- EC-9 [State transitions]: Already canceled or invalidated inspection receives a mutation → invalid transition is rejected.
- EC-10 [Scale]: Many invalid records exist → dashboard exclusions and explicit history filters remain correct.

## External Access and Capture

### US-015: Authenticate and accept required processing

**As an** External Participant, **I want** to access my inspection through link and OTP without creating an account, **so that** I can contribute with low friction and understand required data use.

Acceptance criteria:

- AC-1: Given an active invitation, when the participant requests a code, then the same OTP is sent to every marked verified channel.
- AC-2: Given a valid code within expiry and attempt limits, when it is submitted, then a short session grants access only to that inspection or origin.
- AC-3: Before capture, the participant must explicitly accept disclosed processing of photos, AI analysis, and GPS; refusal blocks the flow.
- AC-4: The disclosure identifies purpose, recipients, retention, tenant controller, and the absence of automatic participant consequences.

Edge cases:

- EC-1 [Invalid input]: Wrong or malformed OTP → attempt is rejected without disclosing the expected code.
- EC-2 [Empty / missing]: Code or required acceptance is missing → capture remains unavailable.
- EC-3 [Limits]: Attempt or resend rate limit is reached → further attempts wait until the displayed limit resets.
- EC-4 [Permissions]: Token for another inspection is used → access is denied without revealing that inspection.
- EC-5 [Concurrency]: Same code is validated simultaneously → sessions remain bounded to the same invitation and policy.
- EC-6 [Interruption]: Browser closes after validation → re-entry follows remaining session and invitation validity.
- EC-7 [Repetition]: Used or expired code is replayed → it is rejected.
- EC-8 [Ordering]: Capture route is opened before authentication or acceptance → the user is returned to the missing step.
- EC-9 [State transitions]: Canceled, completed, or expired invitation is opened → access is denied with a safe status message.
- EC-10 [Scale]: Many participants request codes together → one participant's rate or delivery status never affects another's identity or data.

### US-016: Complete guided capture requirements

**As an** External Participant, **I want** guidance based on each reference item and description, **so that** my evidence is comparable and complete.

Acceptance criteria:

- AC-1: Given a visual reference, when a requirement opens, then the participant sees the reference, description, category, and optional alignment overlay.
- AC-2: For each requirement, the participant must either capture corresponding media or provide a nonblank impossibility reason.
- AC-3: Camera capture is the default; gallery selection remains available and produces a visible risk flag.
- AC-4: The participant sees progress, pending uploads, missing requirements, and blocking validations before submission.

Edge cases:

- EC-1 [Invalid input]: Unsupported file or blank impossibility reason → requirement remains incomplete with guidance.
- EC-2 [Empty / missing]: Template has no applicable requirements → participant sees a controlled empty state and cannot fabricate a submission.
- EC-3 [Limits]: Per-item or total media limit is reached → additional media is rejected without removing accepted evidence.
- EC-4 [Permissions]: Session does not own the requirement → direct access is denied.
- EC-5 [Concurrency]: Same requirement is edited on two devices → conflicts are shown and no evidence is silently overwritten.
- EC-6 [Interruption]: Page refresh occurs → saved and local resumable progress returns.
- EC-7 [Repetition]: Same photo completion is retried → one evidence item is associated.
- EC-8 [Ordering]: Later requirement is opened before earlier work → allowed navigation preserves incomplete status and submission rules.
- EC-9 [State transitions]: Requirement is reopened for recapture → prior evidence is read-only and the requested replacement path is clear.
- EC-10 [Scale]: Template contains many requirements → grouped sections, progress, and navigation remain understandable.

### US-017: Record provenance, GPS, and geofence signals

**As an** External Participant, **I want** the product to explain and record capture context, **so that** internal reviewers can judge evidence without overstated authenticity claims.

Acceptance criteria:

- AC-1: Given GPS is required by the effective policy, when no sufficiently valid location is obtained after guided retries, then capture cannot proceed.
- AC-2: Given GPS is optional, when location is absent or imprecise, then capture proceeds with a visible flag.
- AC-3: Given valid location outside the asset radius, then capture proceeds and records distance, precision, and an out-of-geofence flag.
- AC-4: Given any evidence, then source, time, device context, original hash, and applicable quality or provenance flags are preserved and described as signals rather than proof.

Edge cases:

- EC-1 [Invalid input]: Impossible coordinates or precision values → evidence metadata is rejected or flagged as unverifiable.
- EC-2 [Empty / missing]: Required GPS remains unavailable → the participant sees why capture is blocked and how to retry.
- EC-3 [Limits]: Location attempts exceed the bounded retry period → the effective required/optional policy is applied.
- EC-4 [Permissions]: Another session requests evidence metadata → access is denied.
- EC-5 [Concurrency]: Location or asset policy changes during capture → the occurrence's fixed policy and location snapshot govern.
- EC-6 [Interruption]: Permission prompt or acquisition is interrupted → the participant can retry without losing other progress.
- EC-7 [Repetition]: Same metadata confirmation repeats → one provenance record remains associated with the evidence.
- EC-8 [Ordering]: File is uploaded before metadata completion → submission remains pending until required metadata is resolved.
- EC-9 [State transitions]: Asset coordinates change after submission → historical distance and flags do not change.
- EC-10 [Scale]: Many evidence items collect location → each item and session remain attributable without UI overload.

### US-018: Resume interrupted media uploads

**As an** External Participant, **I want** uploads to survive connection loss and page refresh, **so that** I do not repeat a long inspection.

Acceptance criteria:

- AC-1: Given an accepted file, when upload begins, then progress and local pending state are visible.
- AC-2: Given connection loss or page refresh, when the participant returns with a valid session, then incomplete uploads resume from recoverable progress.
- AC-3: Given completion, then the product verifies the stored object and metadata before marking the evidence ready.
- AC-4: Original media is never silently overwritten by retry or replacement.

Edge cases:

- EC-1 [Invalid input]: Declared size, MIME, or hash conflicts with the file → completion is rejected with retry guidance.
- EC-2 [Empty / missing]: Local pending file is no longer available → item is marked for re-selection rather than falsely complete.
- EC-3 [Limits]: File exceeds size or session quota → upload is rejected before unnecessary transfer when possible.
- EC-4 [Permissions]: Upload authority is expired or belongs to another tenant → transfer completion is denied.
- EC-5 [Concurrency]: Two resumptions upload the same file → one immutable original is accepted and duplicate completion is harmless.
- EC-6 [Interruption]: Connection drops repeatedly → recoverable parts remain reusable until session expiry.
- EC-7 [Repetition]: Completion request repeats after success → the same media item is returned.
- EC-8 [Ordering]: Submission is attempted before all uploads settle → pending items are listed and submission waits or excludes only explicitly abandoned items.
- EC-9 [State transitions]: Inspection closes while upload is pending → completion is refused and local state explains expiry.
- EC-10 [Scale]: Many large allowed files upload → individual progress remains visible and the interface remains responsive.

### US-019: Submit complete or incomplete evidence

**As an** External Participant, **I want** to submit the evidence I can provide and explain missing items, **so that** unavoidable gaps do not erase my work.

Acceptance criteria:

- AC-1: Given all requirements have evidence or an impossibility reason and uploads are ready, when the participant submits, then the submission becomes immutable.
- AC-2: Given unresolved required items, when the participant confirms incomplete submission, then it is accepted and will classify at least `ATTENTION`.
- AC-3: Given extra photos, then each requires a nonblank description and is included in analysis and report context.
- AC-4: After submission, the participant sees confirmation only and cannot browse submitted evidence or internal results.

Edge cases:

- EC-1 [Invalid input]: Extra media has a blank description or submission payload is malformed → submission is rejected with affected items listed.
- EC-2 [Empty / missing]: Submission has no evidence and no valid reasons → explicit incomplete confirmation is required and result cannot be `NORMAL`.
- EC-3 [Limits]: Extra-photo or total-evidence limit is exceeded → extras beyond the limit are rejected before final submission.
- EC-4 [Permissions]: Another invitation attempts submission → access is denied.
- EC-5 [Concurrency]: Submit is pressed twice or from two devices → one submission is created.
- EC-6 [Interruption]: Connection drops during submit → retry returns the committed outcome or safely completes it once.
- EC-7 [Repetition]: Completed submission is replayed → no duplicate analysis or evidence set is created.
- EC-8 [Ordering]: Submit precedes required sensitive-content or upload checks → the unmet checks are shown.
- EC-9 [State transitions]: Canceled, expired, or already submitted inspection is submitted → invalid transition is rejected.
- EC-10 [Scale]: Large allowed evidence set is submitted → a single observable state and stable progress are maintained.

### US-020: Prevent accidental sensitive content

**As an** External Participant, **I want** guidance and detection for faces, documents, and other identifiable personal information, **so that** prohibited content is not unintentionally included.

Acceptance criteria:

- AC-1: Before and during capture, the participant is instructed not to photograph faces, documents, or other identifiable personal information.
- AC-2: Given detected prohibited content, then submission of that item is blocked and the participant can retake it or record impossibility.
- AC-3: Given a false detection, when the participant declares a false positive, then the item may proceed with a flag and audited declaration for internal review.

Edge cases:

- EC-1 [Invalid input]: False-positive declaration is malformed or blank where a reason is required → override is rejected.
- EC-2 [Empty / missing]: Detection cannot complete → the item remains pending rather than silently passing.
- EC-3 [Limits]: Repeated scans or declarations hit abuse limits → the participant is asked to wait or use impossibility handling.
- EC-4 [Permissions]: A different session tries to override the item → access is denied.
- EC-5 [Concurrency]: Retake and false-positive declaration happen together → one final item decision is preserved.
- EC-6 [Interruption]: Detection stops mid-check → the item resumes checking and cannot be submitted meanwhile.
- EC-7 [Repetition]: Same declaration is submitted twice → one flag and audit action result.
- EC-8 [Ordering]: Final submission is attempted before detection resolves → submission is blocked with the pending item identified.
- EC-9 [State transitions]: Flagged item is later targeted for recapture → prior item and declaration remain historical.
- EC-10 [Scale]: Many photos require scanning → per-item status remains visible and the session does not mislabel unchecked items.

## Multi-Stage Inspections

### US-021: Plan standard and exceptional stages

**As a** Manager, **I want** templates to provide planned stages and allow justified additions, **so that** projects stay comparable while accommodating real-world events.

Acceptance criteria:

- AC-1: Given multi-stage enabled, when a project starts, then planned stage names, order, expected requirements, and optional dates are copied from the template version.
- AC-2: Given an active project, when an authorized user adds an exceptional stage with a reason, then it appears in the plan and audit trail.
- AC-3: Given a stage starts, then its effective reference and policies are fixed for that stage.

Edge cases:

- EC-1 [Invalid input]: Duplicate stage key, invalid date, or blank exceptional-stage reason → addition is rejected.
- EC-2 [Empty / missing]: Multi-stage template has no planned stage → project activation is blocked.
- EC-3 [Limits]: Stage count reaches the configured maximum → new exceptional stages are rejected.
- EC-4 [Permissions]: Unauthorized user adds or reorders a stage → access is denied.
- EC-5 [Concurrency]: Two users add a stage simultaneously → both receive stable distinct positions or one resolves a conflict explicitly.
- EC-6 [Interruption]: Stage addition is interrupted → either the full stage exists or no partial stage appears.
- EC-7 [Repetition]: Same exceptional-stage request is retried → one stage results.
- EC-8 [Ordering]: Stage starts before its prerequisite → template ordering rule blocks it or records the allowed exception.
- EC-9 [State transitions]: Closed project receives a new stage → addition is blocked until audited reopening.
- EC-10 [Scale]: Project has many allowed stages → timeline remains navigable and current stage is unambiguous.

### US-022: Skip, close, and reopen a staged project

**As a** Manager, **I want** controlled completion and exception handling for stages, **so that** a project can finish without hiding omitted work.

Acceptance criteria:

- AC-1: Given a planned stage will not occur, when an authorized user supplies a reason, then it is marked skipped and the consolidated result is at least `ATTENTION`.
- AC-2: Given all stage decisions are terminal, when an authorized user closes the project, then new stages and captures are blocked.
- AC-3: Given a closed project, when an authorized user reopens it with a reason, then new permitted work resumes and the reopening is audited.

Edge cases:

- EC-1 [Invalid input]: Skip or reopen reason is blank → action is rejected.
- EC-2 [Empty / missing]: Closure has undecided planned stages → the user must complete or skip them first.
- EC-3 [Limits]: Reason exceeds the allowed length → action is rejected without truncation.
- EC-4 [Permissions]: Viewer, participant, or out-of-scope user changes project state → access is denied.
- EC-5 [Concurrency]: Closure and stage submission occur together → one valid ordered outcome is applied and conflict is shown.
- EC-6 [Interruption]: Close or reopen stops mid-action → project eventually reflects one durable state and matching access.
- EC-7 [Repetition]: Skip, close, or reopen is repeated → state is idempotent and reasons are not duplicated.
- EC-8 [Ordering]: Reopen is requested for an active project → action is rejected as unnecessary.
- EC-9 [State transitions]: Invalidated project or archived asset is reopened → action is blocked.
- EC-10 [Scale]: Long project history exists → closure checks and timeline remain accurate across all stages.

## Evidence Correction

### US-023: Request directed recapture

**As a** Manager, **I want** the system or an authorized reviewer to request replacement for specific deficient evidence, **so that** quality problems can be corrected without discarding the submission.

Acceptance criteria:

- AC-1: Given objective quality or policy failure, when the system identifies an eligible item, then it can create a recapture request with the detected reason.
- AC-2: Given a submitted inspection, when an authorized internal user selects items and supplies reasons, then only those requirements reopen.
- AC-3: Given a recapture request, then prior evidence remains immutable and visible internally, a new controlled invitation is sent to all marked channels, and the final report waits for correction or deadline.

Edge cases:

- EC-1 [Invalid input]: Unknown item or blank reviewer reason → request is rejected.
- EC-2 [Empty / missing]: No items are selected → request cannot be sent.
- EC-3 [Limits]: Request exceeds the allowed number of items or cycles → the user sees the applicable limit.
- EC-4 [Permissions]: Viewer or out-of-scope user requests recapture → access is denied.
- EC-5 [Concurrency]: System and reviewer request the same item → one active requirement can carry all distinct reasons without duplicate work.
- EC-6 [Interruption]: Notification delivery fails → the request remains active and delivery status is visible for retry.
- EC-7 [Repetition]: Same request is retried → one active recapture request results.
- EC-8 [Ordering]: Recapture is requested before initial submission → the user is directed to the active capture instead.
- EC-9 [State transitions]: Canceled, invalidated, finalized-after-deadline, or closed item is targeted → invalid transition is rejected.
- EC-10 [Scale]: Many items are flagged → selection, reasons, and participant instructions remain item-specific and navigable.

### US-024: Complete or miss a recapture request

**As an** External Participant, **I want** a focused correction flow, **so that** I can replace only the evidence that needs attention before the deadline.

Acceptance criteria:

- AC-1: Given a valid recapture link and OTP, when the participant enters, then only requested requirements and their reasons are available.
- AC-2: Given corrected media, when the participant resubmits, then the new evidence is added without overwriting the original.
- AC-3: Given all requested corrections complete, then analysis and final reporting continue.
- AC-4: Given deadline expiry with correction pending, then the inspection finalizes with available evidence, identifies uncorrected items, and classifies at least `ATTENTION`.

Edge cases:

- EC-1 [Invalid input]: Replacement fails media or description validation → affected requirement stays open.
- EC-2 [Empty / missing]: Participant submits no correction → request remains pending until expiry.
- EC-3 [Limits]: Replacement exceeds item limits → it is rejected without removing prior evidence.
- EC-4 [Permissions]: Initial or unrelated invitation token opens recapture → access is denied.
- EC-5 [Concurrency]: Deadline and resubmission occur together → one deterministic cutoff decides inclusion and is visible internally.
- EC-6 [Interruption]: Upload or session is interrupted → resumable progress follows remaining recapture validity.
- EC-7 [Repetition]: Corrected submission repeats → one correction set is accepted.
- EC-8 [Ordering]: Final resubmit is attempted before replacement uploads complete → pending items are shown.
- EC-9 [State transitions]: Link opens after completion or expiry → mutation is blocked and safe status is shown.
- EC-10 [Scale]: Many targeted items exist → the participant sees focused progress and no unrelated evidence.

## Analysis and Reports

### US-025: Receive structured evidence findings

**As a** Manager, **I want** structured comparisons with evidence and confidence, **so that** I can triage observed change without treating AI as the final decision-maker.

Acceptance criteria:

- AC-1: Given analyzable reference and current evidence, when analysis completes, then each finding includes category, title, description, severity, confidence, referenced media, evidence quality, observations, and recommended action.
- AC-2: Given an AI response outside the required structure, then it is rejected and retried only within the configured attempt policy.
- AC-3: Given permanent failure or insufficient evidence, then the affected comparison becomes inconclusive and remains visible in the report.
- AC-4: Findings use neutral observed-change language and do not assign blame, cost, legal responsibility, or automatic action.

Edge cases:

- EC-1 [Invalid input]: Severity, confidence, or evidence references are invalid → response is rejected rather than loosely parsed.
- EC-2 [Empty / missing]: Model returns no findings → it is accepted only when the structure explicitly represents no relevant change.
- EC-3 [Limits]: Response or evidence count exceeds analysis limits → affected comparison becomes a visible bounded failure or is split without losing traceability.
- EC-4 [Permissions]: Unauthorized user requests or views analysis → access is denied.
- EC-5 [Concurrency]: Duplicate analysis starts for the same evidence/profile → one accepted result version governs the report.
- EC-6 [Interruption]: Provider timeout or worker restart occurs → bounded retry resumes and eventually reaches success or inconclusive.
- EC-7 [Repetition]: Completed request is replayed → no duplicate finding set affects classification.
- EC-8 [Ordering]: Analysis is requested before media verification or submission terminality → it waits for prerequisites.
- EC-9 [State transitions]: New recapture evidence arrives → superseding analysis is created without editing prior results.
- EC-10 [Scale]: Many comparisons run for one inspection or tenant → each outcome remains attributable and reporting eventually reaches a terminal state.

### US-026: Apply deterministic inspection classification

**As a** Manager, **I want** reports classified by explicit rules, **so that** priority does not depend on generative wording.

Acceptance criteria:

- AC-1: If any accepted finding is `CRITICAL`, then the inspection classification is `CRITICAL`.
- AC-2: If no critical finding exists but any noncritical change, missing evidence, gallery/geofence/quality flag, skipped stage, uncorrected request, or inconclusive analysis exists, then classification is `ATTENTION`.
- AC-3: Classification is `NORMAL` only when all required evidence is analyzed, no relevant change exists, and no flag or inconclusive outcome exists.
- AC-4: Thresholds and classification rules are fixed by the inspection's analysis-profile version and are not inferred from prompt prose.

Edge cases:

- EC-1 [Invalid input]: Unknown severity or confidence outside range → result cannot enter classification.
- EC-2 [Empty / missing]: Inspection has no analyzable evidence → classification is at least `ATTENTION`, never `NORMAL`.
- EC-3 [Limits]: Very large finding set exists → every accepted critical and flag still affects the outcome.
- EC-4 [Permissions]: Unauthorized user tries to change classification → no manual mutation is allowed.
- EC-5 [Concurrency]: Analysis results finish simultaneously → classification updates from the full committed terminal set.
- EC-6 [Interruption]: Classification process stops → retry produces the same deterministic result.
- EC-7 [Repetition]: Same findings are processed again → classification remains unchanged.
- EC-8 [Ordering]: Partial results arrive before terminality → final classification is not published prematurely.
- EC-9 [State transitions]: Recapture supersedes evidence → a new report classification is calculated without editing the old version.
- EC-10 [Scale]: Portfolio contains many reports → summary counts equal the classifications of valid latest report versions.

### US-027: Review an immutable inspection report

**As a** Viewer, **I want** an internal report with comparison, findings, flags, and inconclusive items, **so that** I can understand what the inspection evidence supports.

Acceptance criteria:

- AC-1: Given all comparisons reach terminal states, then one immutable HTML/PDF report version is generated even when some are inconclusive.
- AC-2: The report shows classification, reference/current evidence side by side, descriptions, provenance flags, findings, confidence, and recommended actions.
- AC-3: The report identifies itself as internal advisory triage and is not available through the external participant session.
- AC-4: An authorized user can download the PDF and relate it to its inspection, template, reference, analysis profile, and version.

Edge cases:

- EC-1 [Invalid input]: Report ID or requested format is invalid → request is rejected without leaking another report.
- EC-2 [Empty / missing]: No finding exists → report still explains evidence coverage and why classification is `NORMAL` or `ATTENTION`.
- EC-3 [Limits]: Evidence or text exceeds page layout limits → output paginates without dropping required content.
- EC-4 [Permissions]: External, cross-tenant, or out-of-scope user opens report or media URL → access is denied.
- EC-5 [Concurrency]: Two generation attempts run → one logical report version is retained.
- EC-6 [Interruption]: PDF generation fails → status remains visible and bounded retry leads to PDF or a recorded failure without changing analysis.
- EC-7 [Repetition]: Download or generation is retried → content and report identity remain stable.
- EC-8 [Ordering]: Report is requested before comparisons are terminal → pending status is shown.
- EC-9 [State transitions]: Inspection is invalidated later → report remains historical and is labeled invalidated.
- EC-10 [Scale]: Report contains many evidence pairs → navigation and rendering remain usable and complete.

### US-028: Review consolidated or historical stage reports

**As a** Manager, **I want** the template-selected report presentation for staged inspections, **so that** I can see either one evolving project view or a separate report per stage.

Acceptance criteria:

- AC-1: Given consolidated mode, when each stage finalizes, then a new immutable consolidated version becomes available and the latest is the default view.
- AC-2: Given historical mode, when each stage finalizes, then it receives its own immutable report in a chronological project history.
- AC-3: In consolidated mode, the headline classification reflects the newest stage and pendencies still observable, while prior classifications remain visible on the timeline.
- AC-4: Skipped stages, exceptional stages, closure, and reopening remain visible in either mode.

Edge cases:

- EC-1 [Invalid input]: Unknown report mode → template cannot publish.
- EC-2 [Empty / missing]: No stage has finalized → the project shows planned progress without pretending a report exists.
- EC-3 [Limits]: Version or stage history exceeds one page → stable chronological pagination is used.
- EC-4 [Permissions]: User lacks project scope → no report or timeline detail is exposed.
- EC-5 [Concurrency]: Two stages finalize together → version order is deterministic and neither stage disappears.
- EC-6 [Interruption]: Consolidation stops → prior latest version stays available until the new version completes.
- EC-7 [Repetition]: Same stage-final event repeats → no duplicate visible report version results.
- EC-8 [Ordering]: A late earlier-stage result arrives → history uses actual stage sequence and records result timing without rewriting prior versions.
- EC-9 [State transitions]: Project reopens after closure → new versions append and closure history remains visible.
- EC-10 [Scale]: Long projects have many report versions → latest state is fast to find and full history stays navigable.

## Dashboard and Notifications

### US-029: Triage the inspection portfolio

**As a** Manager, **I want** a scoped dashboard ordered by risk with useful filters, **so that** I can focus internal review on the most important inspections.

Acceptance criteria:

- AC-1: The dashboard shows totals and trends by `CRITICAL`, `ATTENTION`, and `NORMAL` for the user's authorized scope.
- AC-2: The user can filter by asset, business unit, period, status, segment, classification, and flags.
- AC-3: Lists prioritize critical, attention, then normal latest valid reports and open the corresponding evidence comparison.
- AC-4: Invalidated inspections remain available through explicit history filters but do not count in valid indicators.

Edge cases:

- EC-1 [Invalid input]: Invalid filter combination or date range → query is rejected with corrective guidance.
- EC-2 [Empty / missing]: No authorized data or no match → an explanatory empty state is shown.
- EC-3 [Limits]: Results exceed one page → stable pagination and filter state are preserved.
- EC-4 [Permissions]: Direct link points outside user scope → access is denied and counts never reveal it.
- EC-5 [Concurrency]: New report arrives while viewing → existing list is stable until refresh and updated totals are internally consistent.
- EC-6 [Interruption]: Dashboard request fails → prior displayed data is labeled stale and retry is offered.
- EC-7 [Repetition]: Refresh or repeated navigation → no duplicate rows or inflated counts appear.
- EC-8 [Ordering]: Equal-priority reports exist → deterministic secondary ordering is used.
- EC-9 [State transitions]: Inspection becomes invalidated or reclassified → it moves to the correct view and counts update.
- EC-10 [Scale]: Portfolio reaches 100× typical volume → filters, counts, and ordering remain usable and tenant-isolated.

### US-030: Receive critical-finding alerts

**As a** Manager, **I want** immediate configurable alerts for new critical findings, **so that** urgent internal review does not depend on opening the dashboard.

Acceptance criteria:

- AC-1: Given selected internal recipients and channels, when a new report version first contains a `CRITICAL` finding, then an alert is sent and the dashboard highlights it.
- AC-2: The alert identifies tenant-safe inspection context and links only authorized users to internal details.
- AC-3: External participants are never automatic recipients of finding or classification alerts.
- AC-4: Delivery outcomes and retries are visible to authorized internal users.

Edge cases:

- EC-1 [Invalid input]: Invalid recipient or channel → configuration is rejected.
- EC-2 [Empty / missing]: No internal recipient is configured → dashboard highlight remains and configuration warning is visible.
- EC-3 [Limits]: Alert rate reaches configured protection limits → alerts are safely grouped or queued without losing critical inspection identity.
- EC-4 [Permissions]: Recipient later loses scope → link access is denied even if message was delivered.
- EC-5 [Concurrency]: Multiple critical findings arrive together → notification grouping does not hide any affected inspection.
- EC-6 [Interruption]: Provider delivery fails → bounded retry and final failure are recorded.
- EC-7 [Repetition]: Same analysis event is replayed → no duplicate first-critical alert is sent.
- EC-8 [Ordering]: Critical alert precedes report availability → link shows pending until report is ready rather than an error leak.
- EC-9 [State transitions]: Later stage is no longer critical → old alert remains historical and current dashboard state updates.
- EC-10 [Scale]: Many tenants generate alerts → recipients and content remain isolated per tenant.

## Privacy and Retention

### US-031: Configure and enforce retention

**As a** Tenant Administrator, **I want** retention periods by data class with explicit defaults, **so that** inspection data expires predictably and deletions remain auditable.

Acceptance criteria:

- AC-1: The administrator can configure retention separately for evidence/reports, operational records/location, and invitation/OTP secrets within allowed policy bounds.
- AC-2: Without overrides, evidence, inspections, and reports remain for five years after occupancy, service relationship, or project closure; operational records and separately usable location remain for one year; secrets remain only until expiry.
- AC-3: A deletion request before configured expiry is recorded and refused with the active retention restriction explained.
- AC-4: At expiry, eligible originals, derivatives, reports, and linked personal data are deleted or irreversibly de-identified as applicable, and the outcome is audited.

Edge cases:

- EC-1 [Invalid input]: Negative, malformed, or disallowed retention period → save is rejected.
- EC-2 [Empty / missing]: Tenant has no custom policy → all platform defaults are displayed and applied.
- EC-3 [Limits]: Deletion batch exceeds processing capacity → it continues in bounded batches with status, never partial silent completion.
- EC-4 [Permissions]: Non-administrator changes policy or requests tenant-wide deletion → access is denied.
- EC-5 [Concurrency]: Policy changes while data reaches expiry → each record follows one determinable effective policy and audit explanation.
- EC-6 [Interruption]: Deletion stops after partial derivative cleanup → retry completes the same eligible scope and status remains visible.
- EC-7 [Repetition]: Deletion processing repeats → already deleted data remains absent and audit does not imply duplicate evidence.
- EC-8 [Ordering]: Relationship closure is recorded late → retention start is based on the authoritative closure and recalculated transparently.
- EC-9 [State transitions]: Closed project reopens → future retention eligibility is recalculated without restoring already lawfully deleted data.
- EC-10 [Scale]: Large tenant reaches mass expiry → deletion remains tenant-isolated, observable, and complete across data classes.

## Segment Journeys

### US-032: Complete a periodic property inspection

**As a** Tenant Participant, **I want** to follow the active property origin item by item, **so that** the property manager receives comparable periodic condition evidence.

Acceptance criteria:

- AC-1: A property inspection fixes the active origin version at occurrence creation and shows each origin image and description as a capture requirement.
- AC-2: The participant captures a corresponding image or explains impossibility for every origin item and may add described extra images.
- AC-3: The resulting report compares origin and current evidence, exposes condition changes and flags internally, and remains unavailable to the participant.
- AC-4: Activating a new origin affects future occurrences only.

Edge cases:

- EC-1 [Invalid input]: Property-specific required data or current evidence is invalid → affected step is blocked with guidance.
- EC-2 [Empty / missing]: Property has no active origin → recurring or manual inspection creation is blocked.
- EC-3 [Limits]: Property origin exceeds capture session limits → template or origin activation must be corrected before invitation.
- EC-4 [Permissions]: Participant link targets another property or internal history → access is denied.
- EC-5 [Concurrency]: Origin changes while capture is active → fixed origin remains the reference.
- EC-6 [Interruption]: Tenant loses connectivity → resumable capture preserves progress within invitation validity.
- EC-7 [Repetition]: Schedule or submission is replayed → one property occurrence and one submission result.
- EC-8 [Ordering]: Participant attempts submission before every item has evidence or reason → incomplete choice is made explicit.
- EC-9 [State transitions]: Occupancy closes before an unsubmitted occurrence → authorized staff decide cancellation; participant access reflects that state.
- EC-10 [Scale]: Large property has many rooms/items → grouped categories and progress remain usable.

### US-033: Report progress and nonconformities

**As a** Construction Responsible Party, **I want** to capture planned work stages and defects, **so that** the tenant can compare observed progress with expectations and retain quality history.

Acceptance criteria:

- AC-1: Each construction stage displays its planned stage reference, requirements, quality checklist, and applicable prior evidence.
- AC-2: The participant records observed progress and evidence for nonconformities without assigning contractual fault or cost.
- AC-3: Authorized users can add exceptional stages, skip planned stages with reason, and close or reopen the project under the shared staged lifecycle.
- AC-4: The selected consolidated or historical report mode shows progress, current pendencies, flags, and prior stage classifications.

Edge cases:

- EC-1 [Invalid input]: Progress value, checklist answer, or required evidence is invalid → affected requirement remains incomplete.
- EC-2 [Empty / missing]: Planned stage lacks an expected reference or checklist → it cannot start until corrected or explicitly configured as reference-free.
- EC-3 [Limits]: Project or stage evidence limit is reached → additional items are rejected without losing accepted history.
- EC-4 [Permissions]: Responsible party attempts internal project administration or another project → access is denied.
- EC-5 [Concurrency]: Exceptional stage and planned stage start together → timeline retains a deterministic order.
- EC-6 [Interruption]: Field capture stops → resumable progress remains scoped to the stage.
- EC-7 [Repetition]: Stage completion event repeats → one stage result and report version result.
- EC-8 [Ordering]: Later construction stage starts before a required predecessor → configured ordering rule is enforced.
- EC-9 [State transitions]: Closed project receives participant capture → access is denied until authorized reopening and a valid invitation.
- EC-10 [Scale]: Long construction project contains many stages and findings → latest status and full history remain navigable.

### US-034: Demonstrate cleaning execution and quality

**As a** Cleaning Executor or Supervisor, **I want** to capture an immutable origin and one or more later cleaning stages, **so that** execution and quality can be demonstrated without measuring individual productivity.

Acceptance criteria:

- AC-1: The first configured stage establishes immutable origin photos and descriptions for the cleaning scope.
- AC-2: Each later stage captures comparable after evidence and completes the template quality checklist.
- AC-3: The template-selected report mode produces either a latest consolidated view with immutable versions or a chronological report per stage.
- AC-4: The product reports observed execution, quality gaps, skipped items, and flags without scoring employee productivity, hours, or individual performance.

Edge cases:

- EC-1 [Invalid input]: Checklist answer, origin description, or stage evidence is invalid → stage cannot complete until corrected or marked impossible where allowed.
- EC-2 [Empty / missing]: No valid origin exists for an origin-based template → later comparison stage cannot start.
- EC-3 [Limits]: Cleaning scope exceeds template evidence or stage limits → additional content is rejected with the limit explained.
- EC-4 [Permissions]: Cleaning participant accesses another service or internal report → access is denied.
- EC-5 [Concurrency]: Origin replacement and later stage creation race → stage fixes one complete origin version.
- EC-6 [Interruption]: Before or after capture loses connectivity → progress resumes within the active session.
- EC-7 [Repetition]: Same cleaning stage is submitted twice → one stage result is accepted.
- EC-8 [Ordering]: After stage is attempted before required origin completion → start is blocked.
- EC-9 [State transitions]: Cleaning project is closed → new stage is blocked until authorized audited reopening.
- EC-10 [Scale]: Cleaning operation spans many locations or checklist items → asset filters, grouped requirements, and report navigation remain usable.
