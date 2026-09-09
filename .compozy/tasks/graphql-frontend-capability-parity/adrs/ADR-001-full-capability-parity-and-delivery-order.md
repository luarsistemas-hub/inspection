# ADR-001: Full Capability Parity and Backend-First Delivery

## Status

Accepted

## Date

2026-09-08

## Context

The canonical GraphQL schema exposes substantially more capability than the standalone Admin and Dashboard applications currently surface. Several customer-facing resolvers are placeholders, important administrative read models do not exist, and the legacy `apps/web` application still contains useful but stranded behavior. The product is not in production, so a staged compatibility rollout is not required.

## Decision

Deliver complete end-to-end capability parity as one program. Complete the backend and canonical GraphQL contract first, including missing read models and placeholder projections, and then implement Admin, Dashboard, and Capture journeys against the completed contract.

Every GraphQL operation must be owned by a business journey or an internal workflow step. Technical operations such as multipart upload steps must not become standalone user-facing screens.

Use `apps/web` only as a behavioral reference. Migrate valid behavior into the three owned frontends, verify parity, and remove the legacy application.

## Alternatives Considered

### Alternative 1: Frontend-first delivery

- **Description**: Build screens against provisional contracts and complete the backend afterward.
- **Pros**: Earlier visual progress.
- **Cons**: Duplicated mocks, unstable contracts, and rework around missing read models.
- **Why rejected**: The user explicitly selected backend-first completion.

### Alternative 2: Limit scope to existing GraphQL fields

- **Description**: Surface only operations that already exist without adding missing backend capabilities.
- **Pros**: Smaller scope.
- **Cons**: Leaves effective access, publication history, notification preferences, retention status, analysis profile discovery, and customer projections incomplete.
- **Why rejected**: It would not produce usable parity.

### Alternative 3: Keep `apps/web` indefinitely

- **Description**: Run the legacy and standalone frontends in parallel.
- **Pros**: Avoids migration work.
- **Cons**: Duplicate ownership, divergent behavior, and long-term maintenance overhead.
- **Why rejected**: The accepted direction is to migrate and remove it.

## Consequences

### Positive

- Frontends consume a stable and complete product contract.
- Every operation has explicit ownership and a testable consumer.
- Legacy behavior is preserved without retaining a fourth product surface.
- Generated clients can be refreshed once the backend contract is coherent.

### Negative

- Users will not receive partial frontend capability while the backend contract is being completed.
- The initial backend scope is larger than simply exposing existing operations.
- Removing `apps/web` requires a formal parity checkpoint.

### Risks

- Backend completion may drift into implementation detail without preserving user journeys; mitigate with an operation-to-journey capability matrix.
- Legacy behavior may be missed; mitigate by mapping every legacy operation and acceptance journey before removal.

## Implementation Notes

- Treat `services/inspection/schema.graphqls` as the canonical contract.
- Preserve Vertical Slice Architecture for backend work.
- Regenerate backend and frontend GraphQL artifacts after contract changes.
- Produce a parity matrix covering queries, mutations, owning frontend, user role, journey, and verification evidence.

## References

- `services/inspection/schema.graphqls`
- `apps/web/src/graphql/documents/dashboard.graphql`
- `.compozy/tasks/three-frontend-experience/_techspec.md`
- `../_capability_matrix.md`
