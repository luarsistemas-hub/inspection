# Task 03 Memory

## Current state

- Completed on 2026-09-08. The declared task-local memory file was absent, so this file was created before implementation.
- `packages/inspection-design-system` is independently installable and publishes only compiled ESM, declarations, and opt-in CSS through an explicit export map.

## Decisions

- The canonical spec directory has `_prd.md` and `_techspec.md`; there is no `_spec.md`.
- ADR-005 requires a separately published, exact-versioned package rather than an npm workspace or cross-app source import.
- Version `0.1.0` is intentionally publishable with `publishConfig.access=restricted`; `private` is false because the registry controls package privacy.

## Touched surfaces

- `packages/inspection-design-system`: package manifest and lockfile, TypeScript/Vitest/ESLint/build configuration, source primitives/tokens, test contract, and README.

## Follow-up

- Downstream tasks 04–06 must install `@inspection/design-system@0.1.0` with an exact dependency version and import only the documented package exports/CSS.
