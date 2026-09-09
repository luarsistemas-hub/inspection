# ADR-009: Domain-Specific Observable Operation State

## Status

Accepted

## Date

2026-09-08

## Context

Bulk work, exports, delivery, downloads and purge need distinct product states and authorization.

## Decision

Persist operation state in focused domain records and dispatch work through the existing outbox and worker. GraphQL returns queued, running, retryable, terminal and per-item or destination state where applicable.

## Alternatives Considered

### Alternative 1: Generic jobs API

- **Description**: One table and API for all background work.
- **Pros**: Smaller common implementation.
- **Cons**: Hides domain semantics.
- **Why rejected**: Product states are domain-specific.

### Alternative 2: Audit-only state

- **Description**: Record events without current status.
- **Pros**: Minimal schema change.
- **Cons**: Cannot show recovery or progress.
- **Why rejected**: Journeys require observable current state.

## Consequences

### Positive

- Each journey has meaningful recovery and history.

### Negative

- Several focused models are needed.

### Risks

- State inconsistency; require shared vocabulary and contract tests.

## Implementation Notes

- Preserve 5-second, 30-second and 5-minute retry scheduling before DLQ.

## References

- services/inspection/internal/platform/messaging/retry.go
