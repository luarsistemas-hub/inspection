# User Stories: Three Frontend Experience

Canonical behavior catalog for the separation of Inspection into Admin,
Dashboard, and Capture products. Companion to `_prd.md`; consumed by
`_techspec.md` (component mapping) and `_tests.md` (coverage matrix).

## Personas

- **Tenant Administrator** — configures one tenant, its organization, people, permissions, catalogs, assets, policies, and governance through Admin.
- **Internal Manager** — plans and operates inspections, prioritizes review work, publishes results when required, and acts within assigned business-unit or resource scope through Dashboard.
- **Internal Employee** — performs permitted operational and review work for assigned assets, projects, or inspections through Dashboard.
- **Internal Viewer** — reads authorized progress, evidence, and reports without changing state through Dashboard.
- **Customer or Property Owner** — follows explicitly shared assets, projects, inspections, evidence history, and published reports through a permissioned Dashboard account.
- **Origin Contributor** — uses an invitation to register the immutable reference evidence for an asset through Capture without requiring a Dashboard account.
- **Inspection Participant** — uses an invitation to complete guided inspection evidence through Capture.
- **Recapture Participant** — uses a new invitation to replace only selected deficient evidence while preserving the original submission.

## Story Index

| ID | Feature Area | Persona | Story |
|---|---|---|---|
| US-001 | Product Access | Tenant Administrator | Enter Admin and move safely to Dashboard |
| US-002 | Admin Configuration | Tenant Administrator | Configure tenant and business units |
| US-003 | Admin Access | Tenant Administrator | Invite users and grant hierarchical access |
| US-004 | Admin Catalog | Tenant Administrator | Manage participants, catalogs, templates, and assets |
| US-005 | Admin Governance | Tenant Administrator | Configure publication, notification, retention, and audit governance |
| US-006 | Dashboard Home | Internal Manager | See a role-adaptive operational home |
| US-007 | Dashboard Operations | Internal Manager | Perform permitted inspection and project actions |
| US-008 | Dashboard Review | Internal Manager | Triage findings, reports, and recapture needs |
| US-009 | Dashboard Read Access | Internal Viewer | Review authorized data without mutation controls |
| US-010 | Customer Timeline | Customer or Property Owner | Follow shared assets and projects over time |
| US-011 | Customer Evidence | Customer or Property Owner | Inspect published reports and evidence in simple or advanced mode |
| US-012 | Customer Notifications | Customer or Property Owner | Receive safe progress and publication notifications |
| US-013 | Capture Access | Inspection Participant | Enter one scoped responsibility with link and OTP |
| US-014 | Origin Capture | Origin Contributor | Register guided origin evidence |
| US-015 | Inspection Capture | Inspection Participant | Complete guided inspection evidence |
| US-016 | Directed Recapture | Recapture Participant | Replace only requested evidence |
| US-017 | Capture Resilience | Inspection Participant | Resume interrupted capture and uploads |
| US-018 | Capture Finality | Inspection Participant | Submit immutable evidence and receive confirmation |

## Product Access

### US-001: Enter Admin and move safely to Dashboard

**As a** Tenant Administrator, **I want** one sign-in identity with independent product entitlements, **so that** I can configure the tenant in Admin and operate it in Dashboard without confusing access between them.

Acceptance criteria:

- AC-1: Given an administrator entitled to both products, when sign-in succeeds, then Admin opens in the administrator's tenant and offers an identifiable transition to Dashboard.
- AC-2: Given an existing authenticated identity, when the user opens Dashboard, then the product reuses sign-in continuity while independently confirming Dashboard permission.
- AC-3: Given a user entitled to only one product, when they try to open the other product, then they see a denial with a safe route back to an allowed destination.
- AC-4: Given a product transition, when it completes, then the user can always identify which product and tenant they are using.

Edge cases:

- EC-1 [Invalid input]: A malformed return destination is supplied → it is ignored and the user lands on a safe product home.
- EC-2 [Empty / missing]: The user has an identity but no product entitlement → no tenant data appears and access-request guidance is shown.
- EC-3 [Limits]: The user belongs to many tenants → tenant choices remain searchable and do not grant implicit cross-tenant access.
- EC-4 [Permissions]: A Dashboard-only customer opens Admin → Admin reveals no configuration data and returns a clear denial.
- EC-5 [Concurrency]: An entitlement is revoked while two product tabs are open → both products deny the next protected interaction.
- EC-6 [Interruption]: Sign-in is interrupted during a product transition → the user can restart without landing in the wrong product.
- EC-7 [Repetition]: The transition link is opened repeatedly → it produces one stable destination without duplicate side effects.
- EC-8 [Ordering]: A deep link is opened before authentication → sign-in returns the user only to that authorized context.
- EC-9 [State transitions]: A tenant becomes inactive during a session → both products stop exposing tenant content and explain the state.
- EC-10 [Scale]: The identity has many scoped memberships → product entry remains understandable and only current effective memberships are displayed.

## Admin Configuration

### US-002: Configure tenant and business units

**As a** Tenant Administrator, **I want** to maintain tenant identity, defaults, and business-unit structure in Admin, **so that** all later access and inspection work uses the correct organizational context.

Acceptance criteria:

- AC-1: Given an active tenant, when the administrator opens Admin, then current tenant identity, language, timezone, defaults, and business units are visible as configuration.
- AC-2: Given valid changes, when the administrator saves them, then future work uses the new values while historical inspection snapshots remain unchanged.
- AC-3: Given a business unit with dependent resources, when its lifecycle changes, then Admin explains the impact before the change and preserves historical visibility.
- AC-4: Given inherited defaults, when the administrator views a resource, then platform, tenant, and resource-specific values are distinguishable.

Edge cases:

- EC-1 [Invalid input]: Invalid timezone, language, code, or name → the affected field is rejected with corrective guidance.
- EC-2 [Empty / missing]: A required tenant identity or initial business unit is missing → activation or save is blocked with the missing requirement identified.
- EC-3 [Limits]: Names, codes, or unit counts exceed supported limits → Admin prevents the excess and states the applicable limit.
- EC-4 [Permissions]: A non-administrator reaches a configuration link → no configuration is disclosed.
- EC-5 [Concurrency]: Two administrators edit the same setting → the stale save is rejected and current values can be reloaded.
- EC-6 [Interruption]: Navigation or connection loss occurs with unsaved changes → Admin warns before discarding recoverable input.
- EC-7 [Repetition]: The same successful save is retried → the effective configuration remains singular and unchanged.
- EC-8 [Ordering]: A dependent default is selected before its prerequisite exists → Admin blocks the selection and explains the prerequisite.
- EC-9 [State transitions]: An archived unit is selected for new work → it remains historical but is unavailable for new assignments.
- EC-10 [Scale]: A tenant has many units → search, filtering, and pagination preserve the administrator's place and scope.

### US-003: Invite users and grant hierarchical access

**As a** Tenant Administrator, **I want** to invite internal and customer users and assign explicit hierarchical access, **so that** everyone sees and does only what their role permits.

Acceptance criteria:

- AC-1: Given a known recipient, when the administrator invites them, then the invitation identifies whether access is internal or customer-facing and names its intended scope.
- AC-2: Given a user, when the administrator grants business-unit, asset, project, or inspection access, then Admin previews the effective descendants before confirmation.
- AC-3: Given a customer role, when access becomes active, then the user can enter Dashboard but cannot enter Admin or use internal-only actions.
- AC-4: Given an active grant, when the administrator revokes it, then access stops immediately across future views and actions.
- AC-5: Given multiple grants, when effective access is reviewed, then the product explains which direct or inherited grant permits each resource.

Edge cases:

- EC-1 [Invalid input]: Invalid recipient, role, tenant, or resource → the invitation or grant is rejected without partial access.
- EC-2 [Empty / missing]: No scope is selected → the invitation cannot create a data-bearing Dashboard membership.
- EC-3 [Limits]: Invitation or grant volume exceeds a tenant limit → the excess is rejected with actionable guidance and existing grants remain intact.
- EC-4 [Permissions]: An administrator tries to grant beyond their own tenant → the target is undiscoverable and the action is denied.
- EC-5 [Concurrency]: Access is granted and revoked concurrently → the final effective state is explicit and never broader than the latest authorized decision.
- EC-6 [Interruption]: Invitation delivery fails → the pending invitation remains visible and can be resent without creating duplicate memberships.
- EC-7 [Repetition]: The same invitation is accepted twice → one membership exists and the second attempt reports the existing result.
- EC-8 [Ordering]: A grant targets a resource before the membership is accepted → the scope is preserved but no data is available before activation.
- EC-9 [State transitions]: A granted resource is archived or moved → historical access follows recorded authorization, while any changed effective access is explained before confirmation.
- EC-10 [Scale]: A user has many direct and inherited grants → Admin provides searchable effective-access inspection without flattening tenant boundaries.

## Admin Catalog

### US-004: Manage participants, catalogs, templates, and assets

**As a** Tenant Administrator, **I want** structural registrations in Admin, **so that** Dashboard operations use governed participants, definitions, templates, profiles, and inspectable assets.

Acceptance criteria:

- AC-1: Given tenant scope, when the administrator registers or updates participants and verified delivery channels, then future invitations use only active selected channels.
- AC-2: Given valid catalog content, when the administrator publishes a segment, template, or analysis profile, then the published version is immutable and available to future work.
- AC-3: Given an asset, when the administrator assigns its unit, segment data, template, location, participants, and policy overrides, then effective configuration is visible before use.
- AC-4: Given later configuration changes, when historical work is viewed, then it continues to display its original fixed versions.

Edge cases:

- EC-1 [Invalid input]: Invalid contact, catalog definition, template reference, asset data, or location → publication or save is rejected with field-level guidance.
- EC-2 [Empty / missing]: Required participant, template, or asset context is absent → the resource cannot become active.
- EC-3 [Limits]: Catalog, template, asset, participant, or channel limits are reached → the user sees the exact blocked operation and no partial record.
- EC-4 [Permissions]: A user without Admin entitlement follows a catalog link → no administrative data or mutation is exposed.
- EC-5 [Concurrency]: Two edits target the same mutable resource → stale input is rejected while immutable published versions remain unchanged.
- EC-6 [Interruption]: Publication or asset save is interrupted → Admin shows whether nothing changed or a complete version was created.
- EC-7 [Repetition]: A publication request is retried → it resolves to one version rather than duplicating effective configuration.
- EC-8 [Ordering]: Activation is attempted before validation and publication → Admin blocks it and identifies missing steps.
- EC-9 [State transitions]: An inactive participant, channel, template, or asset is selected for new work → the selection is unavailable but history remains visible.
- EC-10 [Scale]: Catalogs and assets grow to large volumes → search, filters, and pagination remain scoped and stable.

## Admin Governance

### US-005: Configure publication, notification, retention, and audit governance

**As a** Tenant Administrator, **I want** governance policies in Admin, **so that** result release, notifications, retention, and accountability follow my tenant's rules.

Acceptance criteria:

- AC-1: Given no explicit publication policy, when a report becomes final, then it remains internal until an authorized user publishes it manually.
- AC-2: Given automatic publication is enabled, when any report becomes final, then it becomes customer-visible even if critical or inconclusive and retains clear advisory labels.
- AC-3: Given configured customer channels, when a relevant progress or publication event occurs, then safe notifications use those channels and appear in Dashboard.
- AC-4: Given retention and privacy rules, when the administrator reviews them, then effective periods, restrictions, and pending requests are clear.
- AC-5: Given an administrative or sensitive operational action, when it completes or fails, then the audit view records actor, tenant, target, time, reason when required, and outcome.

Edge cases:

- EC-1 [Invalid input]: Unsupported policy or retention values → save is blocked with the accepted range or choice.
- EC-2 [Empty / missing]: Publication policy is absent → manual publication remains the effective default.
- EC-3 [Limits]: Notification recipients or audit results exceed one page → pagination preserves ordering and scope.
- EC-4 [Permissions]: A non-administrator attempts policy changes → values remain visible only where permitted and no change occurs.
- EC-5 [Concurrency]: Two administrators change one policy → stale confirmation is rejected and the effective policy is shown.
- EC-6 [Interruption]: A policy save loses connectivity → Admin does not claim success until the effective value is confirmed.
- EC-7 [Repetition]: The same policy update or notification event repeats → configuration stays singular and customers do not receive duplicate notices for one event.
- EC-8 [Ordering]: Automatic publication is enabled after reports are already final → existing reports remain unchanged unless explicitly published; future finalizations follow the new policy.
- EC-9 [State transitions]: Automatic publication is disabled while a report finalizes → the policy effective at finalization determines visibility and is auditable.
- EC-10 [Scale]: Audit and notification history becomes large → filters and stable chronological ordering remain usable.

## Dashboard Home

### US-006: See a role-adaptive operational home

**As an** Internal Manager, **I want** Dashboard to prioritize the work relevant to my role and scope, **so that** I can act on urgent inspections without navigating administrative configuration.

Acceptance criteria:

- AC-1: Given an internal manager, when Dashboard opens, then critical, attention, overdue, pending, and recently changed work is prioritized within scope.
- AC-2: Given an employee, viewer, or customer, when Dashboard opens, then the home changes to the resources and actions allowed for that role rather than exposing disabled administrative concepts.
- AC-3: Given selected filters, when the user drills into a result, then the destination preserves tenant, unit, asset, project, period, status, classification, and flag context where applicable.
- AC-4: Given no matching work, when the home loads, then it explains the empty state and offers only permitted next actions.

Edge cases:

- EC-1 [Invalid input]: An invalid filter or date range is supplied → it is rejected or safely reset without broadening scope.
- EC-2 [Empty / missing]: No inspections or projects are visible → a role-specific empty state appears instead of zero-value noise.
- EC-3 [Limits]: Result counts exceed one page → stable pagination or progressive loading preserves priority order.
- EC-4 [Permissions]: A user deep-links to an out-of-scope card → existence and data remain hidden.
- EC-5 [Concurrency]: Classification changes while the home is open → refresh identifies the change without applying an action to stale state.
- EC-6 [Interruption]: Loading fails partway → already displayed data is marked stale and recovery is explicit.
- EC-7 [Repetition]: Refresh or back navigation repeats the same query → filters and selected context remain stable.
- EC-8 [Ordering]: Lower-priority work has an earlier timestamp → configured risk ordering still leads, with time used consistently within a priority.
- EC-9 [State transitions]: A visible inspection becomes canceled or invalidated → its card updates and leaves valid outcome totals as required.
- EC-10 [Scale]: A manager sees 100 times typical portfolio volume → summaries, filters, and drill-down remain usable without cross-scope leakage.

## Dashboard Operations

### US-007: Perform permitted inspection and project actions

**As an** Internal Manager, **I want** operational actions in Dashboard, **so that** I can plan and manage inspections without entering Admin.

Acceptance criteria:

- AC-1: Given adequate permission, when the user creates or cancels an inspection, manages a schedule or stage, closes or reopens a project, or requests recapture, then Dashboard shows the resulting state and audit-relevant reason.
- AC-2: Given read-only or customer access, when the same resource opens, then mutation controls are absent and direct attempts are denied.
- AC-3: Given an action with downstream processing, when it is accepted, then Dashboard distinguishes accepted, pending, completed, and failed outcomes.
- AC-4: Given a destructive or history-affecting action, when confirmation is requested, then the user sees the target, consequence, and required reason before committing.

Edge cases:

- EC-1 [Invalid input]: Invalid dates, recurrence, transition, target, or reason → no state changes and the correction is explained.
- EC-2 [Empty / missing]: A required asset, participant, template, stage, or reason is missing → submission remains blocked.
- EC-3 [Limits]: A bulk or recurring operation exceeds supported bounds → excess work is rejected with item-level outcomes.
- EC-4 [Permissions]: A customer, viewer, or out-of-scope internal user invokes an operation directly → it is denied without disclosing hidden state.
- EC-5 [Concurrency]: Another actor changes the resource first → the stale action is rejected and current state is offered.
- EC-6 [Interruption]: The connection drops after confirmation → Dashboard reconciles whether the action succeeded before offering retry.
- EC-7 [Repetition]: The same action is submitted twice → one business outcome occurs and the prior result is returned.
- EC-8 [Ordering]: A later lifecycle step is attempted before its prerequisite → the action is unavailable and the required prior state is named.
- EC-9 [State transitions]: An operation targets a closed, archived, canceled, or invalidated resource → only transitions explicitly allowed by policy remain available.
- EC-10 [Scale]: Many operations are visible across units → filters and grouped outcomes remain attributable to each resource.

## Dashboard Review

### US-008: Triage findings, reports, and recapture needs

**As an** Internal Manager, **I want** to review comparisons, findings, flags, report history, and deficient evidence, **so that** I can prioritize action and publish trustworthy context.

Acceptance criteria:

- AC-1: Given completed analysis, when the reviewer opens an inspection, then reference and current evidence, findings, confidence, quality, flags, and advisory classification are connected visibly.
- AC-2: Given incomplete, critical, or inconclusive analysis, when it is reviewed, then uncertainty and pending work are explicit and never presented as fact or fault.
- AC-3: Given deficient evidence, when an authorized reviewer requests recapture, then selected items and reasons are visible before confirmation.
- AC-4: Given manual publication mode and a final report, when an authorized reviewer publishes it, then permitted customers see that exact immutable version.
- AC-5: Given report history, when a user selects a prior version, then the product distinguishes it from the current version and preserves its original context.

Edge cases:

- EC-1 [Invalid input]: Unknown evidence, blank recapture reason, or nonfinal report publication → the action is rejected with a specific explanation.
- EC-2 [Empty / missing]: No finding or comparison is available → the report explains absence instead of implying a normal result.
- EC-3 [Limits]: Evidence or report history is large → navigation remains grouped by requirement, stage, and version.
- EC-4 [Permissions]: A reviewer outside scope opens a comparison or download → resource existence and signed access remain hidden.
- EC-5 [Concurrency]: Two reviewers publish or request the same correction → one effective outcome exists and both see current state.
- EC-6 [Interruption]: Report or media loading fails → surrounding metadata remains safe and unavailable content is clearly marked.
- EC-7 [Repetition]: Publication, download authorization, or recapture request is retried → no duplicate report, access grant, or correction responsibility is created.
- EC-8 [Ordering]: Publication is attempted before finalization → Dashboard preserves internal state and explains the prerequisite.
- EC-9 [State transitions]: An inspection is invalidated after publication → history remains visible to authorized users and current status is unmistakable.
- EC-10 [Scale]: Hundreds of findings or versions exist → search and structured grouping prevent loss of evidence lineage.

## Dashboard Read Access

### US-009: Review authorized data without mutation controls

**As an** Internal Viewer, **I want** to inspect permitted dashboards and reports without editing them, **so that** I can understand outcomes without risking operational changes.

Acceptance criteria:

- AC-1: Given viewer access, when Dashboard loads, then only resources inside effective scope are visible.
- AC-2: Given an authorized report, when it opens, then published and internal visibility follows the viewer's role and the report's current state.
- AC-3: Given a read-only role, when navigation renders, then mutation controls and administrative destinations are absent.
- AC-4: Given a permitted download, when it is requested, then only the selected authorized immutable report is delivered.

Edge cases:

- EC-1 [Invalid input]: A malformed resource identifier or filter is used → no unrelated resource is shown.
- EC-2 [Empty / missing]: Scope contains no current reports → Dashboard shows an explanatory empty state.
- EC-3 [Limits]: Read results exceed one page → paging does not alter permissions or ordering.
- EC-4 [Permissions]: A viewer attempts a hidden mutation through a direct request → it is denied and no state changes.
- EC-5 [Concurrency]: Access is revoked while a report is open → the next read or download is denied immediately.
- EC-6 [Interruption]: Download is interrupted → retry creates fresh authorized access rather than exposing a reusable URL.
- EC-7 [Repetition]: The same report is opened repeatedly → immutable content remains identical for that version.
- EC-8 [Ordering]: A deep link arrives before tenant selection → only an authorized tenant context can resolve it.
- EC-9 [State transitions]: A report is superseded → the current version leads while the viewed version stays labeled historical.
- EC-10 [Scale]: A viewer has access to many units → filters never expand beyond effective scope.

## Customer Timeline

### US-010: Follow shared assets and projects over time

**As a** Customer or Property Owner, **I want** an asset- and project-centered timeline, **so that** I can follow the evolution of reviews relevant to me.

Acceptance criteria:

- AC-1: Given customer access, when Dashboard opens, then permitted assets and projects are the primary entry points.
- AC-2: Given a selected asset or project, when its timeline opens, then inspection status, milestones, progress, recapture state, and published reports appear chronologically.
- AC-3: Given multiple direct and inherited grants, when the customer changes context, then only permitted descendants are discoverable.
- AC-4: Given unpublished internal work, when the timeline loads, then safe progress may appear but internal notes and unpublished result content do not.

Edge cases:

- EC-1 [Invalid input]: An invalid timeline filter or identifier is supplied → it is rejected without revealing resource existence.
- EC-2 [Empty / missing]: A newly shared asset has no inspections → the customer sees an understandable first-use state.
- EC-3 [Limits]: Timeline history is long → chronological paging preserves version and stage continuity.
- EC-4 [Permissions]: A customer follows a link to an unshared sibling asset → no tenant or resource detail is disclosed.
- EC-5 [Concurrency]: A grant is revoked while the timeline is open → content disappears on the next interaction and access guidance replaces it.
- EC-6 [Interruption]: Timeline loading is interrupted → partial information is marked incomplete and can be retried safely.
- EC-7 [Repetition]: The customer revisits a timeline → selected context and published immutable content remain consistent.
- EC-8 [Ordering]: Events arrive or are processed late → the displayed order follows business occurrence time and explains pending state where needed.
- EC-9 [State transitions]: A project closes or reopens → both transitions remain in history and current status is prominent.
- EC-10 [Scale]: A customer owns many assets or projects → search and grouping keep the asset/project entry model usable.

## Customer Evidence

### US-011: Inspect published reports and evidence in simple or advanced mode

**As a** Customer or Property Owner, **I want** simple results by default and optional advanced evidence history, **so that** I can understand outcomes quickly or investigate the full permitted lineage.

Acceptance criteria:

- AC-1: Given a published report, when it opens, then simple mode shows the outcome, advisory explanation, key evidence, progress context, and current report version.
- AC-2: Given advanced mode, when the customer enables it, then original, replacement, superseded, and discarded evidence records are organized by requirement, stage, and lineage.
- AC-3: Given evidence blocked for possible sensitive content, when either mode renders, then the visual file is never shown and only safe metadata, state, and reason are available.
- AC-4: Given a critical or inconclusive automatically published report, when it opens, then its exact classification, uncertainty, and advisory nature are prominent.
- AC-5: Given a prior report version, when it is selected, then it is clearly historical and cannot be mistaken for the current published result.

Edge cases:

- EC-1 [Invalid input]: A modified evidence or report reference is requested → access is denied without disclosing hidden media.
- EC-2 [Empty / missing]: A published report has no displayable visual evidence → the explanation and safe metadata remain meaningful.
- EC-3 [Limits]: Advanced mode contains many media items → grouped progressive navigation avoids loading or presenting an unbounded wall of content.
- EC-4 [Permissions]: A customer has asset access but not the targeted report or inspection → content remains unavailable until explicitly inherited or granted.
- EC-5 [Concurrency]: Publication or access is revoked while media is opening → the media request is denied and stale access is not reusable.
- EC-6 [Interruption]: A permitted image fails to load → no broken state implies deletion or a different classification; retry guidance is shown.
- EC-7 [Repetition]: A published immutable version is downloaded or reopened repeatedly → content and hashes remain consistent for that version.
- EC-8 [Ordering]: Advanced mode is opened before the report is published → no unpublished result or evidence becomes visible.
- EC-9 [State transitions]: Evidence is later replaced through recapture → prior and replacement records remain distinct and the current lineage is identified.
- EC-10 [Scale]: Hundreds of evidence records exist → mode switching, grouping, and pagination preserve context and accessibility.

## Customer Notifications

### US-012: Receive safe progress and publication notifications

**As a** Customer or Property Owner, **I want** notifications for relevant review changes, **so that** I know when to return to Dashboard without constant checking.

Acceptance criteria:

- AC-1: Given configured and verified channels, when an authorized asset or project reaches a relevant progress event, then the customer receives one safe notification per selected channel and one in-product notification.
- AC-2: Given a newly published report, when notification is sent, then it identifies safe context and links to an authorization-checked Dashboard destination.
- AC-3: Given recapture or delay that changes visible progress, when it occurs, then the notice explains the status without attaching evidence or internal reasoning.
- AC-4: Given notification preferences change, when saved, then future notices follow the new selection without changing prior delivery records.

Edge cases:

- EC-1 [Invalid input]: An invalid or unverified delivery channel is selected → it cannot receive customer notifications.
- EC-2 [Empty / missing]: No channel is selected → in-product notification remains available and Admin shows the delivery limitation.
- EC-3 [Limits]: Events occur in a burst → notices remain attributable and avoid unbounded duplication.
- EC-4 [Permissions]: Access is revoked before delivery or link opening → the message reveals no protected detail and Dashboard denies the destination.
- EC-5 [Concurrency]: Publication and revocation happen together → no usable content is exposed after revocation.
- EC-6 [Interruption]: A provider fails → the in-product state remains accurate and delivery status is visible to authorized administrators.
- EC-7 [Repetition]: An event is replayed → the customer does not receive duplicate notices for the same business change.
- EC-8 [Ordering]: Notifications arrive out of order → each opens current authorized state and does not present stale status as current.
- EC-9 [State transitions]: A report is unpublished or invalidated after notice → the destination reflects current visibility and status.
- EC-10 [Scale]: A customer follows many assets → notifications remain grouped, filterable, and scoped to permitted resources.

## Capture Access

### US-013: Enter one scoped responsibility with link and OTP

**As an** Inspection Participant, **I want** to enter Capture from an invitation without creating an account, **so that** I can complete only the evidence requested from me.

Acceptance criteria:

- AC-1: Given a valid invitation link, when it opens, then Capture exchanges and removes the secret from visible browser history before showing the OTP step.
- AC-2: Given the valid OTP, when verification succeeds, then the participant sees only the assigned origin, inspection, or recapture responsibility.
- AC-3: Given required disclosure, when consent is accepted, then the guided responsibility becomes available; refusal prevents capture.
- AC-4: Given an expired, revoked, completed, or invalid invitation, when it opens, then no responsibility detail is disclosed and recovery guidance is shown.

Edge cases:

- EC-1 [Invalid input]: Malformed link or OTP → generic failure prevents secret or resource inference.
- EC-2 [Empty / missing]: Link, OTP, or required consent is absent → Capture cannot proceed.
- EC-3 [Limits]: OTP attempts or resend limits are reached → further attempts pause according to the stated recovery window.
- EC-4 [Permissions]: A valid session requests another responsibility → the target remains undiscoverable.
- EC-5 [Concurrency]: The same invitation is verified on multiple devices → session rules remain explicit and never broaden responsibility access.
- EC-6 [Interruption]: Verification is interrupted → the participant can restart only while the invitation remains valid.
- EC-7 [Repetition]: A successful link or OTP action is repeated → it does not create duplicate responsibilities or extend authorization unexpectedly.
- EC-8 [Ordering]: Capture APIs are called before link exchange, OTP, or consent → each premature step is denied.
- EC-9 [State transitions]: The inspection is canceled, invalidated, submitted, or completed during access → the session stops and explains that access is no longer available.
- EC-10 [Scale]: Many participants open independent links concurrently → each sees only their own responsibility and receives timely feedback.

## Origin Capture

### US-014: Register guided origin evidence

**As an** Origin Contributor, **I want** Capture to guide reference-photo registration, **so that** future inspections have clear, described, and policy-compliant origin evidence.

Acceptance criteria:

- AC-1: Given an origin responsibility, when Capture starts, then requirements, categories, instructions, minimum evidence, descriptions, and policy expectations are explicit.
- AC-2: Given a photo, when it is added, then source, time, description, required location context, progress, and quality or sensitive-content state are visible.
- AC-3: Given a requirement cannot be completed and impossibility is allowed, when a nonblank reason is submitted, then progress reflects the accepted exception.
- AC-4: Given all submission conditions are met or incompleteness is explicitly confirmed where allowed, when submitted, then a new immutable origin version results.

Edge cases:

- EC-1 [Invalid input]: Unsupported media, excessive size, invalid metadata, or hostile text → the item is rejected with safe corrective guidance.
- EC-2 [Empty / missing]: Required description, category, evidence, GPS, or impossibility reason is absent → affected progress remains incomplete.
- EC-3 [Limits]: Photo or active-evidence limits are reached → additional items are blocked without losing accepted work.
- EC-4 [Permissions]: The origin session attempts inspection or tenant data → access is denied.
- EC-5 [Concurrency]: The same draft is uploaded from two active views → one evidence record and clear current progress result.
- EC-6 [Interruption]: Camera, location, network, or upload fails → accepted local progress remains recoverable and next steps are explicit.
- EC-7 [Repetition]: Upload completion or metadata save repeats → one logical evidence item remains.
- EC-8 [Ordering]: Submission occurs before verification → finalization is blocked and pending items are identified.
- EC-9 [State transitions]: Origin responsibility is revoked during capture → no further submission is accepted and local state is safely explained.
- EC-10 [Scale]: A valid origin contains many requirements and photos → progress remains navigable by section and requirement.

## Inspection Capture

### US-015: Complete guided inspection evidence

**As an** Inspection Participant, **I want** a mobile-first, requirement-by-requirement capture journey, **so that** I can provide comparable evidence without understanding internal administration.

Acceptance criteria:

- AC-1: Given an inspection responsibility, when Capture opens, then only applicable requirements, reference context, descriptions, checklists, policies, and progress are shown.
- AC-2: Given camera, gallery, location, and quality policies, when evidence is added, then allowed actions and advisory flags are explicit.
- AC-3: Given a required item cannot be produced and policy allows it, when a reason is accepted, then the submission can remain explicitly incomplete or attention-bearing as defined.
- AC-4: Given keyboard, screen reader, reduced viewport, or non-color perception, when the participant uses the journey, then every primary action and state remains understandable.

Edge cases:

- EC-1 [Invalid input]: Unsupported file, invalid answer, inaccurate required GPS, or malformed description → the exact item remains incomplete with recovery guidance.
- EC-2 [Empty / missing]: No applicable requirements exist → Capture explains the condition and does not create an empty success silently.
- EC-3 [Limits]: Media, answer, or description bounds are reached → additional input is prevented without corrupting saved progress.
- EC-4 [Permissions]: The participant tries to view findings, reports, or another inspection → no internal or unrelated content is exposed.
- EC-5 [Concurrency]: Two devices update one requirement → conflicting progress is reconciled without silently overwriting accepted evidence.
- EC-6 [Interruption]: Permission denial, browser refresh, app backgrounding, or connection loss occurs → recoverable progress and next action are explicit.
- EC-7 [Repetition]: The same answer or media action repeats → one logical outcome is retained.
- EC-8 [Ordering]: A later section is reached before a blocking prerequisite → Capture guides the participant back to the unmet requirement.
- EC-9 [State transitions]: Deadline expires or responsibility closes during capture → further finalization is blocked and current server state is explained.
- EC-10 [Scale]: The template contains many requirements → section navigation and progress prevent disorientation on mobile.

## Directed Recapture

### US-016: Replace only requested evidence

**As a** Recapture Participant, **I want** a new scoped journey that shows only requested corrections, **so that** I can address deficiencies without repeating or changing accepted evidence.

Acceptance criteria:

- AC-1: Given a recapture invitation, when access succeeds, then only selected requirements, current reasons, deadline, and permitted reference context appear.
- AC-2: Given replacement evidence, when submitted, then prior evidence remains in lineage and the replacement is identified as current for the selected requirement.
- AC-3: Given multiple reasons for one requested item, when it opens, then all active reasons are understandable without exposing internal notes.
- AC-4: Given recapture completion or expiry, when analysis continues, then Capture closes and Dashboard reflects the resulting current state.

Edge cases:

- EC-1 [Invalid input]: Replacement targets an unrequested item → the action is rejected without broadening scope.
- EC-2 [Empty / missing]: A recapture request has no valid active requirement → Capture shows unavailable access and accepts no evidence.
- EC-3 [Limits]: Replacement evidence exceeds policy limits → only excess items are blocked and prior lineage stays intact.
- EC-4 [Permissions]: An original or other recapture session accesses this request → authorization is denied.
- EC-5 [Concurrency]: System and reviewer reasons or two replacement attempts overlap → one active lineage is preserved with all applicable reasons.
- EC-6 [Interruption]: Replacement upload is interrupted → resumable progress remains tied only to the recapture responsibility.
- EC-7 [Repetition]: Replacement submission is replayed → no duplicate lineage or analysis work becomes visible.
- EC-8 [Ordering]: Submission is attempted before all selected items are resolved → blocking requirements and allowed incomplete behavior are explicit.
- EC-9 [State transitions]: The recapture expires during use → no late submission is accepted and the participant sees a final unavailable state.
- EC-10 [Scale]: Many requested corrections exist → grouping and progress keep each reason associated with the correct evidence.

## Capture Resilience

### US-017: Resume interrupted capture and uploads

**As an** Inspection Participant, **I want** unfinished evidence and progress to survive common interruptions, **so that** connectivity loss or refresh does not force me to restart.

Acceptance criteria:

- AC-1: Given pending drafts, when Capture refreshes or connectivity returns, then the participant sees saved progress and can resume unfinished uploads.
- AC-2: Given an offline period, when evidence is captured, then local-pending status is explicit and final submission remains unavailable.
- AC-3: Given expired upload authorization but an active responsibility, when resume starts, then Capture obtains a safe continuation path without duplicating completed work.
- AC-4: Given the server verifies all required media, when progress refreshes, then only verified items become ready for final submission.

Edge cases:

- EC-1 [Invalid input]: A local draft is corrupt or no longer matches accepted policy → it is isolated and recovery does not affect valid drafts.
- EC-2 [Empty / missing]: No recoverable draft exists → Capture starts from confirmed server progress.
- EC-3 [Limits]: Device storage is insufficient → the participant is warned before relying on local persistence and receives cleanup guidance.
- EC-4 [Permissions]: The responsibility expires before resume → local data cannot restore server authorization or expose protected context.
- EC-5 [Concurrency]: Resume runs in multiple tabs → completed parts are reconciled and not uploaded as duplicate evidence.
- EC-6 [Interruption]: Connectivity repeatedly drops → each accepted part remains recognizable and the participant sees current pending state.
- EC-7 [Repetition]: Resume is requested after success → Capture confirms completed state without restarting the upload.
- EC-8 [Ordering]: Final submission is attempted before pending uploads resume → the action is blocked with the pending count.
- EC-9 [State transitions]: Server-side evidence becomes blocked during resume → it cannot become ready and the reason is presented safely.
- EC-10 [Scale]: Many pending media parts exist → progress remains responsive, attributable, and resumable per item.

## Capture Finality

### US-018: Submit immutable evidence and receive confirmation

**As an** Inspection Participant, **I want** an explicit final submission, **so that** I know when my responsibility is complete and understand that later changes require recapture.

Acceptance criteria:

- AC-1: Given ready evidence and connectivity, when the participant reviews final progress, then the product explains whether submission is complete or explicitly incomplete before confirmation.
- AC-2: Given final confirmation, when the server accepts submission, then the evidence set becomes immutable and the responsibility session closes.
- AC-3: Given successful submission, when the confirmation view appears, then it contains no submitted media, internal finding, classification, or report.
- AC-4: Given a later correction need, when an authorized user requests it, then a new recapture invitation is required and the prior submission remains unchanged.

Edge cases:

- EC-1 [Invalid input]: Submission contains unresolved invalid evidence or answers → it is rejected with the unresolved items identified.
- EC-2 [Empty / missing]: Required evidence or reasons are missing → complete submission is blocked and only permitted incomplete behavior is offered.
- EC-3 [Limits]: Submission reaches media or requirement limits → accepted items remain stable and only policy-compliant finalization is allowed.
- EC-4 [Permissions]: A closed or unrelated session submits → the request is denied without reopening responsibility.
- EC-5 [Concurrency]: Two final submissions race → one immutable result exists and both attempts resolve to that final state.
- EC-6 [Interruption]: The connection drops after the final action → Capture checks authoritative state before inviting a retry.
- EC-7 [Repetition]: Final submission is replayed → no evidence, origin version, recapture response, or downstream work is duplicated.
- EC-8 [Ordering]: Submission is attempted before media verification → the participant sees pending verification and cannot complete.
- EC-9 [State transitions]: Cancellation, invalidation, expiry, or prior completion occurs before final acceptance → no new submission is created.
- EC-10 [Scale]: A maximum-size valid submission completes → final status remains attributable to every requirement and item.
