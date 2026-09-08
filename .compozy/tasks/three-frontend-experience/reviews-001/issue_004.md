---
round: 1
round_created_at: 2026-09-08T03:15:57.767566Z
status: resolved
file: packages/inspection-design-system/src/primitives/dialog.tsx
line: 17
severity: medium
author: reviewer
---

# Issue 004: Dialog focus trap allows focus to escape

## Review Comment

The Tab handler only wraps focus when `document.activeElement` is the first or last focusable element. When the dialog itself initially owns focus, pressing Tab or Shift+Tab does not prevent the event, allowing focus to move outside the modal. This violates the documented keyboard behavior and WCAG modal interaction expectations; the dialog must keep focus inside and restore focus to the opener on close.

## Triage

- Decision: `VALID`
- Root cause: the keydown handler only wrapped focus when the active element was already the first or last focusable descendant. When the dialog container held initial focus, Tab was allowed to escape, and the component did not retain the opener needed for focus restoration.
- Fix: the dialog now treats the dialog container and any focus outside the dialog as boundary states, cycles forward/backward into the first/last focusable control, captures the active opener on open, and restores it during cleanup on close. The close callback is held in a ref so callback identity changes do not restart the focus lifecycle.
- Verification: regression coverage asserts initial Tab and Shift+Tab containment and opener restoration; package lint, tests, build, and pack dry-run pass.
