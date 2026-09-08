# Technical Specification: Three Frontend Experience

## Executive Summary

This change replaces the combined `apps/web` application with three independently
deployable Next.js products: Admin, Dashboard, and Capture. They remain in the
same repository but have independent npm projects, lockfiles, generated GraphQL
clients, Docker images, sessions, tests, and release pipelines. A private,
versioned npm package distributes brand tokens and UI primitives without creating
a workspace or runtime dependency between the products. The current Go service,
PostgreSQL database, workers, GraphQL endpoint, Keycloak realm, object storage,
and messaging infrastructure remain shared.

The backend gains explicit product entitlements, a least-privilege customer role,
runtime hierarchy resolution, audited report publication, customer-safe read
models, and recipient notifications. Admin and Dashboard use separate OIDC public
clients with shared Keycloak SSO; Capture retains invitation, OTP, CSRF, offline
draft, and multipart-upload behavior. Report snapshots and Capture submissions
remain immutable. Because the product is not in production, the migration does
not preserve old frontend URLs or previously issued capture links.

## System Architecture

### Component Overview

| Component | Location | Responsibility and boundary |
|---|---|---|
| Admin frontend | `apps/admin` | Tenant configuration, organization, memberships, entitlements, scopes, catalogs, assets, governance, and audit. Requires `ADMIN` entitlement and `TENANT_ADMIN`; contains no inspection-operation or Capture routes. |
| Dashboard frontend | `apps/dashboard` | Role-adaptive internal operations and customer portfolio/result review. Requires `DASHBOARD`; customer components consume only customer-safe GraphQL types. |
| Capture frontend | `apps/capture` | Public PWA for origin, inspection, and recapture responsibilities. Authenticates only with link token, OTP, external session cookie, and CSRF token; persists recoverable drafts in IndexedDB. |
| Design-system package | `packages/inspection-design-system` | Private npm package containing tokens, accessible primitives, icons, and product-family patterns. Contains no domain logic, GraphQL operations, auth, routing, or product pages. |
| GraphQL API | `services/inspection` | Canonical API and authorization boundary. Accepts trusted OIDC audiences or the existing external Capture session, then dispatches each operation to one vertical slice. |
| Access slices | `services/inspection/internal/features/access/*` and `internal/platform/auth` | Invitation lifecycle, product entitlements, role/scopes, effective-access explanation, current-state membership resolution, and product/resource authorization. |
| Publication slices | `services/inspection/internal/features/reports/*` | Tenant publication policy, manual publication, automatic publication at report finalization, invalidation/supersession, and customer-safe report projection. |
| Customer timeline slices | `services/inspection/internal/features/dashboard/customer_*` | Cursor-paginated asset/project history constrained by current entitlement and hierarchical scope. |
| Recipient notification slices | `services/inspection/internal/features/notifications/recipient_*` | Idempotent in-app fan-out, listing, unread count, and read state. External delivery stays in existing notification slices. |
| Identity invitation adapter | `services/inspection/internal/platform/keycloak` | Idempotently creates or resolves a Keycloak user and sends the required-action activation email. It is an I/O adapter behind a slice-local interface. |
| Existing outbox and consumers | `services/inspection/internal/features/messaging/*` | Durable report/customer event fan-out and retries. No frontend publishes directly to RabbitMQ. |

### Runtime and Data Flow

1. Admin uses the `inspection-admin` Keycloak client and Dashboard uses
   `inspection-dashboard`, both with Authorization Code and PKCE. An existing
   realm session provides SSO continuity, but each application receives a token
   for its own audience and holds it only in memory.
2. The API verifies issuer, expiry, and audience, resolves the current local
   membership on every protected request, and authorizes product, role, mutation
   intent, tenant, and canonical resource hierarchy before dispatching the slice.
3. A customer invitation is stored as `PENDING` with entitlements and explicit
   grants. An outbox consumer provisions/resolves the Keycloak subject and sends
   the activation email. A tenant membership becomes active only after the
   subject is bound; retries reuse the same invitation and subject.
4. Report finalization stores the immutable snapshot and the policy version used.
   Automatic policy creates a publication in the same transaction. Manual policy
   requires `publishReport` from an authorized Dashboard user.
5. Publication and safe progress events fan out through the outbox to external
   notification deliveries and idempotent recipient notification records.
6. Customer Dashboard queries dedicated allowlisted projections. A media URL is
   minted only after current access, publication, lineage, and sensitivity checks.
7. Capture exchanges link plus OTP for the existing short-lived external session,
   works from an invitation-scoped bootstrap, writes local drafts, uploads direct
   to private object storage, and finalizes an immutable submission.

### Frontend Route Ownership

| Product | Production origin | Local origin | Route families |
|---|---|---|---|
| Admin | `https://admin.<domain>` | `http://localhost:3000` | `/auth/callback`, `/tenants/[tenantId]`, `/organization`, `/access`, `/catalogs`, `/assets`, `/governance`, `/audit` |
| Dashboard | `https://dashboard.<domain>` | `http://localhost:3002` | `/auth/callback`, `/tenants/[tenantId]`, `/inspections`, `/projects`, `/triage`, `/portfolio`, `/reports`, `/notifications` |
| Capture | `https://capture.<domain>` | `http://localhost:3003` | `/capture/[linkToken]` only; confirmation replaces the form after final submission |

All return destinations are same-origin relative paths validated against owned
route families. There are no compatibility redirects from `apps/web`.

### Story-to-Component Mapping

| Story | Primary implementation components |
|---|---|
| US-001 | Admin/Dashboard PKCE clients, `me`, product authorizer, product switcher |
| US-002 | Admin organization screens and existing tenancy slices |
| US-003 | Admin access screens, invitation slices, entitlement store, hierarchy resolver, Keycloak adapter |
| US-004 | Admin catalog/asset screens and existing participant, segment, template, origin, and asset slices |
| US-005 | Admin governance screens, publication-policy slice, retention/audit queries, notification configuration |
| US-006 | Dashboard role router, existing summary/triage projections, scoped filters |
| US-007 | Dashboard operation screens and existing inspection, schedule, project, and recapture slices |
| US-008 | Dashboard triage/report screens, publication mutations, recapture flow |
| US-009 | Dashboard capability map and mutation-free viewer composition |
| US-010 | Customer portfolio and customer timeline slices/pages |
| US-011 | Customer report/evidence projection, publication ledger, guarded media URL issuer |
| US-012 | Recipient notification projection, polling client, existing external dispatcher |
| US-013 | Capture session client and existing invitation/OTP/bootstrap slices |
| US-014 | Capture origin mode, drafts/uploads, existing origin slices |
| US-015 | Capture inspection mode, requirements, drafts/uploads, submission slices |
| US-016 | Capture recapture mode and immutable replacement lineage |
| US-017 | Capture IndexedDB queue, multipart reconciliation, service worker |
| US-018 | Existing submit idempotency, immutable finality, confirmation-only bootstrap |

## Implementation Design

### Frontend Project Structure

Each application follows feature ownership rather than a global layer hierarchy:

```text
apps/<product>/
├── app/                         # route shells and route-level loading/error UI
├── src/auth/                    # product-specific session and PKCE code
├── src/features/<feature>/      # components, hooks, GraphQL docs, tests
├── src/graphql/generated.ts     # generated, never hand-edited
├── src/graphql/client.ts        # one product-specific transport
├── public/                      # product manifest/assets; Capture owns service worker
├── Dockerfile
├── package.json
└── package-lock.json
```

All three apps stay on the repository's pinned Next.js 15, React 19, TypeScript,
ESLint, Vitest, GraphQL Code Generator, and Playwright baselines. Admin and
Dashboard use the generated operation types with a small `fetch` transport and
feature-local query hooks; authorization state is never inferred from hidden UI.
Capture owns the existing IndexedDB and multipart modules. No app imports another
app, and only the exact published design-system package is shared.

Responsive contracts are product-specific: Admin is desktop-first but usable at
768 px; Dashboard supports 360 px through desktop with customer portfolio cards
and internal dense tables; Capture is touch-first at 320 px and preserves camera
and offline controls. WCAG 2.2 AA semantics, keyboard navigation, visible focus,
reduced motion, and Portuguese copy apply to all products.

### Core Interfaces

The primary authorization dependency is explicit about every decision dimension:

```go
type AuthorizationRequest struct {
    TenantID identity.ID
    Product  string
    Roles    []string
    Mutate   bool
    Resource *requestctx.Scope
}

type ProductAuthorizer interface {
    Authorize(context.Context, AuthorizationRequest) (requestctx.Principal, error)
}
```

Hierarchy resolution returns the grant responsible for a decision:

```go
type ScopeDecision struct {
    Allowed     bool
    DirectGrant requestctx.Scope
    Target      requestctx.Scope
}

type ScopeResolver interface {
    Resolve(context.Context, identity.ID, identity.ID,
        requestctx.Scope) (ScopeDecision, error)
}
```

Keycloak is isolated behind the customer-invitation slice boundary:

```go
type IdentityInvitationPort interface {
    ProvisionAndInvite(ctx context.Context, command InviteIdentity) (
        subject string, err error)
}

type InviteIdentity struct {
    Email, IdempotencyKey, RedirectURI string
}
```

Customer evidence is mapped through an allowlist, never through internal report
serialization:

```go
type CustomerEvidenceMapper interface {
    Map(ctx context.Context, snapshot database.ReportSnapshot,
        mode EvidenceMode) ([]CustomerEvidenceItem, error)
}

type MediaURLIssuer interface {
    IssueVisible(ctx context.Context, tenantID, mediaID identity.ID) (string, error)
}
```

Interfaces return typed application errors. GraphQL maps invalid input and stale
versions to `userErrors`; unauthenticated requests receive `UNAUTHENTICATED`,
forbidden product/role access receives generic `FORBIDDEN`, and out-of-scope or
cross-tenant resources resolve as `NOT_FOUND` to avoid existence disclosure.

### Data Models

New tables use UUIDs from `libs/identity`, tenant RLS, UTC timestamps, and explicit
indexes. Migrations preserve all existing names.

| Table/model | Required fields and constraints |
|---|---|
| `access.product_entitlements` | `id`, `tenant_id`, `membership_id`, `product` (`ADMIN`/`DASHBOARD`), `created_at`; unique `(tenant_id,membership_id,product)`; tenant FK and RLS. |
| `access.user_invitations` | `id`, `tenant_id`, normalized `email`, `kind` (`INTERNAL`/`CUSTOMER`), `role`, JSON-free normalized child rows for entitlements/scopes, `keycloak_subject`, `status` (`PENDING`/`PROVISIONED`/`ACTIVE`/`FAILED`/`REVOKED`), `version`, expiry and audit timestamps; idempotency unique per active tenant/email invitation. |
| `access.resource_scopes` | Existing table; accepts `BUSINESS_UNIT`, `ASSET`, `PROJECT`, `INSPECTION`. Explicit grants only. |
| `reports.publication_policies` | `id`, `tenant_id`, `mode` (`MANUAL`/`AUTOMATIC`), `version`, `created_at`, `updated_at`; one current row per tenant; absence means manual. |
| `reports.report_publications` | `id`, `tenant_id`, `snapshot_id`, `inspection_id`, `policy_version`, `client_mutation_id`, `status` (`PUBLISHED`/`INVALIDATED`/`SUPERSEDED`), actor, reason, `version`, published/invalidated timestamps; one active publication per inspection, unique snapshot publication, and unique tenant mutation key. |
| `notifications.recipient_channels` | `id`, `tenant_id`, `recipient_membership_id`, `channel`, protected destination, `verified_at`, `selected`, `version`, timestamps; unique membership/channel/destination, and only verified selected rows are externally deliverable. |
| `notifications.recipient_notifications` | `id`, `tenant_id`, `recipient_membership_id`, `event_id`, `kind`, safe title/body, resource kind/id, `created_at`, nullable `read_at`; unique `(tenant_id,recipient_membership_id,event_id,kind)` and cursor index. |

`access.memberships.role` adds `CUSTOMER_VIEWER`. Database checks prohibit an
`ADMIN` entitlement for that role. `requestctx.Principal` adds `Audience` and
`ProductEntitlements`. A disabled membership, inactive tenant, absent requested
product, or audience/product mismatch fails before any business query.

Report snapshots remain byte-for-byte immutable after creation and gain an
immutable nullable `publicationPolicyVersion` captured at finalization. The
finalization transaction records the effective policy version even in manual
mode; changing policy affects only
later finalizations. Publishing a newer snapshot supersedes the previous active
publication. Invalidating a publication removes content URLs from current
customer reads but retains status, timestamps, actor, reason, and replacement
reference where permitted.

Customer response models expose only:

- portfolio identity, safe status, progress, dates, and published classification;
- simplified finding summaries and approved recommendations;
- advanced evidence lineage identifiers, requirement labels, timestamps, state,
  replacement links, and `mediaAvailability`, including `SENSITIVE_BLOCKED`;
- short-lived URLs only for currently visible, non-sensitive media.

They never expose object keys, raw provider output, prompts, internal notes,
unpublished reports, raw `canonicalJSON`, raw HTML, internal recipient data, or
the existence of out-of-scope resources.

### GraphQL API Surface

`POST /graphql` remains the sole application endpoint and
`services/inspection/schema.graphqls` remains canonical. Existing Admin,
Dashboard-operation, and Capture operations retain their names and payload/error
conventions; their resolver authorization changes to include product and
hierarchical scope. Every list remains cursor-paginated with a maximum page size
of 100.

New or modified queries:

| Operation | Authorization | Contract |
|---|---|---|
| `me` (modified) | Authenticated Admin/Dashboard audience | Adds product entitlements and role-derived capabilities for the current active tenant; never trusts token roles. |
| `effectiveAccess(membershipId, first, after)` | `ADMIN` + `TENANT_ADMIN` | Returns each explicit grant and cursor-paginated effective descendants with `inheritedFrom`; unknown/cross-tenant membership is `NOT_FOUND`. |
| `scopePreview(kind, resourceId, first, after)` | `ADMIN` + `TENANT_ADMIN` | Previews canonical descendants before a grant; archived descendants are labeled historical and not grantable for new work. |
| `publicationPolicy` | `ADMIN` + `TENANT_ADMIN` | Returns current policy or synthetic manual default with version 0. |
| `customerPortfolio(filter, first, after)` | `DASHBOARD` + `CUSTOMER_VIEWER` | Returns scoped asset/project summaries only; invalid filters yield `userErrors` without widening scope. |
| `customerTimeline(assetId, projectId, first, after)` | `DASHBOARD` + `CUSTOMER_VIEWER` | Returns safe chronological progress entries for an authorized asset/project. |
| `customerReport(inspectionId, version)` | `DASHBOARD` + `CUSTOMER_VIEWER` | Returns the current or requested historically published safe report; an unpublished version is `null`, and invalidated metadata is returned without report content. |
| `customerEvidence(inspectionId, mode, first, after)` | `DASHBOARD` + `CUSTOMER_VIEWER` | `SIMPLE` returns current approved evidence; `ADVANCED` adds all lineage with sensitivity-safe availability. |
| `myNotifications(unreadOnly, first, after)` | `DASHBOARD` | Returns only current membership's notifications and unread count. |

New or modified mutations:

| Operation | Authorization | Success and documented failures |
|---|---|---|
| `inviteCustomerUser(input)` | `ADMIN` + `TENANT_ADMIN` | Creates/reuses one pending invite with `CUSTOMER_VIEWER`, `DASHBOARD`, and at least one valid scope. Invalid email/scope is `INVALID_INPUT`; duplicate active access returns the existing outcome; provider failure leaves retryable `PENDING`/`FAILED`. |
| `inviteInternalUser(input)` (modified) | `ADMIN` + `TENANT_ADMIN` | Accepts explicit product entitlements and all valid scope kinds; rejects `ADMIN` unless role is `TENANT_ADMIN`. |
| `assignMembershipAccess(input)` | `ADMIN` + `TENANT_ADMIN` | Atomically replaces role, entitlements, and explicit scopes using `expectedVersion`; stale writes return `CONFLICT`. |
| `configurePublicationPolicy(input)` | `ADMIN` + `TENANT_ADMIN` | Stores `MANUAL` or `AUTOMATIC` using `expectedVersion`; absence starts from version 0; stale writes return `CONFLICT`. |
| `publishReport(input)` | `DASHBOARD` + `MANAGER` within scope | Publishes one final snapshot and supersedes prior active publication; non-final/missing snapshot is `INVALID_STATE`; duplicate `clientMutationId` returns prior result. |
| `invalidateReportPublication(input)` | `DASHBOARD` + `MANAGER` within scope | Requires reason and `expectedVersion`; moves publication to `INVALIDATED`; repeated request returns current result, stale request is `CONFLICT`. |
| `markNotificationRead(input)` | `DASHBOARD`, own membership only | Sets `readAt` idempotently; unknown or another user's ID is `NOT_FOUND`. |
| `configureMyNotificationPreferences(input)` | `DASHBOARD`, own membership only | Selects only verified recipient channels with `expectedVersion`; in-app remains enabled, invalid/unverified channels are `INVALID_INPUT`, and prior deliveries are unchanged. |

Mutation payloads include the affected object, `userErrors`, and echoed
`clientMutationId`. The API accepts `Idempotency-Key` where the existing
transport contract requires it.

### Authorization Algorithm

For each protected operation, the API executes these checks in order:

1. verify token issuer, expiry, and exact audience from the configured set;
2. resolve active tenant and current membership by verified issuer/subject;
3. require audience-to-product match and current product entitlement;
4. require one of the resolver's roles and reject mutations for `VIEWER` and
   `CUSTOMER_VIEWER` regardless of frontend behavior;
5. load the target's canonical tenant and hierarchy in the tenant transaction;
6. allow `TENANT_ADMIN` for tenant-scoped Admin operations, otherwise resolve an
   explicit matching ancestor grant;
7. return generic `NOT_FOUND` for cross-tenant/out-of-scope target reads and
   `FORBIDDEN` for known product/role denial without resource disclosure.

Authorization lookup targets p95 below 50 ms at the inherited tenant scale. It
uses tenant-prefixed indexes on asset business unit, project asset/business unit,
inspection project/asset/business unit, scopes, entitlements, and membership.

### Report Publication and Notification State

```text
FINAL SNAPSHOT
   | policy=AUTOMATIC (same transaction)
   | policy=MANUAL -> publishReport
   v
PUBLISHED ──newer published snapshot──> SUPERSEDED
   |
   └──invalidateReportPublication──────> INVALIDATED
```

There is no transition back to `PUBLISHED`. A correction produces a new report
snapshot and publication. The outbox event ID is the deduplication key for each
recipient/channel. Automatic mode applies to every final report classification.
Enabling automatic mode does not publish existing final reports retroactively.

Dashboard polls `myNotifications` every 30 seconds only while visible, refreshes
on window focus, and uses the returned cursor and server unread count. Mark-read
is optimistic in the UI but reconciles against the mutation result. External
channels receive safe templates with no sensitive findings or media URLs.

### Capture Migration

Move `app/(capture)`, `src/pwa`, capture auth, capture GraphQL documents,
manifest, service worker, and capture tests from `apps/web` to `apps/capture`.
The service worker is scoped only to the Capture origin. IndexedDB names gain a
Capture-specific version prefix so future schema migrations are explicit.
Draft keys remain responsibility-scoped; server bootstrap always wins for final,
revoked, or expired state. No old URL redirect or data migration is implemented.

## Integration Points

### Keycloak

- Two public PKCE clients use exact Admin and Dashboard redirects/origins.
- A confidential service account limited to user lookup/create and execute-
  actions-email supports invitation provisioning.
- Provisioning calls use invitation ID as idempotency key; 409 lookup races are
  resolved by verified normalized email, while 429/5xx retry with bounded
  exponential backoff. Permanent 4xx marks the invite `FAILED` with a safe Admin
  message and auditable provider code.
- Activation URLs return only to the Dashboard origin for customer invites.

### Private npm Registry

- CI publishes immutable `@inspection/design-system` semantic versions.
- Apps authenticate to the registry only during dependency installation; no
  registry credential enters browser bundles or runtime images.
- Each app pins an exact version and its lockfile integrity hash.

### MinIO and Media Delivery

- Capture retains direct multipart upload with server-issued short-lived URLs.
- Customer media uses separate read-only presigned URLs minted after current
  authorization and sensitivity checks; object keys are never returned.
- Expired links are refreshed by re-querying, not by lengthening TTL.

### RabbitMQ, SMTP, and Twilio-Compatible Delivery

- Existing transactional outbox and at-least-once consumer semantics remain.
- New report/progress events fan out to in-app records and configured verified
  external channels.
- Per-recipient unique keys suppress duplicates; provider failures retry per
  channel without rolling back publication.

## Impact Analysis

| Component | Impact Type | Description and Risk | Required Action |
|---|---|---|---|
| `apps/web` | Deprecated | Combined trust zones and single release; high migration surface | Split by ownership, prove parity, then remove |
| `apps/admin` | New | Administrative product; medium UI breadth | Create standalone Next.js project and move Admin capabilities |
| `apps/dashboard` | New | Internal/customer adaptive product; high authorization sensitivity | Create standalone app with separate internal and customer feature compositions |
| `apps/capture` | New | Public PWA; high mobile/offline sensitivity | Move Capture modules and preserve invitation/session/upload contracts |
| Design system | New | Shared visual dependency; medium drift risk | Publish private exact versions and add compatibility checks |
| GraphQL schema/generated files | Modified | New types and authorization metadata; high contract impact | Evolve canonical schema and regenerate Go/three TypeScript clients |
| OIDC/CORS config | Modified | Multiple exact audiences/origins; high security impact | Parse sets, fail closed, configure two clients, expand smoke tests |
| Access models/slices | Modified/new | Entitlements, customer role, invitations, project scope, hierarchy | Add migrations and vertical slices; update every protected resolver |
| Report models/slices | Modified/new | Publication policy and lifecycle; high visibility impact | Add ledger/policy migrations and transactional publication logic |
| Dashboard projections | Modified/new | Customer portfolio/timeline safe reads | Add scoped projection/query slices and rebuild support |
| Notification slices | Modified/new | Per-recipient in-app state | Add projection, read mutations, deduplication, and polling API |
| Keycloak adapter | New | External account provisioning; medium availability risk | Add least-privilege adapter and retrying outbox consumer |
| Compose/local scripts | Modified | Three ports/services and identity clients | Add admin 3000, dashboard 3002, capture 3003; retain Gotenberg 3001 |
| CI/verification | Modified | Three lockfiles and build/test suites | Run codegen/lint/test/build per app plus cross-product E2E |

## Testing Approach

Concrete cases are canonical in `_tests.md`.

- Go unit tests remain table-driven inside each vertical slice. Fakes replace only
  Keycloak, clock, ID generation, outbox, and media URL I/O boundaries.
- Go integration tests run migrated PostgreSQL with RLS and real slice wiring;
  RabbitMQ/MinIO/Keycloak-compatible test services cover boundary contracts where
  behavior depends on them.
- Each frontend uses Vitest and jsdom for auth state, route capability maps,
  response mappers, forms, polling, and Capture IndexedDB/upload reconciliation.
- GraphQL contract tests regenerate all clients from one canonical schema and
  assert that customer operations cannot select internal fields.
- Playwright runs product-specific configurations plus one root cross-product
  suite. Desktop projects cover Chrome/Edge-compatible Chromium, Firefox, and
  WebKit; Capture adds Pixel/Chrome and iPhone/WebKit projects with camera,
  offline, service-worker, and interrupted-upload fixtures.
- E2E fixtures create isolated tenants, identities, grants, reports, media flags,
  invitations, and outbox events. No suite shares a mutable Mailpit queue or
  tenant across workers.
- Accessibility checks use axe plus keyboard-only journeys. Visual snapshots are
  limited to stable shared primitives and key responsive shells.
- Capacity tests retain the existing 500 global/100 per-tenant Capture-session,
  100,000 asset, and 1,000,000 historical inspection envelope.

## Development Sequencing

### Build Order

1. Add database migrations for entitlements, invitation state, project scopes,
   publication policy/ledger, notification recipients, constraints, RLS, and indexes.
2. Extend request metadata, multi-audience OIDC, multi-origin CORS, product
   authorization, and runtime hierarchy resolution.
3. Add invitation/access vertical slices and the Keycloak adapter/consumer.
4. Add publication policy, publish/invalidate slices, finalization integration,
   outbox contracts, and recipient notification projection.
5. Add customer-safe portfolio, timeline, report, evidence, and media URL slices.
6. Evolve the canonical GraphQL schema, resolvers, contract tests, and generated Go code.
7. Build and publish the first private design-system package version.
8. Create Admin and migrate administrative capabilities from `apps/web`.
9. Create Dashboard, migrate internal operations, and add customer compositions.
10. Create Capture by moving PWA/session/upload behavior and changing the invitation base URL.
11. Update Keycloak realm config, Compose, local scripts, smoke/security scripts,
    CI, and generate all three TypeScript clients.
12. Run the full contract, browser, accessibility, security, offline, and capacity
    suites; remove `apps/web` only after all parity checks pass.

### Technical Dependencies

- A private npm registry and CI publish credential for `@inspection/design-system`.
- Keycloak realm configuration with two public clients and one least-privilege
  provisioning service account.
- DNS/TLS and reverse-proxy routes for the three production origins.
- Exact environment values for all origins, callback URLs, audiences, API URL,
  and Capture invitation base URL.
- Database migration compatibility window must include the last pre-split schema
  until API, worker, and scheduler deployments converge.

## Monitoring and Observability

Structured logs include `correlation_id`, `tenant_id`, `membership_id`, verified
`audience`, requested `product`, role, target kind/id, direct grant kind/id,
authorization outcome, publication/policy IDs, notification event ID, and
invitation ID. Logs never contain bearer tokens, OTPs, link tokens, email bodies,
presigned URLs, object keys, or report/evidence content.

| Signal | Target or alert |
|---|---|
| Simple GraphQL query latency | p95 <= 300 ms excluding storage transfer |
| GraphQL mutation latency | p95 <= 500 ms excluding asynchronous provider work |
| Hierarchy authorization lookup | p95 <= 50 ms; alert after 10 minutes above target |
| Admin/Dashboard initial useful view | p95 <= 2 s at confirmed tenant scale |
| Capture initial useful view | p95 <= 2 s on reference mobile network, excluding user OTP delay |
| Cross-product authorization denial | Count by audience/product; alert on any successful mismatch in security probes |
| Invitation provisioning | Queue age, retry count, permanent failure rate; alert if oldest pending > 5 minutes |
| Automatic publication | Final-to-published lag; alert if transaction/event reconciliation finds a missing publication |
| Recipient notifications | Projection lag < 60 s, duplicate suppression count, unread query latency |
| Sensitive media denial | Count by reason; alert on any presigned URL emitted for blocked media |
| Capture queue | Draft age, multipart retry/failure, orphan uploads, finalization conflicts |
| Frontend health | Separate availability/error-rate and release version per product |

Audit events are mandatory for invitation/grant changes, entitlement changes,
policy updates, report publish/invalidate/supersede, sensitive access denials, and
notification configuration. A reconciliation job reports final snapshots missing
their policy decision, active duplicate publications, and outbox/projection gaps.

## Technical Considerations

### Key Decisions

- **Standalone apps, same repository**: satisfies release/session isolation while
  preserving atomic schema coordination; gives up one-lockfile simplicity.
- **Versioned private design system**: preserves product-family consistency
  without source coupling; accepts deliberate upgrade overhead.
- **Separate OIDC clients, one API**: isolates audiences and redirects while
  retaining one domain backend; requires product-aware authorization everywhere.
- **Role plus entitlement plus runtime scope hierarchy**: avoids role explosion
  and stale materialized grants; adds indexed authorization work per request.
- **Publication ledger beside immutable snapshots**: supports correction and
  audit without mutating report content; adds a visibility lifecycle.
- **Dedicated customer GraphQL projections**: enforces data minimization at the
  server boundary; duplicates a small amount of response mapping.
- **Polling for in-app notifications**: fits current HTTP infrastructure; accepts
  up to 30 seconds of foreground notification latency.
- **No legacy URL compatibility**: removes migration code because no production
  users or links exist; old local links intentionally stop working.

### Known Risks

- **Authorization retrofit (high)**: every existing resolver must declare product,
  role, mutation intent, and target context. Mitigate with a resolver inventory
  contract test that fails when an operation lacks policy metadata.
- **Customer data leakage (high)**: internal report structures are broad. Mitigate
  with separate schema types, allowlist mappers, negative GraphQL tests, and
  presigned-URL denial tests.
- **Three-app drift (medium)**: dependencies and UX can diverge. Mitigate through
  exact design-system versions, shared browser contracts, and CI drift reporting.
- **Keycloak partial failure (medium)**: provider success can occur before local
  acknowledgement. Mitigate through deterministic invitation IDs, subject lookup,
  outbox retries, and idempotent state transitions.
- **Hierarchy performance (medium)**: broad grants may touch large portfolios.
  Mitigate with tenant-prefixed indexes, bounded cursor queries, query plans in
  integration tests, and the 100x scale benchmark.
- **Capture regression (high)**: moving origins changes service-worker, IndexedDB,
  camera, and CORS behavior. Mitigate with mobile/offline parity tests before
  deleting `apps/web`.

## Architecture Decision Records

- [ADR-001: Split the Existing Web Experience into Three Independent Frontends](adrs/adr-001.md) — Establishes the three-product business boundary.
- [ADR-002: Permissioned and Role-Adaptive Dashboard](adrs/adr-002.md) — Defines internal and customer Dashboard behavior.
- [ADR-003: Tenant-Controlled Report Publication](adrs/adr-003.md) — Makes publication manual by default and tenant configurable.
- [ADR-004: Immutable Capture Is Separate from Result Review](adrs/adr-004.md) — Keeps Capture invitation-scoped and immutable.
- [ADR-005: Use Three Standalone Frontend Projects in One Repository](adrs/adr-005.md) — Defines project, URL, release, and design-system boundaries.
- [ADR-006: Use Separate OIDC Clients with Shared SSO and One GraphQL API](adrs/adr-006.md) — Defines identity continuity and audience isolation.
- [ADR-007: Separate Product Entitlements from Roles and Resolve Scope Hierarchies at Read Time](adrs/adr-007.md) — Defines the access model and immediate revocation behavior.
- [ADR-008: Publish Reports Through an Audited Visibility Ledger and Customer-Safe Projections](adrs/adr-008.md) — Defines report visibility, safe evidence, and recipient notifications.
