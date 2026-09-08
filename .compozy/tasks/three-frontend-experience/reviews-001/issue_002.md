---
round: 1
round_created_at: 2026-09-08T03:15:57.767566Z
status: resolved
file: packages/inspection-design-system/tests/design-system.test.tsx
line: 36
severity: medium
author: reviewer
---

# Issue 002: UT-068 does not test viewport overflow

## Review Comment

The test loops over `[320,360,768,1440]` but never applies those viewport widths or renders any layout. It only repeats identical CSS string assertions, so horizontal overflow at every required viewport can regress while the test still passes. The implementation must use actual viewport/layout measurements or an equivalent behavioral assertion.

## Triage

- Decision: `VALID`
- Notes: The original test only repeated static CSS assertions and did not apply any viewport width or render a layout probe, so it could not detect a regression in the viewport contract. The root cause is inadequate test behavior, not a disproven review expectation. The test now creates a DOM layout probe for each required viewport, derives the expected fluid container size from the production CSS contract, and asserts wrapping and zero minimum inline size on the rendered inline layout. The package uses jsdom, which does not implement a layout engine; the test therefore verifies the observable CSS behavior and width contract rather than relying on jsdom's always-zero geometry.
