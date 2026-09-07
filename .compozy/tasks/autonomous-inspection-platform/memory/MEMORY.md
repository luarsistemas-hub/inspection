# Workflow Memory

## Current state

- `task_01.md` foundation is implemented under `services/inspection` with four composition roots, gqlgen schema generation, a dedicated migrator, forced RLS, tenant transactions, authorization, audit, operational routes, and CI gates.
- The workflow has `_prd.md`, `_techspec.md`, `_user_stories.md`, `_tests.md`, and `_tasks.md`.
- `_spec.md` is absent; the task prompt treats it as optional when unavailable.

## Durable decisions

- Keep the Contract Service POC untouched until Task 7.
- Implement the inspection service as operation-level vertical slices with one public `Setup` per operation.
- Tenant-owned runtime I/O must enter `tenanttx.Runner`; the bootstrap slice is the deliberately narrow exception and uses one owner transaction because no tenant context exists before creation.
- Runtime schema compatibility is version 3. Tasks 2 and 3 add tenant-owned messaging, invitation/session, media, notification, participant, segment, template/profile, and asset catalogs through the foundation migrator.
- Published segment/template/profile documents store canonical JSON and digests; database triggers keep their content immutable while allowing explicit lifecycle status transitions.
- Cross-capability prerequisite reads use mediator query contracts. In particular, asset operations resolve participants, segment schemas, and active templates without reaching into another capability's repository.
- External invitation and session tokens use `<tenant UUID>.<256-bit opaque secret>` so the runtime can establish forced-RLS tenant context before lookup; PostgreSQL stores only the digest of the complete token.
- Async delivery uses versioned safe envelopes, PostgreSQL outbox/inbox, publisher confirms plus mandatory routing, and durable RabbitMQ queues with named retry/DLQ topology.
- Original media stays in a private tenant-partitioned MinIO bucket; multipart completion verifies ordered parts, HEAD metadata, decoded media signature, size, and SHA-256 before downstream use.

## Open risks

- Later slices must register new tenant-owned models in `database.Models` and extend the explicit RLS migration table list in the same migration versioning discipline.
- OIDC cryptographic verification remains adapter-owned behind `auth.TokenVerifier`; callers must never construct unverified `OIDCClaims` at the HTTP boundary.
- Later occurrence/capture slices must snapshot the resolved template policy and referenced version IDs; they must not reinterpret mutable asset selections for existing work.
- Lifecycle creation publishes deterministic `inspection.created.v1`; its consumer emits one `origin.invitation_requested.v1`. Terminal inspection events cancel planned reminders and revoke matching external sessions.
- Every grouped `identity.ID` field in a GORM model needs its own explicit `type:uuid` tag; grouped declarations do not safely preserve PostgreSQL UUID/RLS compatibility.
- The migrator must create every capability schema represented by registered GORM models before `AutoMigrate`; this includes `analysis`, `reports`, and `retention`, whose RLS migration is applied afterward.
