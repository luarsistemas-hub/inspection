# Product Requirements Document: GraphQL and Frontend Capability Parity

## Overview

Inspection already exposes a broad GraphQL surface for tenant administration,
catalog configuration, asset origins, scheduling, inspections, projects,
capture, reporting, notifications, retention, and customer access. The current
standalone frontends expose only a small subset of that capability:

- Admin is a generic query shell with no complete administrative lifecycle.
- Dashboard exposes a small operational subset and placeholder customer views.
- Capture supports the core evidence journey but omits residual invitation and
  sensitive-content actions.
- Several backend read models are missing, and customer projection resolvers
  still return empty or null placeholders.
- `apps/web` retains legacy GraphQL documents and behavior that have not been
  fully migrated to the three owned frontends.

This initiative completes the product end to end. The backend and canonical
GraphQL contract are completed first. Admin, Dashboard, and Capture are then
built against that completed contract, every operation is mapped to an owned
journey, and the legacy frontend is removed only after verified parity.

The product serves tenant and delegated administrators, internal operational
teams, auditors, customer viewers, and external capture participants. Completing
parity provides a coherent way to configure the platform, run inspections,
publish controlled results, capture evidence, and prove every high-impact
decision without exposing GraphQL or raw JSON as the user experience.

The Admin redesign is a first-class product requirement. The mandatory market
reference was created with Open Design and defines Admin as a scoped B2B console
for configuration, identity, access, governance, and audit, separate from the
operational Dashboard.

## Goals

- Allow authorized administrators to manage the complete lifecycle of tenants,
  business units, memberships, roles, scopes, participants, contacts, segments,
  templates, analysis profiles, assets, origins, publication policy,
  notification policy, retention policy, deletion, legal holds, audit, and
  usage without raw GraphQL or JSON tooling.
- Allow managers and employees to manage schedules, inspections, projects,
  stages, triage, recapture, reports, downloads, publication, and invalidation
  within their effective scopes.
- Allow internal viewers to inspect authorized operations without mutation
  controls or mutation authority.
- Allow customer viewers to browse only policy-approved portfolio, timeline,
  published reports, and explicitly shared evidence.
- Allow external participants to complete origin capture, inspection capture,
  and recapture through a resilient link-scoped PWA, including consent,
  multipart upload, offline recovery, impossibility declarations, and
  sensitive-detection false-positive declarations.
- Guarantee that every canonical GraphQL query and mutation is consumed by a
  named business journey or an internal step of that journey.
- Replace placeholder customer and governance projections with usable,
  authorized read models.
- Make effective access explainable: administrators can see whether an action
  is allowed or denied and which role, scope, inheritance path, or direct grant
  produced the result.
- Guarantee tenant isolation even for the super administrator by requiring an
  explicit membership and active tenant context.
- Make ordinary physical deletion impossible for audit-sensitive resources;
  use archive, version, soft delete, legal hold, retention, and governed purge.
- Preserve origin and evidence provenance regardless of whether media comes
  from guided Capture or administrative upload.
- Withdraw an invalidated report from customer access immediately without
  silently restoring an older version.
- Support email, SMS, and WhatsApp as verifiable, observable delivery channels.
- Remove `apps/web` and all runtime, build, CI, documentation, and deployment
  dependencies on it after fresh parity evidence exists.

## User Stories

- `US-001`–`US-003`: canonical operation parity, tenant entry, and the Admin
  product experience.
- `US-004`–`US-008`: tenant, business-unit, membership, delegated-role, scope,
  and effective-access management.
- `US-009`–`US-017`: participants, contacts, segments, templates, analysis
  profiles, assets, origins, and selective bulk operations.
- `US-018`–`US-021`: publication, notifications, retention, legal hold, deletion,
  purge, audit, delivery history, and usage.
- `US-022`–`US-027`: schedules, inspections, projects, triage, reports,
  publication, invalidation, download, and recapture.
- `US-028`–`US-030`: customer portfolio, timeline, reports, evidence, and
  recipient notifications/preferences.
- `US-031`–`US-032`: external authentication, consent, capture, offline upload,
  submission, impossibility, recapture, and false-positive handling.
- `US-033`: legacy parity gate and removal of `apps/web`.

[Full user stories](_user_stories.md)

[Canonical GraphQL capability matrix](_capability_matrix.md)

## Core Features

### 1. Complete canonical product contract

The backend must expose every state needed to render usable list, detail,
creation, edit, history, conflict, unavailable, and recovery experiences.
Mutation-only behavior is insufficient when users cannot subsequently query its
current state.

Required contract completion includes:

- Discoverable analysis-profile collections, detail, versions, active state,
  retirement state, and usage relationships.
- Current notification preferences and human-readable verified recipient
  channels, including their version.
- Report publication collection, active publication, complete publication
  history, and the IDs/versions required for safe invalidation.
- Deletion-request, soft-delete, legal-hold, purge-eligibility, purge-progress,
  purge-result, and residual tombstone views.
- Effective-access explanation and comparison for roles, grants, resources,
  and scopes.
- Administrative invitation collection and safe administrative resend/revoke
  operations that do not require exposing public link tokens.
- Administrative origin-media upload with explicit provenance.
- Complete archive/deactivate/retire behavior for resources that currently have
  only publish or upsert operations.
- Selective participant/asset import and filtered collection/audit export.
- Real customer portfolio, timeline, report, and evidence projections rather
  than empty or null placeholder results.
- Queryable delivery attempt and retry states for email, SMS, and WhatsApp.

The canonical schema remains the source of truth. Generated server and frontend
artifacts must agree with it. Every operation must have a stable external error
outcome and an idempotent mutation identity where applicable.

### 2. Tenant access and delegated administration

All business data remains tenant-isolated. A user selects one membership and
tenant context before any tenant data loads. No Admin page may aggregate or
search customer business data across tenants.

The fixed platform super administrator identity receives an explicit
`TENANT_ADMIN` membership automatically in every tenant. It uses the same fixed
operator-supplied credential in development, staging, and production as an
accepted security exception. The password value must not appear in this PRD,
source control, frontend bundles, logs, generated output, or product responses.

Approved administrative roles are:

| Role | Authority |
| --- | --- |
| `TENANT_ADMIN` | Full authority inside one tenant |
| `ORGANIZATION_ADMIN` | Tenant settings, business units, and origins |
| `ACCESS_ADMIN` | Users, invitations, roles, scopes, permissions, and effective access |
| `PARTICIPATION_ADMIN` | Participants, contacts, delivery selections, and segments |
| `INSPECTION_CONFIG_ADMIN` | Templates, analysis profiles, assets, and assignments |
| `GOVERNANCE_ADMIN` | Publication, notifications, retention, deletion, and legal hold |
| `AUDITOR` | Read-only administrative, audit, delivery, retention, and usage access |

Delegated roles can act only inside their assigned resource scopes. Server
authorization is authoritative; frontend capability controls explain and hide
unavailable actions but never grant access.

### 3. Open Design Admin console

The Open Design reference `referencia-mercado-admin-inspection.md` is the
mandatory Admin baseline. It synthesizes patterns from mature enterprise
consoles while preserving a distinct Inspection identity.

Admin answers:

- Who can access what.
- In which tenant, unit, segment, or resource the authority applies.
- How participants, catalogs, assets, origins, and policies are configured.
- Who changed a configuration, when, why, and with which version.
- What a high-impact action will affect before it is confirmed.

Its canonical architecture is:

`grouped side navigation -> persistent tenant/scope -> collection -> detail -> history/audit`

Navigation groups and destinations:

| Group | Destinations |
| --- | --- |
| Administrative overview | Tenant configuration health and recent administrative changes |
| Organization | Tenant and business units; origins |
| Identity and access | Users; invitations; roles and permissions; effective access |
| Participation | Participants; contacts and delivery; segments |
| Inspection configuration | Templates; analysis profiles; assets |
| Governance | Publication; notifications; retention; deletion/holds; audit and usage |

The administrative overview is not an operational KPI dashboard. It shows
configuration health such as pending invitations, unreviewed access,
missing/incomplete policies, retention readiness, and recent changes.

Every administrative collection must provide:

- Breadcrumb, responsibility statement, active tenant, and active scope.
- One primary action appropriate to the current role.
- Search, applicable filters, result count, ordering, and pagination.
- Dense semantic table with identification, scope, state, last change,
  responsible actor, and contextual actions.
- Specific first-use, no-result, loading, stale, error, and denied states.
- A detail route that preserves collection filters and context.

Every administrative detail must provide:

- Human name, copyable ID, status, scope, version, and related resources.
- One primary action and secondary actions ordered by consequence.
- Overview, access/relationships, and history sections as applicable.
- Clear archived, inactive, invalidated, soft-deleted, or out-of-scope state.
- Safe version-conflict recovery without silent overwrite.

Long or high-impact forms use a focused full page, not a complex modal. They
show consequences, affected scope, current version, before/after summary,
unsaved changes, inline errors, and a single primary submit action.

Visual direction is “Evidence Trail” with “Precise Operations” density:

- Compact lists and tables; richer evidence and history context in details.
- Dividers and spacing for hierarchy instead of excessive cards.
- One principal action color per screen.
- Literata for contextual headings, Source Sans 3 for interface text, and IBM
  Plex Mono for IDs, versions, timestamps, coordinates, and audit references.
- Light neutral surfaces and a restrained teal action/accent palette as defined
  by the Open Design reference.
- No decorative gradients, generic KPI walls, raw JSON result panels, or flat
  navigation containing every module at one level.

### 4. Tenant and organization management

Tenant administrators can bootstrap and update the current tenant. Tenant
identity includes name, language, default timezone, status, stable ID, and
version. The shipped interface language is pt-BR and is structured for future
localization. Dates use the tenant timezone, with UTC available in audit and
technical details.

Organization administrators manage business units through create, update, and
archive states. Archived units disappear from active selectors but remain in
history. Impact on dependent assets, participants, schedules, inspections, and
projects must be visible before an archive action.

### 5. Identity, invitations, roles, scopes, and effective access

Access administrators manage internal memberships and invitations. Invitation
states include pending, accepted, expired, revoked, and failed delivery.
Resend, revoke, role change, scope change, and membership disable are distinct
actions.

Role and scope editors must show what the administrator can grant, prevent
authority above the administrator's own effective authority, and explain the
target tenant/resource impact before save.

The effective-access experience is a first-class read model. For every probed
resource/action it identifies:

- Allowed or denied result.
- Direct versus inherited source.
- Role or grant that produced the result.
- Tenant and resource scope.
- Validity and membership state.
- Redundant or conflicting grants.

Administrators can compare two memberships or roles without mutating them.
Every access change emits an auditable event.

### 6. Participants, contacts, and segments

Participation administrators manage participant identity, business unit,
segment role, lifecycle, contacts, and selected delivery destinations.

Supported destinations are email, SMS, and WhatsApp. A destination must be
active, verified, and selected before invitations or reminders use it. Contact
verification and destination selection are separate observable states.

Segment definitions are immutable after publication. Administrators publish
new versions and explicitly activate a compatible version. Historical assets,
templates, inspections, and reports retain the exact referenced version and
digest.

### 7. Templates and analysis profiles

Template versions define capture requirements, evidence rules, media counts,
description requirements, source policy, comparison target, and whether an
impossibility declaration is allowed. Published versions are immutable. One
compatible version may be active for new work while prior versions remain
historical.

Analysis profiles become fully discoverable and manageable. Administrators can
inspect model alias, prompt version, output schema, confidence threshold,
digest, status, usage, and history. Publishing creates an immutable version;
retirement prevents new selection without changing historical analysis.

Neither template nor profile editing may expose raw JSON as the only editing or
inspection experience. Structured forms and readable previews are required,
with an advanced source representation available only as a supplementary view.

### 8. Assets and origin versions

Inspection configuration administrators manage asset identity, unit, segment
version, template, external key, address, coordinates, geofence, attributes,
policy overrides, participant assignments, status, and version.

Assets support search, filter, registration, update, selective import/export,
assignment, archive, and detail history. Archived assets cannot receive new
schedules, inspections, projects, assignments, or origins, but historical work
remains visible according to authorization and retention.

Origin images enter through either:

1. An invited Capture journey with OTP, consent, participant provenance, and
   capture metadata.
2. Administrative upload labeled `ADMIN_UPLOAD`, requiring original file,
   actor, declared source, capture date, justification, and audit reference.

Every accepted origin creates an immutable version. Activating a version makes
it the reference for future comparison. Invalidating it removes future use but
does not overwrite or erase history.

### 9. Selective bulk administration

Bulk behavior is intentionally limited:

- Participant and asset import.
- Filtered participant, asset, administrative collection, and audit export.
- Safe, understandable batch actions such as archive or bounded scope
  assignment where authorization and consequences are homogeneous.

Imports require preview and row-level validation before apply. Results expose
accepted, rejected, conflicted, and unprocessed rows. Replaying an import cannot
silently duplicate accepted resources.

Bulk mutation is not available for roles/permissions, publication policies,
notification policies, retention policies, legal holds, deletion, report
publication, report invalidation, or other high-impact governance actions.

### 10. Publication, customer visibility, and reports

Publication policy is tenant-governed and restrictive by default. Customer
viewers can access only published reports and evidence explicitly included by
policy. Internal notes, preliminary results, model work product, unapproved
media, and sensitive metadata remain excluded.

Managers can inspect report versions and canonical/rendered representations,
request supported downloads, publish a selected snapshot, and inspect
publication history. Downloads disclose preparation state, integrity digest,
and authorized time-limited access.

Invalidation requires the current publication version and a reason. It:

- Immediately removes customer access.
- Preserves internal report and publication history.
- Sends required recipient notifications.
- Does not automatically expose a prior version.
- Leaves a customer-safe unavailable state until a new publication succeeds.

### 11. Notification and delivery experience

Email, SMS, and WhatsApp are complete product channels. Every delivery records
intent, destination, provider outcome, attempt count, failure state, and retry
status. Partial delivery is visible per channel; retry does not resend an
already successful destination unnecessarily.

Authenticated recipients can see in-product notifications, unread count,
related authorized resources, and read state. Marking read is idempotent.

Preference forms show human-readable verified destinations rather than raw
channel IDs. Optional external channels may be selected or deselected. Required
in-product notices remain enabled.

### 12. Retention, legal hold, soft delete, and purge

Governance administrators configure evidence, operational, and security
retention periods. Evidence and operational periods must be greater than zero;
security retention may be zero or greater. A new tenant without an explicit
policy is shown as governance-incomplete and does not physically purge data
until a valid policy exists.

Only authorized administrators can request deletion. The user-facing deletion
action immediately soft-deletes the eligible inspection and related active
views. It does not physically erase data at confirmation time.

After the configured retention deadline, automatic purge removes personal data,
media, and operational content unless an active legal hold blocks it. Applying
or releasing a legal hold requires a reason and is queryable after the action.
The purge process rechecks the hold before removal.

A completed purge preserves only a non-sensitive tombstone containing:

- Technical identifier.
- Resource type.
- Soft-delete, eligibility, and purge dates.
- Requesting/authorizing actor identity.
- Policy reference and non-sensitive reason.
- Audit and correlation references.

No personal fields, media, free-form operational content, or customer-visible
result may survive in the tombstone.

### 13. Audit, delivery, and usage observability

Audit is a searchable administrative product, not an ornamental timeline. It
supports actor, action, resource, scope, outcome, correlation reference, and
date/time filters. Event detail includes safe before/after values, version,
reason, tenant context, and related object when retained.

Filtered export obeys current authorization and retention. If a result is
partial or an export is still being prepared, the state is explicit.

Delivery observability shows channel-level outcomes and retry state. Usage
summary shows the requested period, request count, input/output tokens, cost,
freshness, and tenant-timezone interpretation.

### 14. Schedules, inspections, projects, and triage

Managers manage recurring schedules with recurrence, timezone, start, deadline,
reminder offsets, asset, participant, template, optional origin reference,
status, and next due time. Canceling a schedule stops future generation without
removing historical inspections.

Managers and authorized employees create inspections with explicit source,
reason, due time, deadline, reminders, template/profile versions, project/stage
relationship, and optional origin reference. Cancel and invalidate are separate
transitions; invalidation requires a reason and exposes report/publication
impact.

Projects contain ordered, versioned stages and transitions. Authorized users can
create projects, add exceptional stages with reasons, start eligible stages,
skip permitted stages with reasons, close eligible projects, and reopen closed
projects with reasons. Invalid ordering and stale project/stage versions cannot
advance state.

Dashboard summary and triage use the same active scope and filters. Queue items
link to inspection, project, asset, evidence count, classification, status,
reason, and next available action. `VIEWER` receives the same authorized read
context without mutation controls or mutation authority.

### 15. Recapture

Managers request recapture for specific requirements, with original media where
applicable, reason, and deadline. The external participant receives only the
requested requirements and policy-safe original context.

Replacement media preserves lineage to the original. Submission updates the
recapture request and becomes visible for Dashboard review. Duplicate requests,
uploads, or submissions do not create duplicate logical outcomes.

### 16. Customer Dashboard

`CUSTOMER_VIEWER` is always read-only. Customer portfolio exposes only
authorized assets/projects, published classification, customer-safe status,
progress, and update time. Timeline exposes only customer-safe chronological
events.

Customer report and evidence views apply explicit field allowlists and active
publication policy. Media access is time-limited and is denied after policy,
authorization, publication, replacement, soft-delete, or purge changes make it
unavailable.

The customer experience explains not-yet-published, invalidated, unavailable,
and empty states without revealing internal reasons or the existence of
unauthorized resources.

### 17. Capture PWA

Capture is a mobile-first, task-focused state machine:

1. Validate invitation link.
2. Request and verify the six-digit OTP.
3. Accept the current pt-BR disclosure version and processing choices.
4. Load only the assigned origin, inspection, or recapture responsibility.
5. Follow requirement-specific instructions.
6. Capture or select eligible media.
7. Upload media parts and verify completion.
8. Save description, source, GPS state, and allowed device context.
9. Declare an allowed impossibility or sensitive-detection false positive when
   applicable.
10. Review and submit complete or explicitly confirmed incomplete evidence.
11. Show the server-confirmed result.

OTP lifetime is 10 minutes. A link permits at most five OTP sends per hour, one
resend per 60 seconds, and five verification attempts per 10 minutes. An
external session lasts two hours. Rate-limit dependency failure fails closed.

Capture distinguishes:

- Draft.
- Saved on this device.
- Awaiting upload.
- Uploading.
- Received by the server.
- Recoverable error.
- Submitted.

It must not promise guaranteed background transmission while the application is
closed. A restart may recover eligible local work. If the external session has
expired, the participant reauthenticates before upload while local-state status
remains honest.

Public invitation revocation remains available from Capture. Admin receives a
separate administrative revoke/resend journey without exposing the public link
token.

### 18. Legacy removal and parity gate

`apps/web` is a temporary reference, not a dependency or destination for new
work. Before removal, the release owner maintains an inventory of legacy routes,
operations, forms, behavior, tests, scripts, packages, generated artifacts, and
deployment references.

Removal is allowed only when:

- Every valid legacy journey maps to Admin, Dashboard, Capture, or an explicitly
  retired behavior.
- Every one of the 33 canonical queries and 56 canonical mutations is reconciled
  against `_capability_matrix.md`.
- Every canonical GraphQL operation has an owner and fresh verification.
- Customer placeholder projections are complete.
- No standalone frontend imports legacy source.
- CI, scripts, documentation, deployment, and local workflow no longer require
  `apps/web`.
- Historical URLs receive intentional redirects or retirement responses where
  required.

## Business Rules

### Tenant and identity invariants

- Every business object belongs to exactly one tenant.
- Every authenticated internal action occurs through one active membership and
  one explicit tenant context.
- Tenant selection cannot authorize access; only an active server-resolved
  membership can.
- The super administrator has an explicit `TENANT_ADMIN` membership in every
  tenant, automatically provisioned when the tenant is created.
- Super administrator access never enables a cross-tenant business query.
- The fixed `Admin` identity and same operator-supplied static credential exist
  in all environments as an accepted security exception.
- Credential material never appears in source, PRD/ADR text, frontend output,
  GraphQL responses, logs, generated artifacts, exports, or test snapshots.
- Inactive tenant or disabled membership denies subsequent data access and
  mutations.

### Role and permission invariants

- `TENANT_ADMIN` has full authority only in the active tenant.
- Delegated administrators can grant only roles and scopes within their own
  effective authority.
- `AUDITOR`, `VIEWER`, and `CUSTOMER_VIEWER` never mutate protected product data.
- `MANAGER` may publish and invalidate reports within scope.
- `EMPLOYEE` may perform allowed operational mutations but cannot publish or
  invalidate customer reports or administer tenant governance.
- Effective access is server-computed and explainable; hiding a frontend control
  is not authorization.
- Cross-tenant IDs are rejected without confirming whether the object exists.

### Lifecycle invariants

- Published catalog, template, profile, origin, report, and policy versions are
  immutable.
- Activation affects future use and never rewrites historical references.
- Archived/deactivated resources cannot be newly selected or assigned.
- Ordinary resource management never physically deletes audit-sensitive data.
- Soft-deleted resources disappear from active workflows but remain governed by
  retention and legal hold.
- Physical purge occurs only after a valid retention deadline and a final check
  that no legal hold applies.
- Purge is idempotent and leaves only an allowlisted non-sensitive tombstone.
- Every high-impact lifecycle transition requires target, scope, consequence,
  and reason where specified.

### Concurrency and mutation invariants

- Versioned mutations reject stale versions and do not overwrite newer changes.
- Every mutation uses a client mutation identity or equivalent idempotency key.
- Replaying a completed mutation returns or identifies the same logical outcome
  rather than duplicating it.
- A UI disables duplicate submission while a mutation is in flight but remains
  able to recover the server-confirmed result after interruption.
- Internal failures expose stable generic messages and correlation references,
  not infrastructure details.
- Validation errors identify safe fields and preserve user input.

### Publication and customer invariants

- Customer access requires both resource authorization and an active publication
  policy decision.
- Unpublished, preliminary, internal, invalidated, soft-deleted, held-private,
  or purged content is never customer-visible.
- Evidence is denied by default unless explicitly shared.
- Invalidation removes customer access immediately and never falls back to an
  older publication automatically.
- Customer media URLs are authorized and time-limited.
- Customer projections expose allowlisted data rather than filtering a broad
  internal object in the browser.

### Contact and notification invariants

- Product channels are `EMAIL`, `SMS`, and `WHATSAPP` plus mandatory in-product
  notification where applicable.
- An external destination must be active, verified, and selected before use.
- Delivery result is recorded per destination and channel.
- Retrying a partial delivery does not resend destinations already confirmed as
  successful unless a new explicit delivery intent exists.
- Users may change optional external preferences but cannot disable mandatory
  in-product notices.
- Notification links resolve authorization again before showing the resource.

### Capture invariants and exact defaults

- OTP is exactly six decimal digits and is valid for 10 minutes.
- OTP sends are limited to five per invitation per hour.
- OTP resend is limited to one per invitation per 60 seconds.
- OTP verification is limited to five attempts per invitation per 10 minutes.
- External sessions are valid for two hours.
- Capture access is restricted to exactly one responsibility and its allowed
  origin/inspection/recapture context.
- Processing acceptance is tied to the exact disclosure version shown.
- Required descriptions, media counts, source policy, GPS requirements,
  comparison targets, and impossibility rules come from the immutable template
  version.
- “Saved on this device” never means “received by the server.”
- Submitted answers remain historically attributable and are not silently
  replaced by recapture media.

### Collection and bulk invariants

- Canonical list queries default to 25 items unless a feature defines another
  explicit supported value.
- Every collection uses stable cursor pagination and preserves active search and
  filters when navigating to detail and back.
- Empty collection, no search result, loading, stale data, recoverable error,
  denied, and terminal states are distinct.
- Bulk apply always follows a preview for participant and asset import.
- Bulk result reports each accepted, rejected, conflicted, and unprocessed item.
- Bulk operations never bypass per-item authorization or concurrency checks.
- Sensitive governance and permission changes remain individual.

### Language, date, and device rules

- The shipped language is pt-BR, with all user-facing strings externalized for
  future localization.
- New tenants default to language `pt-BR` and timezone `America/Sao_Paulo`
  unless explicitly configured otherwise.
- User-facing time uses tenant timezone; audit details also make the canonical
  UTC instant available.
- Admin is complete on desktop and tablet, with consultation and simple actions
  on mobile.
- Dashboard is functional on desktop, tablet, and mobile.
- Capture is mobile-first and usable from 320 CSS pixels.

## User Experience

### Administrative journey

1. The user authenticates through the Admin OIDC audience.
2. If multiple memberships exist, the user selects a tenant before any business
   data loads.
3. Admin displays tenant, scope, current role, configuration health, and grouped
   navigation.
4. The user opens a domain collection, applies search/filters, and reviews a
   dense decision-oriented table.
5. The user opens a detail without losing collection context.
6. For an allowed mutation, Admin explains affected scope, version, consequence,
   and history impact.
7. The user confirms, receives a specific success or validation/conflict state,
   and can open the resulting audit event.
8. The user can move to Dashboard through an explicit contextual link without
   mixing navigation hierarchies.

### Operational journey

1. The user authenticates through Dashboard and receives role- and scope-adapted
   navigation.
2. Dashboard shows scoped operational summary and triage, not administrative
   configuration health.
3. The user opens schedules, inspections, projects, reports, or notifications
   with persistent filters.
4. Allowed actions are available according to role: employees operate,
   managers publish/invalidate, viewers read only.
5. Inspection and project detail connect status, deadlines, evidence, origin,
   report, recapture, and timeline.
6. Failures preserve existing data and clearly identify potentially stale
   information.

### Customer journey

1. A customer viewer authenticates into the Dashboard customer audience.
2. Portfolio shows only authorized shared assets/projects and published status.
3. Timeline presents a customer-safe chronology.
4. Active published reports and explicitly shared evidence can be opened.
5. An invalidated report disappears immediately and the customer sees a safe
   unavailable state until a new publication exists.
6. Notifications link only to resources that remain authorized at click time.

### Capture journey

1. A participant opens a scoped invitation link and requests an OTP.
2. After verification, the participant reviews and accepts the current
   disclosure.
3. Capture shows one task at a time with requirement instructions and progress.
4. Media is captured/selected, uploaded, verified, and annotated with required
   metadata.
5. Offline and local-only states are visible and recoverable.
6. The participant reviews the exact evidence state and submits.
7. Capture shows server-confirmed completion or required attention.
8. A recapture journey shows only requested replacement items and preserves
   lineage.

### Accessibility and interaction requirements

- Meet WCAG 2.2 AA for contrast, semantics, keyboard operation, focus, error
  association, status announcements, and responsive reflow.
- Interactive targets are at least 44 by 44 CSS pixels.
- Focus order matches reading order and remains visible with at least a 3-pixel
  focus treatment in the Admin design direction.
- No state depends only on color.
- Native controls are preferred: buttons for actions, links for navigation,
  semantic tables, labels for fields, and accessible dialogs.
- Loading completion uses status announcements; urgent errors use alerts without
  moving focus unexpectedly.
- Tables retain captions and header associations when rendered as mobile record
  views.
- At 200% zoom, critical content and actions remain reachable without two-axis
  page scrolling.
- Destructive and high-impact actions show target, scope, reason, reversibility,
  and consequence before confirmation.
- Skeleton/loading states do not invent data and retain tenant/scope context.

## High-Level Technical Constraints

- `services/inspection/schema.graphqls` is the canonical product contract.
- Backend completion precedes frontend completion; compatibility with the
  current incomplete generated clients is not a product requirement because the
  product is not in production.
- Backend operations remain tenant-isolated and preserve the repository's
  established Vertical Slice Architecture boundaries.
- Existing PostgreSQL row-level tenant protections remain authoritative.
- OIDC/Keycloak remains the internal authentication boundary; Capture retains
  link/OTP external sessions with CSRF and scoped responsibility checks.
- The accepted fixed cross-environment Admin credential is supplied only through
  identity-provider/deployment secret configuration and must never be embedded
  in source or distributable artifacts.
- Original and evidence media remain in authorized object storage with integrity
  digests, immutable lineage, and time-limited access.
- Email uses the configured mail provider; SMS and WhatsApp use configured
  providers through the existing channel abstraction. Provider secrets and
  private errors never reach product responses.
- Mutations remain idempotent and optimistic-concurrency-aware. Context and
  correlation references propagate through observable work.
- Background scheduling, delivery, report rendering, analysis, and purge remain
  externally observable through product states; the UI does not claim completion
  before server confirmation.
- GraphQL-generated backend and frontend files are regenerated and verified
  whenever the schema changes.
- Customer projections use explicit safe view models and field allowlists.
- No code or dependency is added to the removed Contract Service.
- The design system is product-neutral and shares tokens/primitives without
  forcing Admin, Dashboard, and Capture into one shell.
- User-facing collections must remain usable at zero, typical, and 100-times
  typical data volume through server-backed search, filtering, and pagination.

## Non-Goals (Out of Scope)

- A cross-tenant business-data console or aggregate customer search for the
  platform super administrator.
- A generic GraphQL explorer, raw operation runner, or one page per query and
  mutation.
- Reusing one navigation shell across Admin, Dashboard, and Capture.
- Full complex Admin workflows optimized for 320-pixel mobile screens; mobile
  Admin is limited to consultation and simple actions.
- Shipping English or another translated interface in this initiative; the
  product is localization-ready but pt-BR only.
- Customer access to internal notes, preliminary analysis, unapproved evidence,
  sensitive metadata, or invalidated publications.
- Automatic fallback to an older report after invalidation.
- Ordinary hard-delete controls for participants, catalogs, assets, origins,
  inspections, reports, or policies.
- Bulk mutation of permissions, roles, publication, retention, legal holds,
  deletion, or report lifecycle.
- Guaranteed background upload while Capture is closed.
- Keeping `apps/web` as a permanent fourth frontend.
- Reintroducing or adding code to the removed Contract Service.
- Preserving compatibility with the current incomplete frontend contracts before
  the coordinated regeneration, because the product is not in production.

## Architecture Decision Records

- [ADR-001: Full Capability Parity and Backend-First Delivery](adrs/ADR-001-full-capability-parity-and-delivery-order.md) — Complete the backend contract first, map every operation to a journey, and remove the migrated legacy frontend.
- [ADR-002: Admin Product Boundary and Open Design Direction](adrs/ADR-002-admin-product-and-design-direction.md) — Make the Open Design scoped B2B console the mandatory Admin baseline while keeping each frontend distinct.
- [ADR-003: Tenant Isolation, Super Admin Memberships, and Delegated Administration](adrs/ADR-003-tenant-isolation-and-delegated-administration.md) — Preserve tenant isolation, auto-provision super admin memberships, and introduce six delegated roles.
- [ADR-004: Versioned Resource Lifecycle, Origin Provenance, and Retention](adrs/ADR-004-versioned-lifecycle-origin-and-retention.md) — Use archive/version/soft-delete semantics, dual origin intake, legal holds, automatic purge, and non-sensitive tombstones.
- [ADR-005: Customer Publication, Evidence, and Communication Policy](adrs/ADR-005-customer-publication-and-communication-policy.md) — Restrict customer access to explicitly published content and complete email, SMS, and WhatsApp delivery behavior.
- [ADR-006: Fixed Cross-Environment Super Admin Credential](adrs/ADR-006-fixed-cross-environment-super-admin-credential.md) — Record the accepted security exception without placing credential material in artifacts.

## Open Questions

No unresolved product-direction questions remain from brainstorming.

The following numeric and provider-specific boundaries belong to the subsequent
technical specification because they do not change product scope or behavior:

- Maximum upload size and supported media formats per capture requirement.
- Maximum bulk-import rows/file size and export retention duration.
- Maximum selectable page size above the canonical default of 25.
- Supported report download kinds beyond PDF.
- Provider-specific retry schedules and delivery callback windows.
- Tenant-selected retention day values beyond the exact validation invariants in
  this PRD.
