# Capability Matrix: Canonical GraphQL to Product Journeys

This matrix is the parity contract for the schema that existed when the PRD was
created on 2026-09-08. GraphQL field names are canonical. “Current state” refers
to the standalone Admin, Dashboard, and Capture applications, not legacy
`apps/web` documents.

Every row must end in one of these verified outcomes:

- **User journey** — a permitted persona can complete and inspect the business
  outcome in the owning frontend.
- **Internal journey step** — the operation is exercised by a user journey but
  is intentionally not exposed as a standalone action.
- **Explicitly retired** — a later accepted ADR removes the capability and marks
  the row retired without deleting its history.

## Queries

| # | Canonical query | Owner | Current state | Required product outcome |
| ---: | --- | --- | --- | --- |
| 1 | `me` | Shared access gate | Used | All frontends resolve identity, product, roles, memberships, and effective scopes before loading protected data. |
| 2 | `tenant` | Admin; shared context | Used partially | Admin manages full tenant detail; every protected frontend displays the active tenant safely. |
| 3 | `memberships` | Admin | Used as generic data | Searchable membership collection and detail with roles, scopes, status, version, and tenant context. |
| 4 | `businessUnits` | Admin | Used as generic data | Complete unit collection with filters, pagination, detail, create/update/archive lifecycle, and impact. |
| 5 | `auditEvents` | Admin | Used as generic data | Filterable, paginated, exportable audit product with event detail and safe before/after context. |
| 6 | `participants` | Admin | Used as generic data | Searchable/filterable participant collection, bulk import/export, lifecycle, contacts, and assignments. |
| 7 | `participant` | Admin | Missing UI | Participant detail with contacts, selected destinations, scope, version, relationships, and history. |
| 8 | `segmentDefinitions` | Admin | Missing UI | Segment collection with active version, search, versions, publication, activation, and retirement state. |
| 9 | `templates` | Admin | Missing UI | Template collection with search, active version, dependencies, publication, activation, and retirement state. |
| 10 | `templateVersion` | Admin | Missing UI | Readable requirement/policy preview and advanced source view for one immutable version. |
| 11 | `assets` | Admin | Used as generic data | Searchable/filterable asset collection with assignments, import/export, lifecycle, and origin status. |
| 12 | `asset` | Admin | Missing UI | Complete asset detail, relationships, origin lineage, version, policy overrides, and history. |
| 13 | `originVersions` | Admin | Missing UI | Paginated origin lineage with source/provenance, active/invalid state, media availability, and audit. |
| 14 | `schedules` | Dashboard | Missing UI | Operational schedule collection with due/status filters and create/update/cancel journeys. |
| 15 | `projects` | Dashboard | Missing UI | Project collection with status, scope, asset, participant, stage progress, and pagination. |
| 16 | `project` | Dashboard | Partial ID detail | Complete project detail with stages, transitions, inspections, report context, and allowed actions. |
| 17 | `inspections` | Dashboard | Missing UI | Inspection collection/history with filters, status, source, deadlines, project, classification, and pagination. |
| 18 | `inspection` | Dashboard | Partial detail | Complete inspection workspace connecting evidence, origin, project/stage, report, publication, and recapture. |
| 19 | `report` | Dashboard | Partial ID lookup | Versioned readable report detail with canonical/rendered views, analysis provenance, and publication history. |
| 20 | `reportDownload` | Dashboard | Missing UI | Observable preparation, integrity digest, supported kind, and authorized time-limited download. |
| 21 | `dashboardSummary` | Dashboard | Used | Scoped summary whose counts match active filters and expose freshness. |
| 22 | `triageInspections` | Dashboard | Used partially | Filtered, paginated priority queue linked to complete inspection workspaces. |
| 23 | `projectTimeline` | Dashboard | Missing UI | Ordered stage/inspection timeline with state, occurrence time, and navigable relationships. |
| 24 | `notificationDeliveries` | Admin | Missing UI | Delivery collection with intent, channel attempts, partial failure, retry state, and related resource. |
| 25 | `publicationPolicy` | Admin | Used as generic data | Governance form/detail with restrictive default, version, scope, customer effect, and history. |
| 26 | `customerPortfolio` | Dashboard customer | Placeholder/partial | Real authorized customer-safe portfolio with filters, progress, published classification, and pagination. |
| 27 | `customerTimeline` | Dashboard customer | Placeholder | Real customer-safe chronological projection with no internal notes or hidden state. |
| 28 | `customerReport` | Dashboard customer | Placeholder | Active policy-approved report with safe unavailable/invalidated behavior. |
| 29 | `customerEvidence` | Dashboard customer | Placeholder | Policy-approved evidence allowlist, lineage, availability, pagination, and time-limited media access. |
| 30 | `myNotifications` | Dashboard | Used partially | Paginated notification center with unread count, authorized resource links, and read state. |
| 31 | `retentionPolicies` | Admin | Missing UI | Current policy/history plus legal-hold, deletion, purge eligibility/progress, and tombstone entry points. |
| 32 | `usageSummary` | Admin | Missing UI | Period-filtered requests, tokens, cost, freshness, timezone, and authorized export context. |
| 33 | `externalCapture` | Capture | Used | Link-scoped origin/inspection/recapture bootstrap with policy, reference, requirements, answers, and states. |

## Mutations

| # | Canonical mutation | Owner | Current state | Required product outcome |
| ---: | --- | --- | --- | --- |
| 1 | `createTenant` | Admin onboarding | Local bootstrap only | Idempotent tenant bootstrap with initial unit and automatic super-admin membership. |
| 2 | `updateTenant` | Admin | Missing UI | Version-safe tenant name/language/timezone update with audit. |
| 3 | `createBusinessUnit` | Admin | Missing UI | Guided unit creation with tenant impact and current tenant version. |
| 4 | `upsertBusinessUnit` | Admin | Missing UI | Version-safe unit update; creation usage must not duplicate the dedicated create journey. |
| 5 | `archiveBusinessUnit` | Admin | Missing UI | Impact-aware archive that preserves history and removes active selection. |
| 6 | `inviteInternalUser` | Admin | Missing UI | Internal invitation with approved role/scope and channel-visible status. |
| 7 | `assignRoleScopes` | Admin | Missing UI | Delegated role/scope assignment with effective-access preview and stale-write protection. |
| 8 | `disableMembership` | Admin | Missing UI | Immediate access disable with target/scope confirmation and audit. |
| 9 | `upsertParticipant` | Admin | Missing UI | Participant create/update journey with contacts, scope, version, and import integration. |
| 10 | `verifyContact` | Admin | Missing UI | Explicit contact verification state change with permission and channel context. |
| 11 | `setDeliveryChannels` | Admin | Missing UI | Select only active verified email/SMS/WhatsApp destinations using current participant version. |
| 12 | `publishSegmentDefinition` | Admin | Missing UI | Publish immutable segment version after readable schema validation/preview. |
| 13 | `activateSegmentDefinition` | Admin | Missing UI | Activate one compatible segment version for future use without rewriting history. |
| 14 | `publishTemplateVersion` | Admin | Missing UI | Publish immutable template version from structured requirements and preview. |
| 15 | `activateTemplateVersion` | Admin | Missing UI | Activate one compatible template version after dependency/impact review. |
| 16 | `publishAnalysisProfile` | Admin | Missing UI | Publish immutable analysis profile through a discoverable profile lifecycle. |
| 17 | `registerAsset` | Admin | Missing UI | Asset registration with configuration, assignments, policy overrides, and import parity. |
| 18 | `updateAsset` | Admin | Missing UI | Version-safe asset update with dependency impact. |
| 19 | `archiveAsset` | Admin | Missing UI | Archive asset, prevent new work, and preserve historical work. |
| 20 | `requestInvitationOtp` | Capture | Used | Internal journey step with safe rate-limit and delivery feedback. |
| 21 | `verifyInvitationOtp` | Capture | Used | Internal journey step that establishes a two-hour scoped external session. |
| 22 | `revokeInvitation` | Capture | Missing UI | Public participant decline/revoke journey; never reused as the Admin revoke contract. |
| 23 | `inviteOriginCapture` | Admin | Missing UI | Origin invitation with asset, participant, expiry, delivery status, and origin version tracking. |
| 24 | `activateOriginVersion` | Admin | Missing UI | Make one verified immutable origin version the future comparison reference. |
| 25 | `invalidateOriginVersion` | Admin | Missing UI | Invalidate future use with visible reason/history while preserving the original. |
| 26 | `createSchedule` | Dashboard | Missing UI | Schedule creation with recurrence preview, timezone, deadline, reminders, and dependencies. |
| 27 | `updateSchedule` | Dashboard | Missing UI | Version-safe recurrence/deadline/reminder update with next-due preview. |
| 28 | `cancelSchedule` | Dashboard | Missing UI | Stop future generation and retain historical inspections. |
| 29 | `createInspection` | Dashboard | Missing UI | Create scoped manual inspection with source reason, due/deadline/reminders, and references. |
| 30 | `cancelInspection` | Dashboard | Used partially | Contextual version-safe transition with downstream impact. |
| 31 | `invalidateInspection` | Dashboard | Missing UI | Reasoned invalidation with report/publication consequences and audit. |
| 32 | `createProject` | Dashboard | Missing UI | Project creation with asset, participant, template, report mode, and generated stages. |
| 33 | `addExceptionalStage` | Dashboard | Missing UI | Add a reasoned exceptional stage at an understandable position/time. |
| 34 | `startProjectStage` | Dashboard | Missing UI | Start eligible stage, optionally create/link inspection, and enforce project/stage versions. |
| 35 | `skipProjectStage` | Dashboard | Missing UI | Reasoned skip only where the stage lifecycle permits it. |
| 36 | `closeProject` | Dashboard | Missing UI | Close an eligible project after impact/prerequisite review. |
| 37 | `reopenProject` | Dashboard | Missing UI | Reopen a closed project with reason and complete transition history. |
| 38 | `acceptProcessing` | Capture | Used | Consent step bound to exact disclosure version and explicit processing choices. |
| 39 | `createMediaUpload` | Capture | Used | Internal upload step with type, size, digest, expiry, and resumable identity. |
| 40 | `presignMediaParts` | Capture | Used | Internal step that requests only needed parts for the current scoped media. |
| 41 | `completeMediaUpload` | Capture | Used | Idempotent internal step that verifies completed parts and server receipt. |
| 42 | `saveCaptureMetadata` | Capture | Used | Save requirement, description, source, GPS state, and allowed device context after upload. |
| 43 | `declareCaptureImpossibility` | Capture | Used | Reasoned requirement outcome only when the immutable template permits it. |
| 44 | `submitCapture` | Capture | Used | Submit complete or explicitly confirmed incomplete evidence and show server outcome. |
| 45 | `requestRecapture` | Dashboard | Missing UI | Targeted manager request with requirement/media lineage, reason, deadline, and delivery. |
| 46 | `submitRecapture` | Capture | Used | Submit requested replacements and preserve lineage/status. |
| 47 | `declareSensitiveDetectionFalsePositive` | Capture | Missing UI | Reasoned participant declaration that remains reviewable and audited. |
| 48 | `configureRetentionPolicy` | Admin | Missing UI | Governance form for evidence/operational/security periods with current state and impact. |
| 49 | `recordDeletionRequest` | Admin | Missing UI | Admin-only immediate soft delete that starts governed retention/purge state. |
| 50 | `applyLegalHold` | Admin | Missing UI | Reasoned hold that blocks purge and becomes queryable/auditable. |
| 51 | `releaseLegalHold` | Admin | Missing UI | Reasoned release that recomputes purge eligibility and remains auditable. |
| 52 | `configurePublicationPolicy` | Admin | Missing UI | Version-safe restrictive customer publication/evidence policy. |
| 53 | `publishReport` | Dashboard | Used partially | Manager publishes one valid snapshot and triggers policy-controlled customer access/delivery. |
| 54 | `invalidateReportPublication` | Dashboard | Missing UI | Immediate customer withdrawal, no prior-version fallback, reason, audit, and notification. |
| 55 | `markNotificationRead` | Dashboard | Used | Idempotent read state and unread-count update. |
| 56 | `configureMyNotificationPreferences` | Dashboard | Used as raw IDs | Human-readable verified destination preferences with current-version conflict handling. |

## Required Backend Capabilities Not Yet Represented Adequately

Names below describe product capabilities. Final GraphQL field names and shapes
are decided in the TechSpec, but each capability is mandatory.

| Capability gap | Primary owner | Required behavior |
| --- | --- | --- |
| Tenant membership selector summary | Shared/Admin | Resolve names and statuses only for tenants where the identity has memberships; never provide cross-tenant business data. |
| Analysis profile collection/detail/version/activation/retirement | Admin | Make the existing publication mutation part of a complete discoverable lifecycle. |
| Effective-access explanation and comparison | Admin | Explain allow/deny, source grant, role, scope, inheritance, validity, redundancy, and conflicts. |
| Administrative invitation collection/resend/revoke | Admin | Manage internal and origin invitations without exposing or requiring public link tokens. |
| Administrative origin upload | Admin | Upload immutable source media with `ADMIN_UPLOAD` provenance and required justification metadata. |
| Participant archive/soft-delete status | Admin | Remove from active assignment while preserving history and retention state. |
| Segment/template/profile retirement status | Admin | Prevent future use without rewriting immutable historical references. |
| Current recipient channels and notification preferences | Dashboard | Query human-readable verified destinations, selection, mandatory notices, and version. |
| Notification delivery detail and retry | Admin | Inspect per-channel attempts and retry only eligible failed destinations. |
| Report publication collection/current/history | Dashboard | Resolve publication IDs, versions, active state, invalidation history, and customer effect. |
| Retention/deletion/legal-hold/purge status | Admin | Query current requests, holds, eligibility, progress, result, and tombstone. |
| Filtered audit and usage export | Admin | Export authorized current filters with observable generation state. |
| Participant and asset import preview/apply/status | Admin | Validate before apply, report every row, preserve idempotency, and support recovery. |
| Filtered administrative collection export | Admin | Export authorized fields using active filters and tenant timezone. |
| Complete customer projections | Dashboard customer | Replace empty/null placeholder resolvers with policy-approved portfolio, timeline, report, and evidence data. |
| Resource lifecycle extensions | Admin | Add archive/deactivate/retire operations where current publish/upsert-only contracts cannot express the accepted lifecycle. |
| Soft-delete and purge tombstone filtering | Admin/Audit | Exclude deleted data from active lists while exposing authorized governed history and non-sensitive tombstones. |

## Frontend Ownership Summary

| Frontend | Owns | Does not own |
| --- | --- | --- |
| Admin | Tenant configuration, organization, memberships, delegated access, participants, catalogs, assets, origins, policies, retention, audit, usage, administrative imports/exports | Operational queues, inspection execution, customer portal, external capture |
| Dashboard | Schedules, inspections, projects/stages, triage, reports, publication/invalidation, recapture request, customer portfolio/results, notifications/preferences | Tenant/catalog governance, origin capture execution, external evidence submission |
| Capture | Public invitation/OTP, processing consent, origin/inspection/recapture evidence, upload, metadata, impossibility, submission, public revoke, false-positive declaration | Tenant administration, operational triage, report publication, customer portfolio |

## Parity Gate

Parity is achieved only when:

1. All 33 query rows and 56 mutation rows have fresh journey evidence.
2. All mandatory backend gaps above are represented in the completed contract
   and in at least one accepted user journey.
3. No customer projection remains an empty/null placeholder.
4. No standalone frontend displays raw GraphQL JSON as the primary product UI.
5. `apps/web` has no unique valid behavior or runtime/build/deployment consumer
   and can be removed under `US-033`.
