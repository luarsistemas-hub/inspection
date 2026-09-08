---
round: 1
round_created_at: 2026-09-08T03:15:57.767566Z
status: resolved
file: packages/inspection-design-system/src/primitives/form.tsx
line: 12
severity: medium
author: reviewer
---

# Issue 003: Field does not associate hints and errors with controls

## Review Comment

`Field` puts `aria-describedby` on a wrapper `<span>` rather than on the contained input/select/textarea. Consumers such as `<Field error="..."><Input /> </Field>` therefore leave the actual control unassociated with the hint/error text, so assistive technology may not announce it. The `required` prop also renders only a visual asterisk and does not provide `aria-required` or `required` semantics to the control.

## Triage

- Decision: `VALID`
- Root cause: `Field` assigned `aria-describedby` to a wrapper `<span>`, so the actual form control never received the hint/error relationship. Its `required` prop only rendered visible text and did not propagate native or ARIA required semantics.
- Fix: clone valid child controls while preserving any existing `aria-describedby`, append the generated description ID, and propagate `aria-invalid`, `aria-required`, and native `required` when applicable. Added a regression test covering hint/error association, preservation of an existing description, invalid state, and required semantics.
- Verification: `npm test`, `npm run lint`, and `npm run build` in `packages/inspection-design-system` all passed; the package test suite reports 5 passing tests.
