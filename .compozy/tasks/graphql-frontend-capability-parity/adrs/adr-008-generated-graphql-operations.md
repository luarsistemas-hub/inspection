# ADR-008: Generated Typed GraphQL Operations

## Status

Accepted

## Date

2026-09-08

## Context

Frontend generation currently emits schema types only while runtime operations are hand-written strings.

## Decision

Keep product-specific fetch transports and generate operation result and variable types with typescript-operations. Each product owns .graphql documents beside its features.

## Alternatives Considered

### Alternative 1: Typed manual wrappers

- **Description**: Keep strings and declare result types manually.
- **Pros**: No codegen change.
- **Cons**: Contract copies drift.
- **Why rejected**: Operations would not be schema-checked.

### Alternative 2: Shared GraphQL client

- **Description**: Build a common transport package.
- **Pros**: Centralizes code.
- **Cons**: Blurs bearer and Capture cookie/CSRF boundaries.
- **Why rejected**: Only code generation should be common.

## Consequences

### Positive

- CI detects selection and variable drift.

### Negative

- Schema changes regenerate all three products.

### Risks

- Stale output; generation check fails CI.

## Implementation Notes

- Do not add a GraphQL runtime client solely for this initiative.

## References

- apps/admin/codegen.ts
