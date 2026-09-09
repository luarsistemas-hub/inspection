# Workflow Memory

## Durable Context

- Task 1 establishes server-resolved membership context as the prerequisite for protected Admin and Dashboard requests.
- Internal products send `X-Inspection-Membership-ID`; the API reloads ownership and active state for each request and derives the tenant only from that membership. Capture remains outside this boundary.
- The fixed Admin identity is provisioned by the Keycloak bootstrap container from environment-only values; no credential values belong in the realm export or tracked environment template.
- Delegated administrative memberships are entitled to the Admin product; only `TENANT_ADMIN` receives both Admin and Dashboard. `ACCESS_ADMIN` may grant delegated or operational roles but cannot grant `TENANT_ADMIN`.
- Capture multipart recovery is available through `media/reconcile_upload`: it reconciles the durable multipart row with MinIO, persists provider-confirmed parts, and returns only valid missing part numbers. The Task 4 GraphQL boundary and Task 7 Capture client should consume this mediator query instead of inferring completion from local draft state.
- Product-owned GraphQL documents are now validated against the canonical schema while the operation manifest is built. This catches invalid selections and unused variables before a manifest can be accepted.
- MinIO's `ListMultipartParts` is the recovery source of truth: reconciliation replaces stale local part rows, and presigning rejects provider-confirmed parts so only missing work can resume.

## Open Risks

- None recorded yet.
