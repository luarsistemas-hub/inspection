# ADR-007: Server-Resolved Membership Context

## Status

Accepted

## Date

2026-09-08

## Context

OIDC identities can have multiple tenant memberships; client-supplied tenant IDs cannot authorize access.

## Decision

Admin and Dashboard send X-Inspection-Membership-ID. The server verifies the OIDC subject, reloads the membership on every request, rejects inactive membership or tenant, and derives the RLS tenant context. Capture retains its scoped cookie session.

## Alternatives Considered

### Alternative 1: Backend tenant session

- **Description**: Persist selected membership in a server session.
- **Pros**: No context header.
- **Cons**: New session lifecycle and multi-tab ambiguity.
- **Why rejected**: Per-request server validation preserves the stateless bearer boundary.

### Alternative 2: Tenant token exchange

- **Description**: Mint an internal tenant token after selection.
- **Pros**: Token-level scoping.
- **Cons**: Token lifecycle and Keycloak integration work.
- **Why rejected**: Reloaded membership validation is sufficient.

## Consequences

### Positive

- Selection is explicit, auditable, and RLS-safe.

### Negative

- Internal clients must attach membership context consistently.

### Risks

- Stale context causes denial; clients clear protected data and return to selection.

## Implementation Notes

- Add the header to CORS allow headers.

## References

- services/inspection/internal/platform/auth/authorize.go
