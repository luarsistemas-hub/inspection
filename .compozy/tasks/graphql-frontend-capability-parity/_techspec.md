# Technical Specification: GraphQL and Frontend Capability Parity

## Executive Summary

Complete the canonical GraphQL contract before completing Admin, Dashboard and
Capture. Backend additions remain Vertical Slice Architecture siblings with
their domain, repository, setup, tests, mediator registration and thin resolver
wiring. PostgreSQL migrations and existing RLS remain authoritative.

Admin and Dashboard select a membership, then send X-Inspection-Membership-ID
on protected requests. The API validates it against the OIDC subject every time
and derives tenant context from that membership. Capture retains its link/OTP
cookie-and-CSRF boundary. Every product generates typed operations from
product-owned GraphQL documents while keeping its existing fetch transport.

## System Architecture

### Component Overview

| Component | Responsibility |
|---|---|
| schema and gqlgen | Canonical surface, generated server contracts and thin mediator resolvers |
| Membership context | Identity-scoped selector, subject/membership validation and RLS derivation |
| Administrative slices | Tenancy, access, participants, catalogs/profiles, assets/origins, governance, audit, usage and exports |
| Operational slices | Schedules, inspections, projects, triage, reports, publication/download and recapture |
| Customer projections | Explicit allowlisted portfolio, timeline, report and evidence view models |
| Capture slices | OTP, consent, media, metadata, impossibility, false-positive, submission and recovery |
| Domain operations | Import/export, delivery, download and purge rows through existing outbox/worker |
| Product applications | Separate routes and UX; shared design-system primitives only |
| Parity gate | Contract, RLS, authenticated browser evidence and legacy inventory |

### Story Coverage Mapping

| Stories | Components |
|---|---|
| US-001, US-033 | Capability matrix, schema/client generation, parity CI and legacy removal |
| US-002 to US-008 | Membership, tenancy, roles/scopes, access explanation and Admin |
| US-009 to US-017 | Participant, catalog/profile, asset/origin, bulk/export and Admin |
| US-018 to US-021 | Policy, retention/purge, delivery, audit/usage and governance |
| US-022 to US-027 | Schedule, inspection, project, triage, report and recapture Dashboard |
| US-028 to US-030 | Customer projections and notification Dashboard |
| US-031 to US-032 | Capture baseline plus decline and false-positive |

## Implementation Design

### Core Interfaces

Membership middleware is the only source of internal tenant context.

    type MembershipContextResolver interface {
        Resolve(context.Context, string, string) (auth.Metadata, error)
    }
    func RequireMembershipContext(next http.Handler, r MembershipContextResolver) http.Handler

Every new slice receives tenant ID only from validated context. Commands accept
client mutation identity and expected version when mutating versioned resources.
Async work is a focused domain record, never a generic job.

    type OperationView struct {
        ID identity.ID
        Status OperationStatus
        Retryable bool
        CorrelationID string
    }
    type OperationReader interface {
        Get(context.Context, identity.ID, identity.ID) (OperationView, error)
    }

### Data Models

| Family | Additions |
|---|---|
| Access | Membership selector, delegated roles/scopes, effective-access explanation/comparison, administrative invitation lifecycle |
| Configuration | Analysis profile collection/detail/version/activation/retirement/usage and lifecycle/history projections |
| Origin/media | Administrative upload intent, immutable ADMIN_UPLOAD provenance, lineage, verification and active/invalid state |
| Governance/operations | Publication/history, delivery attempts, deletion/hold/purge/tombstone, import rows, exports and download preparation |
| Customer | Projection DTOs only; never internal entities filtered in TypeScript |

All connections use stable cursors, default 25 and maximum 100. Enforce 20 MiB
JPEG/PNG/WebP/HEIC media, 5 MiB parts, 24-hour upload/export expiry, UTF-8 CSV
imports up to 10,000 rows or 25 MiB, PDF-only reports, and retries at 5
seconds, 30 seconds and 5 minutes before DLQ.

### API Endpoints

The surface remains POST /graphql.

| Group | Contract completion |
|---|---|
| Shared access | Paginated identity-owned membership selector and active membership summary; tenant ID never authorizes |
| Admin | Collections/details/history for invitations, profiles, lifecycle, origin upload, delivery, retention/deletion/hold/purge, audit/usage/export |
| Access | Allow/deny explanation and comparison with grant, role, scope, inheritance, validity, redundancy and conflict |
| Lifecycle | Missing archive/deactivate/retire, administrative resend/revoke/upload, bulk preview/apply and export state |
| Dashboard | Publication current/history, observable download and complete operational details |
| Customer | Real portfolio, timeline, report and evidence allowlist projections with safe unavailable states |
| Capture | Public decline/revoke and false-positive through existing scoped session |

Retain existing operation names when sufficient. Make incompatible coordinated
schema changes before frontend work, regenerate gqlgen and all client artifacts,
then add GraphQL contract tests.

### Frontend Design

Admin implements grouped navigation, persistent tenant/scope, query-parameter
collection context, semantic dense tables, filters, pagination, detail/history
routes and full-page high-impact forms. Dashboard keeps distinct operational and
customer architectures. Capture retains mobile-first state, IndexedDB drafts,
multipart resume, HttpOnly session and memory-only CSRF token.

The design system gains neutral accessible breadcrumbs, filter bars, tables,
pagination, recovery banners, version-conflict and confirmation primitives; it
does not become a shared shell. User text is pt-BR and externalized.

## Integration Points

| Service | Design |
|---|---|
| Keycloak/OIDC | Validate bearer identity, resolve membership server-side, bootstrap Admin only from injected secrets |
| PostgreSQL/GORM/RLS | Context-bound tenant transactions; migrator alone changes schema/policy |
| RabbitMQ/outbox | Atomically persist domain operation/intent; worker updates observable state |
| MinIO | Opaque tenant keys, digest verification, 24-hour expiry and reauthorization |
| Delivery providers | Per-channel/destination outcome; retry excludes successful targets |
| LiteLLM/Gotenberg | Existing services expose ready/failed state and authorized PDF download |

## Impact Analysis

| Component | Impact Type | Description and Risk | Required Action |
|---|---|---|---|
| schema and gqlgen | modified | High cross-consumer contract risk | Complete once, regenerate and contract-test |
| auth/tenant middleware | modified | Affects protected internal requests | Validate membership header and CORS |
| slices/migrations | new/modified | Missing read/lifecycle/operation state | Add focused VSA siblings |
| Admin | modified | Replace JSON query shell | Scoped collection/detail/form journeys |
| Dashboard | modified | Complete operational/customer/report journeys | Typed documents and explanatory gates |
| Capture | modified | Missing decline/false-positive | Preserve session/local recovery |
| design system | modified | Data/recovery primitives | Accessible neutral components |
| apps/web | deprecated | High release risk | Remove only after gate |
| CI/deploy/docs | modified | No seeded parity evidence | Add stack, fixtures and audit |

## Testing Approach

Use table-driven Go unit tests with fakes only at I/O boundaries, setup tests for
valid/invalid/dependency/setup behavior, and GraphQL contract tests for schema,
errors, idempotency, membership context and allowlists. Integration tests use
real PostgreSQL/RLS, migrations and outbox. Seeded authenticated Playwright
tests cover all personas, customer withdrawal, Capture recovery and WCAG 2.2
AA. Concrete cases are in _tests.md.

## Development Sequencing

### Build Order

1. Membership middleware, selector, roles and secret-backed Admin bootstrap.
2. Migrations and missing administrative/access/governance slices.
3. Customer projections, report/download history and delivery/purge models.
4. Schema/resolver completion, regeneration and contract tests.
5. Typed documents, design primitives and Admin redesign.
6. Dashboard and Capture completion.
7. Bulk/export UX, observability and audit links.
8. Seeded parity gate and evidence.
9. Legacy inventory, URL handling and apps/web removal after green gate.

### Technical Dependencies

- Keycloak secret injection and bootstrap contract.
- Migrator deployment before runtime schema use.
- Seeded Keycloak/PostgreSQL/RabbitMQ/MinIO/provider-double test stack.
- typescript-operations support in every standalone application.

## Monitoring and Observability

Log tenant, membership, actor, product, operation/resource, state transition,
idempotency, correlation and causation IDs while redacting credentials, tokens,
contacts and media. Measure denials, conflicts, generation drift, queue age,
retry/DLQ, bulk outcomes, expiry, provider outcomes, purge backlog and hold
blocks. Alert on secret bootstrap failure, customer authorization regressions,
DLQ growth, purge failure and unmapped capability rows.

## Technical Considerations

### Key Decisions

- Server-resolved membership context.
- Generated typed operations with separate product transports.
- Domain-specific observable async state through outbox/worker.
- Contract, RLS and authenticated browser evidence before legacy removal.
- Fixed, testable operational limits.
- Secret-provisioned fixed Admin identity.

### Known Risks

- Contract drift: backend-first ownership and generation checks.
- Role regression: role/scope and RLS tests.
- Customer leakage: projection DTO allowlists.
- Purge/hold race: final transaction recheck.
- E2E flakiness: deterministic fixtures and artifacts.

## Architecture Decision Records

- ADR-001 through ADR-006 — Accepted product-direction ADRs in this directory.
- [ADR-007: Server-Resolved Membership Context](adrs/adr-007-server-resolved-membership-context.md) — Per-request membership validation.
- [ADR-008: Generated Typed GraphQL Operations](adrs/adr-008-generated-graphql-operations.md) — Typed operation generation.
- [ADR-009: Domain-Specific Observable Operation State](adrs/adr-009-domain-operation-state.md) — Focused async state.
- [ADR-010: Executable Parity Release Gate](adrs/adr-010-parity-release-gate.md) — Required CI evidence.
- [ADR-011: Fixed Operational Boundaries](adrs/adr-011-operational-boundaries.md) — Exact limits.
- [ADR-012: Secret-Provisioned Fixed Super Administrator](adrs/adr-012-secret-provisioned-super-admin.md) — Secret-only bootstrap.
