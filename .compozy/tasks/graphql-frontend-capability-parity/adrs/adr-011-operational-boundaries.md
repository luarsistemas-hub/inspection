# ADR-011: Fixed Operational Boundaries for Parity

## Status

Accepted

## Date

2026-09-08

## Context

The PRD leaves operational limits to the TechSpec.

## Decision

Preserve tested 20 MiB JPEG/PNG/WebP/HEIC media, 5 MiB parts, 24-hour upload expiry, page default 25/max 100, and retries at 5 seconds, 30 seconds and 5 minutes. Accept UTF-8 CSV imports up to 10,000 rows or 25 MiB, retain exports for 24 hours, and support PDF-only report downloads.

## Alternatives Considered

### Alternative 1: Unspecified environment limits

- **Description**: Leave every maximum configurable.
- **Pros**: Flexibility.
- **Cons**: Ambiguous behavior and tests.
- **Why rejected**: Parity needs visible, stable boundaries.

### Alternative 2: Large bulk profile

- **Description**: Support 100,000 rows/100 MiB and seven-day exports.
- **Pros**: Bigger migrations.
- **Cons**: Higher worker, storage and exposure risk.
- **Why rejected**: Exceeds current scope.

## Consequences

### Positive

- Exact limits are testable and user-visible.

### Negative

- Larger imports require a future decision.

### Risks

- Adversarial CSV; stream and limit before persistence.

## Implementation Notes

- Signed files expire and are reauthorized for every issue.

## References

- services/inspection/internal/platform/objectstore/objectstore.go
