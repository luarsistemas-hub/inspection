---
round: 2
round_created_at: 2026-09-08T03:34:39.793998Z
status: resolved
file: packages/inspection-design-system/src/primitives/dialog.tsx
line: 52
severity: medium
author: unknown
---

# Issue 001: Dialog usa ID de título duplicado

## Review Comment

Cada instância usa `aria-labelledby="inspection-dialog-title"` e `<h2 id="inspection-dialog-title">`. Duas instâncias montadas simultaneamente produzem IDs duplicados, tornando a referência ARIA ambígua e podendo fazer leitores de tela anunciarem o título incorreto. A renderização SSR reproduzida gera dois diálogos com o mesmo ID. Gere o ID com `useId()` por instância e use-o em `aria-labelledby`.

## Triage

- Decision: `VALID`
- Root cause: `Dialog` used the literal `inspection-dialog-title` for both
  `aria-labelledby` and the heading, so multiple mounted instances produced
  duplicate DOM IDs.
- Fix: generate one title ID per component instance with React `useId()` and
  use that value for both attributes.
- Regression coverage: the design-system dialog test now mounts two dialogs,
  asserts their `aria-labelledby` values are unique, and verifies each value
  matches its own heading ID.
- Verification: `npm test -- --run`, `npm run lint`, and `npm run build` in
  `packages/inspection-design-system` all exited 0; 6 tests passed.
