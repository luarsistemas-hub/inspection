# User Stories: GraphQL and Frontend Capability Parity

Canonical behavior catalog for completing the Inspection backend contract and all
Admin, Dashboard, and Capture experiences. Companion to `_prd.md`; consumed by
`_techspec.md` for component mapping and `_tests.md` for the coverage matrix.

## Personas

- **Platform super administrator** — an internal operator whose fixed identity has an explicit `TENANT_ADMIN` membership in every tenant and who must enter one tenant context before acting.
- **Tenant administrator** — the accountable administrator for one tenant with full authority over its configuration, access, catalogs, governance, and audit.
- **Organization administrator** — a delegated administrator responsible for tenant settings, business units, and origins inside assigned scopes.
- **Access administrator** — a delegated administrator responsible for users, invitations, roles, scopes, and effective-access review.
- **Participation administrator** — a delegated administrator responsible for participants, contacts, delivery channels, and segments.
- **Inspection configuration administrator** — a delegated administrator responsible for templates, analysis profiles, assets, assignments, and origin configuration.
- **Governance administrator** — a delegated administrator responsible for publication, notification, retention, deletion, and legal holds.
- **Auditor** — a read-only administrator who investigates configuration, access, usage, delivery, and audit history.
- **Manager** — an internal operational user who manages schedules, inspections, projects, triage, recapture, reports, and publication inside assigned scopes.
- **Employee** — an internal operational user who executes permitted workflows but cannot publish reports or administer tenant governance.
- **Internal viewer** — an internal user who can inspect authorized operational data without mutating it.
- **Customer viewer** — an external customer who can see only policy-approved published results and evidence.
- **External participant** — a link-scoped user who authenticates by OTP and captures origin or inspection evidence, including recapture.
- **Release owner** — the person who verifies that every canonical operation has an owner, a journey, and fresh evidence before the legacy frontend is removed.

## Story Index

| ID | Feature Area | Persona | Story |
| --- | --- | --- | --- |
| US-001 | Platform contract | Release owner | Maintain complete operation-to-journey parity |
| US-002 | Tenant access | Platform super administrator | Enter any tenant through an explicit membership |
| US-003 | Admin experience | Administrative user | Use the market-grade scoped Admin console |
| US-004 | Tenant management | Tenant administrator | Configure tenant identity and defaults |
| US-005 | Organization | Organization administrator | Manage business units through their lifecycle |
| US-006 | Access | Access administrator | Invite and disable internal memberships |
| US-007 | Access | Access administrator | Assign delegated roles and resource scopes |
| US-008 | Access | Access administrator | Understand effective access |
| US-009 | Participation | Participation administrator | Manage participants and contacts |
| US-010 | Participation | Participation administrator | Verify and select delivery destinations |
| US-011 | Catalogs | Participation administrator | Publish and activate segment definitions |
| US-012 | Catalogs | Inspection configuration administrator | Publish and activate template versions |
| US-013 | Catalogs | Inspection configuration administrator | Manage analysis profiles |
| US-014 | Assets | Inspection configuration administrator | Manage assets and assignments |
| US-015 | Origins | Organization administrator | Invite an origin capture |
| US-016 | Origins | Organization administrator | Upload, activate, and invalidate origin versions |
| US-017 | Admin efficiency | Administrative user | Import, export, and safely update resources in bulk |
| US-018 | Governance | Governance administrator | Configure publication and notification policies |
| US-019 | Governance | Governance administrator | Configure retention and legal holds |
| US-020 | Governance | Governance administrator | Soft-delete and automatically purge governed data |
| US-021 | Audit | Auditor | Investigate and export audit and usage history |
| US-022 | Scheduling | Manager | Manage recurring inspection schedules |
| US-023 | Inspections | Manager or employee | Create and transition inspections |
| US-024 | Projects | Manager or employee | Manage projects and their stages |
| US-025 | Triage | Manager or viewer | Review prioritized operational work |
| US-026 | Reports | Manager | Review, download, publish, and invalidate reports |
| US-027 | Recapture | Manager and external participant | Request and complete targeted recapture |
| US-028 | Customer portal | Customer viewer | Browse authorized portfolio and timeline |
| US-029 | Customer portal | Customer viewer | Read published reports and shared evidence |
| US-030 | Notifications | Authenticated recipient | Read notifications and manage verified channels |
| US-031 | Capture | External participant | Authenticate, consent, capture, and submit evidence |
| US-032 | Capture | External participant | Recover from offline, incomplete, and sensitive-content states |
| US-033 | Legacy retirement | Release owner | Retire `apps/web` after verified parity |

## Platform Contract and Product Boundaries

### US-001: Maintain complete operation-to-journey parity

**As a** release owner, **I want** every canonical GraphQL operation mapped to an owned business journey or internal workflow step, **so that** backend capability and shipped product behavior cannot drift apart.

Acceptance criteria:

- AC-1: Given the canonical schema, when parity is reviewed, then every query and mutation names its owning frontend, permitted persona, business journey, and observable verification evidence.
- AC-2: Given an operation that is only an internal step, when its journey is documented, then the user sees the business action rather than a generic GraphQL executor.
- AC-3: Given a missing read model or placeholder resolver, when backend completion is assessed, then the operation is not marked complete until it returns authorized production-shaped data and states.
- AC-4: Given a schema change, when clients are generated, then Admin, Dashboard, and Capture consume matching canonical types without hand-maintained contract copies.

Edge cases:

- EC-1: Invalid or undocumented schema field → parity fails with the unmapped field named.
- EC-2: Empty domain data → the owning journey remains verifiable through a first-use state.
- EC-3: Pagination, quotas, and maximum input sizes → the matrix identifies the user-visible limit behavior.
- EC-4: Unauthorized or cross-tenant operation → the matrix requires denial evidence without data leakage.
- EC-5: Concurrent schema and client changes → parity remains failed until all consumers use the same contract.
- EC-6: Interrupted generation or partial deployment → mismatched artifacts cannot be accepted as complete.
- EC-7: Retried mutation → the journey defines idempotent replay behavior.
- EC-8: Operation called outside its prerequisite order → the user receives a stable state error.
- EC-9: Operation targets an archived or invalid state → the matrix records the expected blocked transition.
- EC-10: Zero, typical, and 100-times-typical records → list and batch behavior remains defined.

### US-002: Enter any tenant through an explicit membership

**As a** platform super administrator, **I want** an explicit tenant context backed by my membership in each tenant, **so that** I can support every tenant without weakening tenant isolation.

Acceptance criteria:

- AC-1: Given a newly created tenant, when provisioning completes, then the fixed super administrator identity has an active `TENANT_ADMIN` membership in that tenant.
- AC-2: Given multiple memberships, when the super administrator selects a tenant, then no business data loads until the selected tenant context is established and displayed.
- AC-3: Given an administrative action, when it succeeds or fails, then audit history identifies the shared super administrator identity, tenant, action, target, outcome, and correlation reference.
- AC-4: Given the Admin login in any environment, when the configured fixed credential is used, then authentication is delegated to the identity provider and the credential is never exposed by the product.

Edge cases:

- EC-1: Unknown tenant, malformed tenant reference, or hostile deep link → entry is rejected without confirming tenant existence.
- EC-2: Identity has no membership → no tenant data or empty pseudo-tenant is shown.
- EC-3: Very large membership count → tenant selection remains searchable and paginated.
- EC-4: Disabled membership or inactive tenant → entry is denied even with the super administrator identity.
- EC-5: Tenant switches in two tabs → each tab shows its active tenant and prevents ambiguous writes.
- EC-6: Provisioning is interrupted → tenant creation reports incomplete administrative access and can be retried safely.
- EC-7: Provisioning replay after success → no duplicate membership is created.
- EC-8: Deep link into a different tenant → explicit tenant selection occurs before content loads.
- EC-9: Tenant becomes inactive during a session → subsequent actions stop and the UI explains the state change.
- EC-10: Many tenants with similar names → stable identifiers and status distinguish them.

### US-003: Use the market-grade scoped Admin console

**As an** administrative user, **I want** a clear, efficient console organized around configuration and governance, **so that** I can manage high-impact settings without confusing them with operational work.

Acceptance criteria:

- AC-1: Given Admin access, when the console opens, then navigation is grouped into Administrative overview, Organization, Identity and access, Participation, Inspection configuration, and Governance.
- AC-2: Given any Admin page, when it is displayed, then the active tenant, active scope, current role, page responsibility, and route back to Dashboard are visible.
- AC-3: Given an administrative collection, when it loads, then it provides a specific title, search, applicable filters, result count, sortable dense table, pagination, one primary action, and contextual row actions.
- AC-4: Given a resource detail, when it opens, then identity, status, scope, related resources, available actions, version, and history are understandable without raw JSON.
- AC-5: Given desktop, tablet, mobile consultation, keyboard navigation, or 200% zoom, when the Admin is used, then content reflows without hidden actions or horizontal page scrolling.

Edge cases:

- EC-1: Invalid filter or malformed deep link → the page preserves safe filters and explains what must change.
- EC-2: First-use empty collection or no search result → distinct empty states and permitted next actions appear.
- EC-3: Long labels, IDs, and maximum result pages → content remains readable and copyable without breaking layout.
- EC-4: Insufficient role or scope → navigation and page content disclose no inaccessible objects.
- EC-5: Resource changes while its form is open → a version conflict prompts review instead of overwriting.
- EC-6: Load or mutation interruption → prior data and form input remain visible with recovery actions.
- EC-7: Repeated click on a mutation → only one action is accepted and progress remains announced.
- EC-8: Direct navigation skips collection context → the detail reconstructs tenant and scope safely.
- EC-9: Archived object → its banner and restricted actions are explicit.
- EC-10: Collection grows to 100 times typical size → server-backed search, filtering, and pagination remain usable.

## Tenant, Identity, and Access

### US-004: Configure tenant identity and defaults

**As a** tenant administrator, **I want** to view and update the current tenant's name, language, timezone, and status context, **so that** every experience uses correct organizational defaults.

Acceptance criteria:

- AC-1: Given the current tenant, when its detail opens, then name, language, default timezone, status, stable ID, version, and recent changes are shown.
- AC-2: Given valid changes, when they are saved, then all affected frontends show the new tenant name and format future dates with the selected timezone.
- AC-3: Given tenant bootstrap, when creation succeeds, then the initial business unit and super administrator membership are visible.
- AC-4: Given pt-BR as the shipped language, when tenant language is viewed, then the interface remains localization-ready without claiming unavailable translations.

Edge cases:

- EC-1: Blank name, invalid language, or invalid timezone → save is rejected at the relevant field.
- EC-2: Optional branding or metadata absent → tenant detail remains complete without placeholders.
- EC-3: Maximum name length or timezone list → selection and display remain usable.
- EC-4: Non-tenant administrator or wrong tenant → update is denied without exposing values.
- EC-5: Two administrators update the tenant → stale version is rejected with current values offered for review.
- EC-6: Save loses connection → input remains and confirmed state is not falsely reported.
- EC-7: Repeated bootstrap request → no duplicate tenant or initial unit is created.
- EC-8: Update attempted before tenant selection → action is blocked.
- EC-9: Inactive tenant → configuration becomes read-only except permitted recovery behavior.
- EC-10: Super administrator has many tenants → only the selected tenant is changed.

### US-005: Manage business units through their lifecycle

**As an** organization administrator, **I want** to create, inspect, update, and archive business units, **so that** tenant resources remain organized by accountable scope.

Acceptance criteria:

- AC-1: Given permitted scope, when a unit is created, then its code, name, active state, version, and tenant relationship appear in the collection.
- AC-2: Given an active unit, when it is edited, then dependent resource counts and impact are shown before save.
- AC-3: Given an archivable unit, when archive is confirmed, then it is removed from active selectors but remains available in history and audit.
- AC-4: Given multiple units, when searched or paginated, then filters and collection context persist after opening and closing a detail.

Edge cases:

- EC-1: Blank or duplicate code, invalid name, or hostile text → field-level rejection.
- EC-2: No units beyond the bootstrap unit → first-use guidance appears.
- EC-3: Maximum units or deep pagination → stable ordering and page boundaries are visible.
- EC-4: Organization administrator lacks the unit scope → no data or action leaks.
- EC-5: Unit changes concurrently → stale archive or update is rejected.
- EC-6: Archive is interrupted → the UI reports the confirmed server state on retry.
- EC-7: Duplicate create or archive replay → only one lifecycle change occurs.
- EC-8: Archive requested before dependent impact loads → confirmation remains unavailable.
- EC-9: Unit already archived or tenant inactive → invalid transition is explained.
- EC-10: Unit has 100-times-typical dependent resources → impact summary remains bounded and linkable.

### US-006: Invite and disable internal memberships

**As an** access administrator, **I want** to invite internal users and disable memberships, **so that** tenant access has an accountable lifecycle.

Acceptance criteria:

- AC-1: Given an identity reference, role, and scopes, when an invitation is sent, then the membership and invitation status appear with actor, issue time, and scope.
- AC-2: Given a pending, failed, expired, accepted, or revoked invitation, when its row is viewed, then only valid actions such as resend, revoke, or inspect are offered.
- AC-3: Given an active membership, when disable is confirmed, then new access is denied immediately and the membership remains in audit history.
- AC-4: Given invitation delivery through email, SMS, or WhatsApp, when channels partially fail, then each destination outcome and retry option is visible.

Edge cases:

- EC-1: Malformed identity, unsupported role, invalid scope, or unsafe destination → invitation is rejected with specific fields.
- EC-2: No memberships or no eligible destination → a non-error empty state explains the prerequisite.
- EC-3: Invitation rate or membership limit reached → the limit and recovery path are shown.
- EC-4: Access administrator attempts a role or scope above their own → action is denied.
- EC-5: Membership changes during disable → stale version is rejected.
- EC-6: Delivery process restarts → successful channels are not resent unnecessarily.
- EC-7: Replayed invitation request → one logical invitation and traceable delivery set exists.
- EC-8: Acceptance occurs after revocation or expiry → access remains denied.
- EC-9: Disabled membership is invited again → the product requires an explicit reactivation or new-invitation decision.
- EC-10: Thousands of memberships → search and pagination remain server-backed.

### US-007: Assign delegated roles and resource scopes

**As an** access administrator, **I want** to assign administrative and operational roles with explicit resource scopes, **so that** people receive only the authority needed for their work.

Acceptance criteria:

- AC-1: Given a membership, when its role is edited, then available choices include the approved administrative roles and operational roles permitted by the editor's authority.
- AC-2: Given a role, when scopes are selected, then the UI explains the affected tenant, business units, segments, or resources before save.
- AC-3: Given a successful assignment, when the membership detail refreshes, then direct role, scopes, version, and audit event are visible.
- AC-4: Given a redundant or conflicting grant, when reviewed, then the Admin identifies it and explains its effective consequence.

Edge cases:

- EC-1: Unknown role, malformed resource ID, duplicate scope, or prohibited combination → assignment is rejected.
- EC-2: No eligible scopes → the role cannot be saved with misleading empty authority.
- EC-3: Very large scope trees → search and incremental selection remain usable.
- EC-4: Editor grants authority beyond their own → server denial is explained without exposing the target.
- EC-5: Two access administrators edit one membership → stale version cannot overwrite newer grants.
- EC-6: Save is interrupted → original grants stay visible and draft selections are preserved.
- EC-7: Repeated assignment request → one version change and one logical audit action occur.
- EC-8: Role assignment precedes required membership activation → invalid order is blocked.
- EC-9: Scope points to archived resource → it cannot be newly assigned.
- EC-10: Membership has many scopes → grouped presentation remains understandable.

### US-008: Understand effective access

**As an** access administrator, **I want** to inspect why a user is allowed or denied an action, **so that** I can correct access without guessing how roles and scopes combine.

Acceptance criteria:

- AC-1: Given a membership, when effective access opens, then each resource/action decision shows allowed or denied, source grant, role, scope, inheritance path, and validity.
- AC-2: Given direct and inherited grants, when compared, then the Admin distinguishes them and identifies redundant or conflicting rules.
- AC-3: Given two memberships or roles, when compared, then differences are shown without changing either membership.
- AC-4: Given an access change, when the view refreshes, then it explains the new outcome and links to its audit event.

Edge cases:

- EC-1: Unknown resource/action probe → no fabricated decision is shown.
- EC-2: Membership has no effective grants → a clear least-privilege empty state appears.
- EC-3: Very large permission set → results are grouped, searchable, and paginated.
- EC-4: Reviewer lacks access to the subject or resource → result is denied without existence leakage.
- EC-5: Grants change during comparison → the view warns that versions differ and offers refresh.
- EC-6: Explanation load is interrupted → no stale decision is presented as current.
- EC-7: Repeated probes → results remain consistent and do not create mutations.
- EC-8: Deep link omits tenant context → evaluation does not run.
- EC-9: Membership disabled or resource archived → the current state dominates historical grants.
- EC-10: Complex nested scopes → explanation remains traceable from decision to source.

## Participation, Catalogs, Assets, and Origins

### US-009: Manage participants and contacts

**As a** participation administrator, **I want** to create, inspect, update, archive, search, and bulk-import participants and their contacts, **so that** inspection responsibilities reach the correct people.

Acceptance criteria:

- AC-1: Given valid participant data, when saved, then business unit, segment role, status, version, contacts, and assignments are visible.
- AC-2: Given an existing participant, when edited, then contact changes and impact on selected delivery channels are explained before save.
- AC-3: Given an archive or soft-delete action, when confirmed, then the participant disappears from active assignment selectors but remains historically attributable.
- AC-4: Given a bulk import, when validation finishes, then accepted and rejected rows are listed with no hidden failures.

Edge cases:

- EC-1: Blank name, invalid contact, duplicate normalized contact, or hostile import cell → affected field or row is rejected.
- EC-2: No contacts or no participants → the UI explains which workflows remain unavailable.
- EC-3: File, row, contact, and pagination limits → limits are shown before submission.
- EC-4: Administrator lacks participant or business-unit scope → access is denied.
- EC-5: Participant changes during edit or import → stale update is rejected item by item.
- EC-6: Import or save is interrupted → confirmed rows are distinguishable from unprocessed rows.
- EC-7: Replayed create/import → duplicates are not silently created.
- EC-8: Participant is assigned before required catalog or unit exists → prerequisite is explained.
- EC-9: Archived participant receives an active assignment attempt → transition is blocked.
- EC-10: 100-times-typical participant volume → search and import result navigation remain usable.

### US-010: Verify and select delivery destinations

**As a** participation administrator, **I want** to verify participant contacts and select eligible delivery destinations, **so that** invitations and reminders use trusted channels.

Acceptance criteria:

- AC-1: Given an unverified email, SMS, or WhatsApp contact, when verification succeeds, then its status, verification time, and available selection action are shown.
- AC-2: Given verified active contacts, when delivery selection is saved, then only selected contact IDs are used for future deliveries.
- AC-3: Given a selected contact that becomes inactive or unverified, when the participant is viewed, then it is removed from eligibility and the user is warned.

Edge cases:

- EC-1: Malformed destination or invalid verification decision → action is rejected.
- EC-2: No verified contacts → save cannot imply that delivery is configured.
- EC-3: Channel verification rate limit → retry timing is visible.
- EC-4: Administrator lacks participant scope → contact values are not disclosed.
- EC-5: Participant version changes during selection → stale selection is rejected.
- EC-6: Verification provider times out → status remains pending rather than confirmed.
- EC-7: Duplicate verification or selection replay → state remains idempotent.
- EC-8: Selection attempted before verification → blocked with a verification path.
- EC-9: Contact archived, participant deleted, or tenant inactive → action is unavailable.
- EC-10: Many contacts across participants → collection filters by channel and verification state.

### US-011: Publish and activate segment definitions

**As a** participation administrator, **I want** to publish versioned segment definitions and activate a chosen version, **so that** participant and asset attributes follow a controlled schema.

Acceptance criteria:

- AC-1: Given a key, name, schema, and UI schema, when published, then an immutable version with digest, status, number, and publication time is visible.
- AC-2: Given a valid published version, when activated, then the definition identifies it as active and future use references that version.
- AC-3: Given multiple versions, when history is viewed, then differences, status, digest, and affected resources are understandable.

Edge cases:

- EC-1: Invalid JSON schema, hostile UI schema, duplicate key, or incompatible definition → publish is rejected safely.
- EC-2: No definitions or no active version → dependent creation explains the missing prerequisite.
- EC-3: Maximum schema size or version count → limit and remediation are visible.
- EC-4: Participation administrator lacks scope → definitions outside scope remain hidden.
- EC-5: Activation uses stale definition version → conflict is shown.
- EC-6: Publication is interrupted → no partial version is presented as published.
- EC-7: Replayed publish with same idempotency key → one immutable version exists.
- EC-8: Activation before publication → transition is blocked.
- EC-9: Definition archived or incompatible with active assets → impact is shown and invalid transition blocked.
- EC-10: Large version history → history is paginated and digest remains copyable.

### US-012: Publish and activate template versions

**As an** inspection configuration administrator, **I want** to publish immutable template versions and activate one version, **so that** capture requirements are reproducible for each inspection.

Acceptance criteria:

- AC-1: Given a valid template definition, when published, then version number, digest, status, linked segment version, and requirement preview appear.
- AC-2: Given a published version, when activated, then new schedules, projects, and inspections use it without changing historical records.
- AC-3: Given a template version ID, when opened, then its complete definition and publication metadata are available without raw unformatted JSON as the only view.

Edge cases:

- EC-1: Invalid requirement, duplicate key, malformed definition, or unsupported policy → publish is rejected with location.
- EC-2: No template or no active version → creation flows explain the prerequisite.
- EC-3: Maximum requirements, media counts, or definition size → limits are visible before publish.
- EC-4: Administrator lacks template scope → access is denied.
- EC-5: Two activations race → only the version accepted by current concurrency rules becomes active.
- EC-6: Publish is interrupted → draft input remains and no partial version is visible.
- EC-7: Duplicate publish replay → one immutable version is produced.
- EC-8: Activate before segment compatibility is satisfied → blocked with the dependency named.
- EC-9: Template archived or version invalidated → unavailable for new work but historical inspections remain readable.
- EC-10: Extensive version history → filtering and pagination remain responsive.

### US-013: Manage analysis profiles

**As an** inspection configuration administrator, **I want** to discover, publish, inspect, activate, and retire versioned analysis profiles, **so that** automated analysis behavior is controlled and reproducible.

Acceptance criteria:

- AC-1: Given existing profiles, when the collection loads, then key, version, status, model alias, prompt version, confidence threshold, digest, and usage status are visible.
- AC-2: Given a valid profile definition, when published, then an immutable version appears and can be selected for future inspections according to policy.
- AC-3: Given profile history, when a version is retired, then historical reports retain their original profile reference and new work cannot select the retired version.

Edge cases:

- EC-1: Invalid output schema, unknown model alias, unsafe prompt metadata, or out-of-range threshold → publish is rejected.
- EC-2: No profile exists → inspection creation explains why analysis cannot start.
- EC-3: Definition and version limits → the Admin shows the supported boundary.
- EC-4: Administrator lacks configuration scope → profile details are hidden.
- EC-5: Concurrent activation or retirement → stale action is rejected.
- EC-6: Publication dependency is unavailable → no published success is shown.
- EC-7: Repeated publish → idempotent result or explicit new-version intent is required.
- EC-8: Profile selected before publication → operation is blocked.
- EC-9: Retired profile is referenced by active schedule → impact and migration choice are shown.
- EC-10: Many profiles and versions → server-backed search and pagination remain usable.

### US-014: Manage assets and assignments

**As an** inspection configuration administrator, **I want** to register, inspect, update, search, bulk-import, assign, and archive assets, **so that** inspections target correctly configured real-world objects.

Acceptance criteria:

- AC-1: Given valid asset data, when registered, then unit, segment version, template, external key, address, geofence, attributes, policy overrides, assignments, status, and version are shown.
- AC-2: Given an active asset, when edited, then changes to template, segment, location, policy, and participant assignments show impact before save.
- AC-3: Given an archive action, when confirmed, then the asset cannot receive new schedules or inspections but remains in historical timelines and reports.
- AC-4: Given an import or filtered export, when completed, then each row has an observable success or failure result and the export reflects active filters.

Edge cases:

- EC-1: Duplicate external key, invalid coordinates, malformed attributes, unsafe overrides, or invalid assignment → field or row is rejected.
- EC-2: No assets or missing template/segment → prerequisite guidance appears.
- EC-3: File, assignment, geofence, attribute, and page limits → boundaries are explicit.
- EC-4: Administrator lacks unit or asset scope → no object or assignment data leaks.
- EC-5: Concurrent asset update or archive → stale version is rejected.
- EC-6: Import or update interruption → confirmed server state is recoverable.
- EC-7: Duplicate registration replay → no second asset is silently created.
- EC-8: Schedule or inspection requested before asset activation → blocked.
- EC-9: Asset archived while work is active → existing work remains visible and new work is prevented according to policy.
- EC-10: 100-times-typical assets → search, filters, pagination, and export remain bounded.

### US-015: Invite an origin capture

**As an** organization administrator, **I want** to invite a participant to capture an asset origin image, **so that** the reference image has authenticated provenance.

Acceptance criteria:

- AC-1: Given an eligible asset, participant, verified destination, and expiry, when an invitation is sent, then invitation ID, origin version ID, status, delivery outcomes, and expiry are visible.
- AC-2: Given a pending invitation, when managed, then resend, administrative revoke, and history are distinct actions.
- AC-3: Given an accepted capture, when its origin version appears, then actor, channel, source, capture metadata, and immutable original are traceable.

Edge cases:

- EC-1: Invalid expiry, archived asset, ineligible participant, or unverified destination → invite is rejected.
- EC-2: No delivery destination → invitation cannot pretend to be sent.
- EC-3: Invitation and provider rate limits → retry timing and partial outcomes appear.
- EC-4: Administrator lacks asset or participant scope → action is denied.
- EC-5: Asset or invitation changes during send → final status reflects one accepted version.
- EC-6: Delivery interrupted → successful channel attempts are preserved.
- EC-7: Replayed invitation → one logical invitation is maintained.
- EC-8: OTP requested after expiry or revoke → Capture shows an unavailable state.
- EC-9: Asset archived after invitation → submission is blocked or quarantined with an explicit state.
- EC-10: Many invitations → filter by asset, participant, state, expiry, and channel.

### US-016: Upload, activate, and invalidate origin versions

**As an** organization administrator, **I want** to upload an existing origin image and manage immutable origin versions, **so that** legitimate source material can be onboarded without obscuring provenance.

Acceptance criteria:

- AC-1: Given an administrative upload, when submitted, then original media, `ADMIN_UPLOAD` source, actor, declared source, capture date, justification, and audit reference are recorded.
- AC-2: Given origin versions for an asset, when one is activated, then it becomes the reference for future comparison and previous versions remain immutable.
- AC-3: Given an invalid origin version, when invalidated, then it cannot be selected for new work and the reason is visible in history.

Edge cases:

- EC-1: Unsupported media, excessive size, invalid date, missing justification, or malicious file → upload is rejected before activation.
- EC-2: Asset has no origin → first-use state offers capture invitation or administrative upload.
- EC-3: Media and version limits → the Admin explains how to proceed.
- EC-4: Administrator lacks origin or asset scope → media and metadata are not disclosed.
- EC-5: Concurrent activation/invalidation → only a valid current transition succeeds.
- EC-6: Upload interruption → resumable state is distinguished from complete storage.
- EC-7: Duplicate upload completion → one media/version result exists.
- EC-8: Activation before verification completes → blocked.
- EC-9: Active version invalidated → future comparisons show that no active reference exists until replacement.
- EC-10: Long origin history → paginated lineage remains traceable.

### US-017: Import, export, and safely update resources in bulk

**As an** administrative user, **I want** selective bulk operations, **so that** high-volume maintenance is efficient without making sensitive changes opaque.

Acceptance criteria:

- AC-1: Given participant or asset import data, when validation runs, then the user sees proposed creates/updates, warnings, and row-level errors before applying changes.
- AC-2: Given selected participants or assets, when a safe bulk archive or scope assignment is available, then target count, scope, consequences, and item-level result are shown.
- AC-3: Given a filtered administrative collection or audit search, when exported, then the export reflects current filters, authorized fields, timezone, and an identifiable generation status.
- AC-4: Given permissions, policies, retention, publication, or other sensitive configuration, when bulk mode is requested, then no bulk mutation is offered.

Edge cases:

- EC-1: Malformed, hostile, duplicate, or mixed-schema rows → rejected rows cannot corrupt accepted data.
- EC-2: Empty file or zero selection → no operation starts.
- EC-3: File, row, export, and processing limits → limit is shown before or during validation.
- EC-4: Mixed authorized and unauthorized rows → unauthorized targets fail without leaking details.
- EC-5: Objects change between preview and apply → affected rows report conflicts.
- EC-6: Process interruption → status distinguishes queued, applied, failed, and unprocessed rows.
- EC-7: Retry after partial success → successful rows are not duplicated.
- EC-8: Apply requested before preview → blocked.
- EC-9: Archived or deleted targets in selection → item-level invalid state appears.
- EC-10: 100-times-typical import/export → asynchronous progress and bounded result access remain understandable.

## Governance, Retention, and Audit

### US-018: Configure publication and notification policies

**As a** governance administrator, **I want** to configure publication and notification policies, **so that** customer visibility and communications follow tenant rules.

Acceptance criteria:

- AC-1: Given the current publication policy, when viewed, then mode, version, customer evidence defaults, affected scope, and last change are understandable.
- AC-2: Given a policy change, when saved, then a before/after impact summary and audit event are available.
- AC-3: Given notification policy, when configured, then email, SMS, WhatsApp, mandatory in-product notices, retry expectations, and applicable recipients are explicit.
- AC-4: Given a customer request, when content is evaluated, then only published and explicitly shared evidence is returned.

Edge cases:

- EC-1: Unknown mode, unsafe visibility combination, or invalid channel rule → save is rejected.
- EC-2: Policy absent on a new tenant → restrictive defaults are shown, never implicit broad sharing.
- EC-3: Policy size or recipient limits → boundary is explained.
- EC-4: Non-governance administrator → policy data and actions follow permitted read scope.
- EC-5: Concurrent policy edit → stale version cannot overwrite current policy.
- EC-6: Save or notification provider interruption → prior policy remains authoritative.
- EC-7: Replayed policy change → one logical version and audit action occur.
- EC-8: Publish attempted before policy exists → restrictive default or explicit prerequisite applies.
- EC-9: Tenant inactive or policy archived → mutation is blocked.
- EC-10: Many policy history entries → history remains paginated and comparable.

### US-019: Configure retention and legal holds

**As a** governance administrator, **I want** to configure retention windows and apply or release legal holds, **so that** data is kept and purged according to policy.

Acceptance criteria:

- AC-1: Given retention policy, when viewed or edited, then evidence, operational, and security periods, version, affected data, and expected purge behavior are shown.
- AC-2: Given an inspection, when legal hold is applied with a reason, then scheduled purge is blocked and hold status is visible to authorized administrators.
- AC-3: Given an active hold, when released with a reason, then the next eligible purge timing is recalculated and audited.
- AC-4: Given a policy or hold action, when completed, then its current status can be queried rather than inferred from a mutation response.

Edge cases:

- EC-1: Negative, contradictory, excessive, or malformed retention period or blank hold reason → action is rejected.
- EC-2: No explicit tenant policy → documented defaults appear.
- EC-3: Maximum holds or retention history → list remains bounded and searchable.
- EC-4: User lacks governance scope → hold existence and protected data are not leaked.
- EC-5: Policy and hold change concurrently → purge uses the latest confirmed state and stale mutation fails.
- EC-6: Scheduler or request interruption → status remains inspectable and no false completion is shown.
- EC-7: Repeated apply/release → idempotent current state and audit semantics are preserved.
- EC-8: Release requested before hold exists → invalid order is explained.
- EC-9: Hold applied after soft delete but before purge → purge remains blocked.
- EC-10: Many due purges → policy status and backlog remain observable.

### US-020: Soft-delete and automatically purge governed data

**As a** governance administrator, **I want** to soft-delete eligible inspection data and let the system purge it after retention, **so that** privacy requests are fulfilled without losing accountable process evidence.

Acceptance criteria:

- AC-1: Given an eligible inspection and reason, when deletion is requested by an authorized administrator, then it is immediately soft-deleted and removed from active journeys.
- AC-2: Given the retention deadline with no legal hold, when it passes, then personal data, media, and operational content are physically purged automatically.
- AC-3: Given a completed purge, when audit is inspected, then only a non-sensitive tombstone with technical ID, type, dates, actor, policy, and reason remains.
- AC-4: Given a legal hold, when purge becomes due, then no protected content is removed and the blocked outcome is auditable.

Edge cases:

- EC-1: Invalid inspection ID or hostile reason → request is rejected without existence leakage.
- EC-2: No eligible content → request reports a stable terminal state.
- EC-3: Large related media set → progress and completion remain observable without partial success claims.
- EC-4: Non-governance administrator → deletion request is denied.
- EC-5: Hold is applied while purge starts → final authorization check prevents prohibited deletion.
- EC-6: Purge process restarts → already removed objects are handled idempotently.
- EC-7: Duplicate deletion request → one logical request controls the lifecycle.
- EC-8: Purge requested before retention deadline → blocked.
- EC-9: Already purged or invalidated inspection → stable tombstone response, not recreation.
- EC-10: Purge backlog at 100 times typical volume → due work remains ordered and inspectable.

### US-021: Investigate and export audit and usage history

**As an** auditor, **I want** searchable audit, delivery, retention, and usage views, **so that** I can reconstruct who did what, where, when, and with what outcome.

Acceptance criteria:

- AC-1: Given audit history, when searched, then filters include time range, actor, action, resource, scope, outcome, and correlation reference.
- AC-2: Given an event detail, when opened, then safe before/after values, reason, version, tenant scope, and related object are shown when retained.
- AC-3: Given notification deliveries, when inspected, then intent, per-channel status, attempt timing, failure/retry state, and related resource are visible.
- AC-4: Given a usage period, when requested, then request count, token usage, cost, timezone boundaries, and data freshness are displayed.
- AC-5: Given an authorized filtered view, when export is requested, then it contains only allowed fields and current filters and exposes generation status.

Edge cases:

- EC-1: Invalid date range, malformed cursor, or hostile search → query is rejected safely.
- EC-2: No events, deliveries, or usage → specific empty state appears.
- EC-3: Export or query limit reached → partial result is labeled and continuation explained.
- EC-4: Auditor lacks target scope → sensitive values and events are redacted or denied.
- EC-5: New events arrive during pagination → stable cursor semantics avoid silent duplication.
- EC-6: Export generation is interrupted → status is retryable and no broken file is offered.
- EC-7: Repeated export request → duplicate work is bounded and results remain attributable.
- EC-8: Deep link to purged event → tombstone view appears if authorized.
- EC-9: Event refers to archived or deleted target → history remains readable without reactivating it.
- EC-10: 100-times-typical event volume → server-side filters and pagination remain usable.

## Dashboard Operations

### US-022: Manage recurring inspection schedules

**As a** manager, **I want** to list, create, update, and cancel schedules, **so that** inspections are generated at the right cadence and deadline.

Acceptance criteria:

- AC-1: Given eligible asset, participant, template, timezone, recurrence, start, deadline, and reminders, when a schedule is created, then next due time and active status are shown.
- AC-2: Given an active schedule, when recurrence or reminders change, then the next due time and impact are previewed before save.
- AC-3: Given a cancellation, when confirmed, then no future inspection is generated while prior inspections remain visible.

Edge cases:

- EC-1: Invalid recurrence, timezone, deadline, reminder order, or date → field-level rejection.
- EC-2: No schedules or missing eligible dependencies → first-use guidance appears.
- EC-3: Reminder count and schedule limits → boundary is shown.
- EC-4: Manager lacks asset/unit scope → schedule data is denied.
- EC-5: Concurrent update or cancel → stale version is rejected.
- EC-6: Scheduler interruption → next generation state remains observable and recoverable.
- EC-7: Duplicate create/update replay → no duplicate schedule or generation occurs.
- EC-8: Start date, template, or origin prerequisite out of order → creation is blocked with reason.
- EC-9: Asset, participant, or template archived → new generation is prevented.
- EC-10: Many schedules → filters and pagination support due time, status, unit, and asset.

### US-023: Create and transition inspections

**As a** manager or employee, **I want** to list, inspect, create, cancel, and invalidate inspections within my scope, **so that** operational work has a controlled lifecycle.

Acceptance criteria:

- AC-1: Given eligible asset, participant, template/reference, due time, deadline, reminders, and reason, when created, then the inspection and invitation-ready state appear.
- AC-2: Given an inspection, when detail opens, then source, status, reason, evidence count, versions, deadlines, project relationship, and history are visible.
- AC-3: Given a valid cancellation, when confirmed, then collection, detail, and triage show the canceled state.
- AC-4: Given an invalidation with reason, when confirmed by an authorized role, then downstream report/publication implications are shown and audited.

Edge cases:

- EC-1: Invalid date, missing reason, incompatible reference, or malformed reminder → creation is rejected.
- EC-2: No inspections or no eligible asset → actionable empty state appears.
- EC-3: Evidence, reminder, and pagination limits → limits are explicit.
- EC-4: Employee or manager lacks scope, viewer mutates, or cross-tenant ID is used → denied without leakage.
- EC-5: Concurrent transition → stale version is rejected.
- EC-6: Creation or transition interruption → confirmed state is recoverable.
- EC-7: Duplicate mutation replay → one logical inspection or transition occurs.
- EC-8: Submit, cancel, or invalidate occurs out of order → invalid transition is explained.
- EC-9: Inspection already final, soft-deleted, or purged → only valid historical/tombstone behavior remains.
- EC-10: 100-times-typical inspections → history filter and pagination remain usable.

### US-024: Manage projects and their stages

**As a** manager or employee, **I want** to create projects and manage planned and exceptional stages, **so that** multi-step inspections follow an auditable workflow.

Acceptance criteria:

- AC-1: Given eligible asset, participant, and template, when a project is created, then ordered stages, report mode, status, version, and timeline are visible.
- AC-2: Given a valid project, when an exceptional stage is added with reason and planned time, then it appears in sequence and history.
- AC-3: Given an eligible stage, when started or skipped, then required reason, inspection linkage, and stage/project versions are updated visibly.
- AC-4: Given a closable or closed project, when close or reopen is confirmed, then status, reason, timeline, and allowed next actions update.

Edge cases:

- EC-1: Duplicate stage key, invalid order/date, missing reason, or incompatible template → rejected.
- EC-2: Project has no optional stages or no projects exist → clear first-use state.
- EC-3: Maximum stages and project pages → limit is explicit.
- EC-4: User lacks project/asset scope or is read-only → mutation denied.
- EC-5: Stage and project change concurrently → expected versions prevent overwrite.
- EC-6: Stage start is interrupted after inspection creation → resulting linked state is recoverable and not duplicated.
- EC-7: Replayed stage or project transition → one logical transition occurs.
- EC-8: Skip before eligibility, close with active required stage, or reopen before close → blocked.
- EC-9: Related asset or participant archived → existing project remains historical and invalid new transitions are blocked.
- EC-10: Long project timeline → ordered history remains navigable.

### US-025: Review prioritized operational work

**As a** manager or internal viewer, **I want** dashboard summaries and triage queues filtered to my scope, **so that** I can understand workload and open the most important inspection next.

Acceptance criteria:

- AC-1: Given authorized scope, when Dashboard opens, then summary counts and triage results reflect the same active filters and data freshness.
- AC-2: Given classification or status filters, when applied, then the queue updates, the applied filters remain visible, and pagination is preserved.
- AC-3: Given a triage item, when opened, then inspection, project, asset, evidence count, status, reason, and available actions are shown without losing queue context.
- AC-4: Given a viewer, when the same queue is used, then all mutation controls are absent and server denial remains authoritative.

Edge cases:

- EC-1: Invalid filter or cursor → safe validation error and retained prior results.
- EC-2: Zero priorities or no filter results → distinct empty states.
- EC-3: Page and summary limits → freshness and partial counts are labeled.
- EC-4: Cross-scope item or viewer mutation attempt → denied without leaking inaccessible data.
- EC-5: Item changes between list and detail → refreshed current state is shown.
- EC-6: Refresh fails → existing data is marked potentially stale.
- EC-7: Repeated refresh → no duplicated rows or actions.
- EC-8: Deep link to unavailable item → queue context recovers safely.
- EC-9: Inspection invalidated or deleted → queue removes it and detail shows permitted history.
- EC-10: 100-times-typical queue → server-backed filtering and pagination remain responsive.

### US-026: Review, download, publish, and invalidate reports

**As a** manager, **I want** to inspect report versions, download rendered outputs, publish an approved snapshot, and invalidate an incorrect publication, **so that** customers receive only controlled results.

Acceptance criteria:

- AC-1: Given an inspection report, when viewed, then classification, mode, canonical content, rendered content, version, digests, creation time, and publication history are understandable.
- AC-2: Given an available report snapshot, when PDF or another supported download is requested, then preparation status, integrity digest, and authorized time-limited download are shown.
- AC-3: Given an unpublished valid snapshot, when published by a manager, then customer access and notification follow the active publication policy.
- AC-4: Given an active publication, when invalidated with reason and current version, then customer access is removed immediately, no older version resurfaces, and affected recipients are notified.

Edge cases:

- EC-1: Invalid snapshot, unsupported download kind, blank reason, or malformed version → action rejected.
- EC-2: No report or no publication → explicit not-ready state.
- EC-3: Large report or generation limit → asynchronous status is visible.
- EC-4: Employee/viewer/customer attempts publish or internal report access → denied.
- EC-5: Concurrent publish/invalidate → stale version is rejected.
- EC-6: Rendering or notification interruption → publication truth and delivery outcomes remain distinguishable.
- EC-7: Repeated publish, download, or invalidate request → idempotent state and bounded generation.
- EC-8: Publish before report completion or invalidation before publication → blocked.
- EC-9: Inspection invalidated or deleted → publication actions follow governance rules.
- EC-10: Many report versions → version history and publication history remain paginated.

### US-027: Request and complete targeted recapture

**As a** manager and external participant, **I want** a targeted recapture request and guided replacement submission, **so that** insufficient evidence can be corrected without repeating the entire inspection.

Acceptance criteria:

- AC-1: Given an inspection and deficient requirements, when a manager requests recapture with reasons and deadline, then the request, items, delivery, and status are visible.
- AC-2: Given a valid recapture invitation, when the participant opens Capture, then only requested requirements and original context allowed by policy are shown.
- AC-3: Given replacement media, when submitted, then lineage identifies replaced media, request status changes, and Dashboard can review the new evidence.

Edge cases:

- EC-1: Empty item list, invalid original media, blank reason, or past deadline → request rejected.
- EC-2: No eligible destination or original evidence unavailable → manager sees the blocking reason.
- EC-3: Maximum recapture items/media → limit appears before submission.
- EC-4: Unauthorized manager or wrong link scope → no evidence is disclosed.
- EC-5: Original media or request changes concurrently → stale submission is rejected safely.
- EC-6: Delivery/upload interruption → request and media progress remain recoverable.
- EC-7: Duplicate request or submission → one logical recapture result exists.
- EC-8: Submission before OTP/consent or after deadline → blocked with next action.
- EC-9: Request canceled, completed, invalidated, or inspection purged → invalid transitions are explained.
- EC-10: Many recapture cycles → lineage and history remain traceable.

## Customer Dashboard and Notifications

### US-028: Browse authorized portfolio and timeline

**As a** customer viewer, **I want** to browse my shared assets, projects, progress, and timeline, **so that** I can follow work without seeing internal operations.

Acceptance criteria:

- AC-1: Given customer access, when portfolio loads, then only authorized assets/projects, published classification, customer-safe status, progress, and update time are shown.
- AC-2: Given an asset or project filter, when timeline opens, then customer-safe chronological events are shown and internal notes/transitions are excluded.
- AC-3: Given pagination or search, when navigating, then filters and context persist.

Edge cases:

- EC-1: Invalid filter, asset, project, or cursor → no existence leakage.
- EC-2: No shared work → customer-specific empty state.
- EC-3: Timeline and portfolio page limits → continuation remains explicit.
- EC-4: Customer requests another tenant's resource → denied indistinguishably from unavailable data.
- EC-5: Publication changes during navigation → current authorized state wins.
- EC-6: Load interruption → existing customer-safe data is marked stale and retryable.
- EC-7: Repeated pagination → no duplicate items.
- EC-8: Deep link before portfolio authorization → authorization runs before content.
- EC-9: Project closed, report invalidated, or asset archived → customer-safe state remains accurate.
- EC-10: Large portfolio → server-backed search and pagination remain usable.

### US-029: Read published reports and shared evidence

**As a** customer viewer, **I want** to read published reports and explicitly shared evidence, **so that** I can understand approved results without exposure to sensitive internal data.

Acceptance criteria:

- AC-1: Given an active publication, when report opens, then approved classification, advisory, status, version, and historical marker are shown.
- AC-2: Given evidence allowed by publication policy, when requested in simple or advanced mode, then only allowlisted fields, lineage, availability, and authorized media URLs are returned.
- AC-3: Given an invalidated publication, when the customer opens or refreshes it, then access is removed immediately and no previous version appears automatically.
- AC-4: Given shared media, when a URL is issued, then it is time-limited and unavailable after authorization or policy changes.

Edge cases:

- EC-1: Invalid inspection/version/mode or tampered URL → safe unavailable response.
- EC-2: No publication or no shared evidence → distinct explanatory states.
- EC-3: Evidence and download limits → pagination and expiry are explicit.
- EC-4: Internal-only field or unauthorized evidence → never returned or hinted.
- EC-5: Publication invalidates during viewing → refresh and subsequent media access are denied.
- EC-6: Media service interruption → report text remains available when authorized and media shows recoverable unavailability.
- EC-7: Repeated download request → bounded, authorized URL issuance.
- EC-8: Evidence requested before publication → blocked.
- EC-9: Media replaced, archived, soft-deleted, or purged → lineage state is shown without leaking removed content.
- EC-10: Large evidence set → pagination preserves stable lineage ordering.

### US-030: Read notifications and manage verified channels

**As an** authenticated recipient, **I want** to read notifications and choose among my verified channels, **so that** I receive relevant updates through trusted destinations.

Acceptance criteria:

- AC-1: Given notifications, when the center opens, then unread count, kind, title, body, related authorized resource, creation time, and read state are visible.
- AC-2: Given an unread notification, when marked read, then the count and item update idempotently.
- AC-3: Given recipient channels, when preferences open, then human-readable email, SMS, and WhatsApp destinations, verification state, selection, and current version are shown.
- AC-4: Given preference changes, when saved, then optional external delivery follows selections while mandatory in-product notifications remain enabled.

Edge cases:

- EC-1: Invalid notification/channel ID or unsupported selection → action rejected.
- EC-2: No notifications or no verified channels → specific empty state and verification path.
- EC-3: Page, unread count, and channel limits → boundaries are explicit.
- EC-4: Notification points outside current authorization → target content is not disclosed.
- EC-5: Preferences change concurrently → stale version is rejected.
- EC-6: Mark-read or preference save interruption → confirmed server state is recoverable.
- EC-7: Repeated mark-read/save → idempotent result.
- EC-8: Select channel before verification → blocked.
- EC-9: Destination deactivated or membership disabled → no future external delivery.
- EC-10: Many notifications → unread filter and pagination remain usable.

## Capture

### US-031: Authenticate, consent, capture, and submit evidence

**As an** external participant, **I want** a guided link-scoped capture flow, **so that** I can provide valid origin or inspection evidence with confidence.

Acceptance criteria:

- AC-1: Given a valid link, when OTP is requested and verified, then a time-limited external session opens only the assigned responsibility.
- AC-2: Given the current disclosure, when processing consent is accepted, then choices for photo processing, AI analysis, and GPS are recorded against that disclosure version.
- AC-3: Given capture requirements, when the participant records or selects eligible media, then upload progress, requirement association, description, source, GPS state, and media verification are understandable.
- AC-4: Given all required answers or an explicit incomplete confirmation, when submitted, then completion and attention state are shown and the Dashboard can observe the result.
- AC-5: Given a valid unaccepted invitation, when the participant revokes it, then the link becomes unusable and no tenant administration data is exposed.

Edge cases:

- EC-1: Malformed/expired link, wrong OTP, hostile metadata, unsupported media, or invalid GPS → specific safe error.
- EC-2: No optional requirements or no existing answers → flow still shows current step and next action.
- EC-3: OTP rate, media count, part, size, and session limits → boundaries and retry timing are visible.
- EC-4: Link/session belongs to another responsibility → no cross-responsibility data is exposed.
- EC-5: Answer or session changes in another tab → stale submission is rejected or reconciled explicitly.
- EC-6: Camera, GPS, network, or upload interruption → draft and confirmed upload state remain distinguishable.
- EC-7: Repeated OTP, upload completion, metadata save, or submission → idempotent logical results.
- EC-8: Capture before consent, submit before verification, or metadata before upload completion → blocked with next required step.
- EC-9: Invitation revoked/expired, responsibility completed, or inspection invalidated → no invalid transition is accepted.
- EC-10: Maximum requirements and media → step navigation remains usable on a 320-pixel screen.

### US-032: Recover from offline, incomplete, and sensitive-content states

**As an** external participant, **I want** recoverable handling of offline work, impossible requirements, and suspected sensitive content, **so that** I can finish honestly without losing evidence.

Acceptance criteria:

- AC-1: Given connection loss, when media or answers are saved locally, then Capture distinguishes draft, saved on this device, awaiting upload, uploading, received, and recoverable error states.
- AC-2: Given an allowed but impossible requirement, when a reason is declared, then it is saved visibly and included in incomplete-submission assessment.
- AC-3: Given a sensitive-content flag the participant believes is wrong, when a reasoned false-positive declaration is submitted, then the media state updates and remains reviewable.
- AC-4: Given interrupted multipart upload, when connection returns and the session remains valid, then only missing work resumes and duplicate media is not created.

Edge cases:

- EC-1: Blank impossibility/false-positive reason, corrupt local draft, or hostile device metadata → rejected or quarantined safely.
- EC-2: Nothing saved locally → recovery view does not imply pending upload.
- EC-3: Device storage, retry, part, and offline duration limits → the participant sees the risk and available action.
- EC-4: Session expires while offline → local data remains distinguishable but upload requires safe reauthentication.
- EC-5: Server receives media while another retry is pending → one canonical media state wins.
- EC-6: Browser or device restarts → eligible draft can be resumed without promising background delivery while closed.
- EC-7: Repeated completion or false-positive declaration → one logical result remains.
- EC-8: Submit incomplete without explicit confirmation or declare impossibility where forbidden → blocked.
- EC-9: Recapture closes or inspection invalidates while offline → upload does not silently attach to invalid work.
- EC-10: Many large media items on a constrained device → storage and queue usage remain visible and bounded.

## Legacy Retirement

### US-033: Retire `apps/web` after verified parity

**As a** release owner, **I want** valid legacy journeys migrated and evidenced before removing `apps/web`, **so that** duplicate ownership ends without losing behavior.

Acceptance criteria:

- AC-1: Given the legacy operation and route inventory, when migration is reviewed, then every valid behavior maps to Admin, Dashboard, Capture, or an explicitly retired capability.
- AC-2: Given the completed backend contract, when standalone frontend acceptance runs, then all mapped journeys pass without importing legacy source code.
- AC-3: Given parity evidence and no remaining runtime/deployment dependency, when `apps/web` is removed, then documentation, scripts, CI, generated artifacts, and deployment references point only to owned frontends.
- AC-4: Given removed legacy routes, when an applicable historical URL is requested, then the user receives an intentional redirect or retirement response rather than a broken ambiguous page.

Edge cases:

- EC-1: Unknown or malformed legacy route → safe not-found behavior.
- EC-2: Legacy-only empty-state copy or fixture → replacement journey still has an accepted first-use state.
- EC-3: Hidden build, package, or deployment dependency → removal remains blocked and dependency is named.
- EC-4: User lacks access in the new owner frontend → migration cannot bypass current authorization.
- EC-5: Legacy behavior changes during migration → inventory version is refreshed before final parity.
- EC-6: Removal or deployment is interrupted → rollback path does not restore split ownership silently.
- EC-7: Repeated redirects or old bookmarks → no redirect loop.
- EC-8: Legacy removed before parity gate → release is blocked.
- EC-9: Historical link targets archived/deleted content → new owner shows the permitted historical state.
- EC-10: 100-times-typical legacy route/test inventory → automated inventory remains reviewable and complete.
