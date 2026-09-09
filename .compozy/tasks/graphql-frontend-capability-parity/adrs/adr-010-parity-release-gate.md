# ADR-010: Executable Parity Release Gate

## Status

Accepted

## Date

2026-09-08

## Context

Build-only checks cannot prove tenant isolation, protected journeys, or customer withdrawal before legacy removal.

## Decision

Removal of apps/web requires CI GraphQL contract tests, PostgreSQL/RLS integration tests, seeded authenticated Playwright journeys for all three products, generated-artifact checks, and a fully evidenced capability matrix.

## Alternatives Considered

### Alternative 1: Manual authenticated evidence

- **Description**: Maintain manual test records.
- **Pros**: Lower CI setup cost.
- **Cons**: Drift escapes repeatable verification.
- **Why rejected**: Parity is release-critical.

### Alternative 2: Build-only checks

- **Description**: Verify compilation and unauthenticated UI only.
- **Pros**: Fast.
- **Cons**: Does not verify protected behavior.
- **Why rejected**: Insufficient deletion evidence.

## Consequences

### Positive

- Legacy removal is repeatable and auditable.

### Negative

- CI needs a seeded service stack.

### Risks

- Browser flakiness; use deterministic fixtures and failure artifacts.

## Implementation Notes

- Any failed or unmapped matrix row blocks removal.

## References

- .github/workflows/ci.yml
