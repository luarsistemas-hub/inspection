# Product Requirements Document: Three Frontend Experience

## Overview

Inspection currently delivers tenant administration, operational review, customer visibility, and invitation-scoped evidence capture through one web application. These responsibilities compete for navigation, attention, and disclosure rules even though they serve fundamentally different users.

This initiative separates the existing product into three independent frontend products:

- **Admin** gives tenant administrators a focused place for registrations, permissions, structural configuration, policies, and governance.
- **Dashboard** gives internal users and permissioned customers a role-adaptive place to follow inspection evolution, operate authorized workflows, review evidence, and access published results.
- **Capture** gives invitation recipients a focused mobile journey for origin evidence, inspection evidence, and directed recapture without requiring a Dashboard account.

The separation preserves existing inspection capabilities and domain guarantees. It adds only the access, publication, notification, and presentation behavior required to make the three products coherent. The products share one brand and interaction vocabulary but use different layouts and disclosure boundaries appropriate to their audiences.

## Goals

- Tenant administrators can complete all structural registration and governance work in Admin without navigating operational review screens.
- Internal managers and employees can operate inspections and prioritize pending review work in Dashboard without entering Admin.
- Internal viewers can inspect authorized operational data without seeing mutation controls.
- Invited customers and property owners can use Dashboard accounts to follow only explicitly shared assets, projects, inspections, evidence history, and published results.
- Customers see an asset- and project-centered timeline by default, while internal users see risk, status, and pending work appropriate to their role.
- A user entitled to Admin and Dashboard can move between them with sign-in continuity while each product enforces independent authorization.
- Capture participants can complete origin, inspection, and recapture responsibilities through link and OTP without gaining Dashboard or Admin access.
- A completed Capture submission is immutable; every later correction uses a new recapture and preserves prior evidence.
- Each tenant can choose manual or automatic customer report publication, with manual publication as the default.
- Customers can start with a simplified published result and opt into advanced evidence lineage without receiving blocked sensitive media.
- Relevant customer progress and publication changes generate safe in-product and configured-channel notifications.
- Existing backend behavior, historical snapshots, evidence lineage, tenant isolation, and advisory AI language remain intact.

## User Stories

- `US-001`: independent product access, shared sign-in continuity, and safe cross-product transitions.
- `US-002`–`US-005`: tenant configuration, hierarchical access, structural catalogs, and governance in Admin.
- `US-006`–`US-009`: role-adaptive home, operations, review, and read-only access in Dashboard.
- `US-010`–`US-012`: customer timeline, published evidence visibility, and safe notifications.
- `US-013`–`US-018`: scoped Capture access, origin, inspection, recapture, resilience, and immutable submission.

[Full user stories](_user_stories.md)

## Core Features

### 1. Three Independent Product Experiences

Admin, Dashboard, and Capture are distinct user-facing products with their own entry points and product identity. Each can evolve and be released independently. Shared brand, language, accessibility principles, and safe design primitives make the suite feel coherent without forcing identical navigation or page density.

Admin and Dashboard support sign-in continuity for identities entitled to both. Authorization remains independent: access to Dashboard never grants Admin access, and access to one tenant never grants access to another. Capture does not reuse Admin or Dashboard entitlement and never acts as a customer result portal.

### 2. Tenant Administration and Structural Registration

Admin owns tenant-level configuration and governance:

- tenant identity, language, timezone, defaults, and business units;
- internal users, customer users, roles, memberships, and hierarchical scopes;
- participants, verified delivery channels, and invitation eligibility;
- segments, templates, analysis profiles, assets, relationships, locations, and policy overrides;
- publication policy, notification settings, retention policy, privacy state, and administrative audit review.

Admin does not own day-to-day inspection operation. Schedules, occurrences, project stages, cancellation, invalidation, review, publication actions, and recapture requests belong in Dashboard when the current user has permission.

Configuration screens distinguish inherited defaults from explicit overrides and distinguish mutable current configuration from immutable historical snapshots. Actions that alter effective access or future behavior show their impact before confirmation.

### 3. Invitation-Only Hierarchical Dashboard Access

Dashboard has no public self-registration. A tenant administrator invites each internal or customer user and assigns a role plus at least one scope. Scope may start at a business unit, asset, project, or inspection. A grant includes only permitted descendants within the same tenant.

Admin previews effective access before saving and explains whether access is direct or inherited. Revocation takes effect immediately. A resource move or lifecycle change never silently broadens customer access.

Customer and property-owner roles are read-only. They cannot access Admin, unpublished reports, internal notes, hidden analysis state, or operational mutation controls. Internal roles retain the existing scoped operational permissions.

### 4. Role-Adaptive Dashboard

Dashboard changes its home, navigation, terminology, and available actions based on effective role and scope rather than rendering one universal page with disabled controls.

Internal managers receive a risk- and work-oriented view that prioritizes critical, attention, overdue, pending, and recently changed inspections. Internal employees receive their assigned operational work. Internal viewers receive scoped read-only review. Customers and property owners begin with permitted assets and projects.

The internal experience supports schedules, projects, stages, inspections, triage, evidence comparison, findings, report history, notifications, and operational actions already supported by the platform. Accepted asynchronous work visibly distinguishes accepted, pending, completed, and failed states.

### 5. Customer Asset and Project Timeline

The customer experience is centered on shared assets and projects rather than tenant administration or a raw inspection table. Each timeline presents relevant inspections, milestones, progress, recapture state, publication state, and published report history in chronological context.

Safe progress may be shown before publication, but result content, internal notes, analysis details, and evidence remain hidden until the applicable report is published. Empty and first-use states explain why no inspections or reports are available.

### 6. Tenant-Controlled Report Publication

Each tenant chooses one customer publication policy in Admin:

- **Manual publication** is the default. Final reports remain internal until an authorized internal user explicitly publishes them.
- **Automatic publication** makes every report customer-visible when it becomes final, including critical and inconclusive reports.

A policy change applies to reports finalized after the change. Existing final unpublished reports remain unchanged until explicitly published. Every published report is immutable and attributable to its exact version. Critical and inconclusive reports preserve their classification and prominently explain that findings and recommended actions are advisory.

### 7. Simplified and Advanced Customer Evidence Review

Published reports open in simplified mode. This mode emphasizes current outcome, key evidence, progress context, advisory interpretation, and the current report version.

Customers may enable advanced mode to inspect all evidence records permitted by their resource scope, including original, replacement, superseded, and discarded lineage. Advanced mode groups evidence by requirement, stage, and version so that replacement history remains understandable.

Evidence blocked for possible sensitive content is never visually disclosed to customers in either mode. Dashboard shows only safe metadata, state, time, and reason for the block. Internal visibility continues to follow existing authorization and sensitive-content policy.

### 8. Customer Progress Notifications

Customers receive notifications for meaningful changes affecting resources they can access, including relevant inspection progress, recapture state, completion, and report publication. Notifications appear inside Dashboard and use verified configured channels selected for the customer.

Notification previews include only safe status and navigation context. They never contain evidence files, internal notes, detailed findings, reusable access credentials, or information about resources outside current access. Opening a notification always rechecks current authorization.

### 9. Focused Invitation-Scoped Capture

Capture supports origin capture, inspection capture, and directed recapture. Each journey begins with an individual expiring link and OTP, then shows only the assigned responsibility after required disclosure and consent.

The experience is mobile-first and requirement-driven. It presents instructions, reference context where allowed, descriptions, checklists, camera or gallery policy, GPS and geofence expectations, quality state, sensitive-content state, upload progress, impossibility reasons, and complete or permitted-incomplete submission behavior.

Capture preserves recoverable pending progress through refresh and connectivity loss. Final verification and submission require connectivity. The product never claims success before the server accepts the completed responsibility.

After successful submission, Capture displays confirmation only. The participant cannot browse results or alter submitted evidence. A later correction creates a new scoped recapture that shows only requested items and preserves the original lineage.

## Business Rules

### Product and Identity Rules

1. Admin, Dashboard, and Capture are three independent products with distinct entry points, session boundaries, and release lifecycles.
2. All three products use the same Inspection brand, core terminology, initial `pt-BR` language, and accessibility standard.
3. Admin and Dashboard may reuse authentication continuity, but each product independently evaluates entitlement, tenant, role, and scope.
4. A user's access to Dashboard never implies access to Admin.
5. Capture authorization never grants access to Admin, Dashboard, or a different capture responsibility.
6. Every visible product surface identifies the current product and tenant or responsibility context.

### Admin Rules

1. Only a tenant administrator may access Admin.
2. Every administrator is restricted to their own tenant; this initiative introduces no global platform-superadministrator product.
3. Admin owns structural registration, access grants, configuration, policies, and governance but not routine inspection operation.
4. A customer or internal user receives Dashboard access only after an administrator invitation; public self-registration is prohibited.
5. Every Dashboard membership has an explicit role and at least one effective resource scope before data becomes visible.
6. Administrative changes never rewrite immutable historical inspection, evidence, analysis, or report versions.
7. Effective defaults and overrides must be distinguishable before an administrator saves a change.

### Dashboard Permission Rules

1. Dashboard supports internal managers, internal employees, internal viewers, customers, and property owners with role-appropriate experiences.
2. Access may be granted at business-unit, asset, project, or inspection scope and includes only authorized descendants in the same tenant.
3. Multiple grants combine only inside the same tenant; no combination may produce cross-tenant access.
4. Revocation takes effect immediately for subsequent views, downloads, and actions, including already-open pages.
5. Customer and property-owner roles are read-only.
6. Internal operational actions remain subject to existing role, resource scope, lifecycle, reason, and audit requirements.
7. Hidden controls are not an authorization mechanism; direct attempts outside permission must fail without disclosing resource existence.
8. A user denied one product or resource receives a safe recovery route to an allowed context.

### Publication and Customer Visibility Rules

1. Manual customer publication is the default tenant policy.
2. In manual mode, only an authorized internal user may publish a final report.
3. In automatic mode, every final report becomes published, including critical and inconclusive reports.
4. A policy change affects future report finalizations; it does not retroactively publish existing reports.
5. Customers may see safe progress before publication but may not see result content, evidence, or internal analysis before publication.
6. A published report exposes exactly one immutable report version and labels historical versions clearly.
7. Critical and inconclusive reports retain their actual classification and state that analysis is advisory.
8. Customers see simplified report and evidence presentation by default.
9. Advanced mode may show original, replaced, superseded, and discarded evidence records within permission scope.
10. A visual file blocked for possible sensitive content is never displayed to a customer; safe metadata and the block reason may be displayed.
11. Unpublished reports, internal notes, model traces, provider details, and hidden review material are never customer-visible.

### Customer Notification Rules

1. Relevant progress and publication events produce an in-product customer notification and notifications through selected verified channels.
2. Notification content contains only safe status and navigation context.
3. Every notification destination checks current authorization when opened.
4. Revoked access invalidates the destination even when the notification was sent earlier.
5. One business event produces at most one notice per selected channel and one in-product notification for each intended recipient.
6. Provider failure does not change Dashboard truth and remains visible to authorized administrators.

### Capture and Evidence Rules

1. Each Capture session accesses exactly one active origin, inspection, or recapture responsibility.
2. Possession of a link is insufficient; valid OTP and required consent precede responsibility access.
3. Capture never exposes internal findings, classification, reports, tenant configuration, or unrelated evidence.
4. Pending evidence and progress may be preserved locally, but final verification and submission require connectivity.
5. Capture never claims submission success before authoritative acceptance.
6. Successful submission makes the evidence set immutable and closes the responsibility session.
7. A participant cannot reopen or alter a submitted responsibility.
8. Every correction uses a new directed recapture, invitation, OTP, and session.
9. Recapture exposes only selected requirements and preserves prior evidence and replacement lineage.
10. Successful submission ends in confirmation-only presentation.
11. Existing media type, size, quality, GPS, geofence, sensitive-content, completeness, and maximum-evidence policies remain in force.

### Consistency and History Rules

1. The same tenant, asset, project, inspection, evidence, and report state must have consistent meaning across all three products.
2. Product separation must not duplicate or fork authoritative business records.
3. Resource lifecycle changes remain visible historically according to permission but cannot be selected for prohibited new work.
4. Repeated or interrupted user actions must not create duplicate memberships, invitations, submissions, publications, notifications, or evidence lineage.
5. Concurrent changes must reject stale state rather than silently overwriting a newer authorized decision.

## User Experience

### Shared Product Family

The products share typography, color semantics, component behavior, Portuguese terminology, focus treatment, status language, and recognizable Inspection branding. Product-specific shells make the current context obvious. Users with both Admin and Dashboard access have an explicit product switcher that preserves sign-in continuity without implying authorization.

All products support keyboard operation, screen readers, visible focus, readable contrast, non-color-only status, clear error recovery, and responsive layouts appropriate to their use. Sensitive or irreversible actions explain their consequence before confirmation.

### Admin Journey

1. A tenant administrator signs in and lands on a configuration-oriented overview.
2. The administrator confirms tenant identity, defaults, and business-unit structure.
3. The administrator registers participants, catalogs, templates, profiles, and assets.
4. The administrator invites internal or customer users, assigns roles and hierarchical scopes, and previews effective access.
5. The administrator configures report publication, customer notification, retention, and other governance policies.
6. The administrator reviews administrative audit history and delivery limitations.
7. When operational work is required, the administrator switches to Dashboard.

Admin favors predictable navigation, searchable tables, explicit forms, effective-access previews, clear inheritance, audit context, and deliberate confirmations. It avoids risk charts and inspection-review content as its primary navigation model.

### Internal Dashboard Journey

1. An internal user signs in and receives a home adapted to role and scope.
2. Managers see critical, attention, overdue, pending, and recently changed work first.
3. The user filters by permitted unit, asset, project, period, status, classification, or flag.
4. The user opens an inspection or project to inspect progress, comparisons, findings, evidence lineage, and report history.
5. When authorized, the user creates or changes operational work, requests recapture, or publishes a final report.
6. Dashboard shows accepted, pending, completed, and failed processing states without ambiguous loading.

Dashboard favors status hierarchy, trends, timelines, actionable queues, saved context, drill-down, and side-by-side evidence review. Administrative configuration does not compete in its navigation.

### Customer Dashboard Journey

1. A customer receives an Admin-created invitation and completes account sign-in.
2. Dashboard opens on the customer's permitted assets and projects.
3. The customer selects an asset or project and follows chronological review progress.
4. Safe in-progress status appears without unpublished findings or evidence.
5. A publication notification deep-links to the currently authorized asset, project, inspection, or report.
6. The customer opens a published report in simplified mode.
7. When more detail is needed, the customer enables advanced mode to inspect permitted evidence lineage.
8. Blocked sensitive visuals remain unavailable and appear only as safe metadata records.

Customer Dashboard favors plain language, timeline comprehension, current status, publication state, and progressive disclosure. It does not imitate the internal operational console.

### Capture Journey

1. A contributor receives an individual link through configured channels.
2. Capture exchanges the link, removes the visible secret, validates OTP, presents disclosure, and records consent.
3. The participant sees only the assigned origin, inspection, or recapture requirements.
4. The journey guides one requirement at a time through reference context, camera or gallery, description, GPS, quality, sensitive-content state, and upload progress.
5. Pending work survives refresh and connectivity loss where existing guarantees allow.
6. Final review explains complete or permitted-incomplete status and the immutability of submission.
7. Submission requires connectivity and server acceptance.
8. Capture closes the responsibility and shows confirmation only.

Capture favors a mobile single-task flow, large touch targets, minimal navigation, explicit progress, permission explanations, recovery guidance, and no administrative or reporting concepts.

## High-Level Technical Constraints

- The initiative must integrate with the existing Inspection GraphQL business surface and preserve the backend as the authoritative source of tenant, access, inspection, evidence, analysis, report, notification, and audit state.
- Existing OIDC-compatible internal authentication remains the identity basis for Admin and Dashboard. Product entitlement and resource authorization remain server-enforced.
- Existing invitation link, OTP, scoped external session, CSRF, consent, and revocation guarantees remain mandatory for Capture.
- The three products must prevent credential, cache, state, media, report, or navigation context from crossing an unauthorized product or tenant boundary.
- Internal tokens, external credentials, protected responses, evidence, report content, and durable media URLs must not be exposed through persistent browser storage or public caching.
- Existing immutable media, analysis, report, and recapture lineage guarantees remain unchanged.
- Existing direct resumable media upload and server-side verification remain mandatory from the participant's perspective.
- Existing SMTP and Twilio-compatible channel integrations remain the customer-notification delivery options; delivery never becomes the source of truth for product state.
- Every product must support supported desktop and mobile browsers appropriate to its journey, with Capture optimized for current mobile browsers and intermittent connectivity.
- Tenant isolation, authorization denial, accessibility, responsive behavior, deep links, stale-state handling, and notification safety must be verifiable through public user surfaces.
- Initial product language remains Portuguese (Brazil), and dates and times use the tenant timezone with the existing default.

## Non-Goals (Out of Scope)

- Introducing new inspection types, analysis rules, report semantics, or domain workflows unrelated to the three-product separation.
- Creating a global platform-superadministrator frontend.
- Public Dashboard self-registration or anonymous report access.
- Giving customers operational mutation rights in Dashboard.
- Using Capture as a report, status, or evidence-review portal after submission.
- Allowing Capture participants to edit submitted evidence.
- Displaying sensitive-content-blocked visual files to customers, including in advanced mode.
- Replacing directed recapture with mutable evidence editing.
- Building native iOS or Android applications.
- Claiming full offline submission or server completion while disconnected.
- Adding a visual template editor.
- Replacing the current identity provider, messaging providers, storage model, analysis gateway, PDF behavior, or backend architecture solely because the frontends are separated.
- Redesigning domain behavior already accepted by the Autonomous Inspection Platform PRD unless this PRD explicitly changes customer access, publication, notification, or presentation.

## Architecture Decision Records

- [ADR-001: Separate the Product into Three Independent Frontend Experiences](adrs/adr-001.md) — Admin, Dashboard, and Capture become independent products with shared brand and bounded access.
- [ADR-002: Use a Permissioned Role-Adaptive Dashboard for Internal and Customer Users](adrs/adr-002.md) — invited users receive hierarchical scope and audience-specific Dashboard experiences.
- [ADR-003: Make Customer Publication Policy Tenant-Configurable](adrs/adr-003.md) — tenants choose manual or automatic publication, with manual as the default.
- [ADR-004: Keep Capture Submission Immutable and Separate from Result Review](adrs/adr-004.md) — Capture remains responsibility-scoped and corrections use new recapture lineage.

## Open Questions

None. Product scope, audience boundaries, customer access, publication policy, evidence visibility, notification behavior, Capture finality, and shared visual direction were resolved during PRD discovery.
