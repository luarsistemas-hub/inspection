---
status: completed
title: Versioned Shared Design System
type: frontend
complexity: medium
---

# Task 3: Versioned Shared Design System

## Overview

Create the private, semantically versioned UI package that gives Admin, Dashboard, and Capture one recognizable product family without source-level coupling. The package owns accessible primitives and tokens only; each standalone application remains free to compose a product-specific layout.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- `packages/inspection-design-system` MUST be independently buildable and publishable as private `@inspection/design-system`.
- The package MUST use semantic immutable versions and MUST expose a minimal documented public export map.
- It MUST contain brand tokens, typography, spacing, color, focus, icon, layout, and accessible primitive contracts needed by all three products.
- It MUST NOT contain authentication, GraphQL, routing, product pages, authorization decisions, or domain rules.
- Components MUST meet WCAG 2.2 AA semantics, keyboard behavior, visible focus, non-color-only state, and reduced-motion behavior.
- Layout primitives MUST support 320, 360, 768, and 1440 px without horizontal overflow.
- Package consumers MUST pin an exact registry version rather than importing local source or using a workspace link.
- Build and publication output MUST exclude registry credentials and unrelated application source.
</requirements>

## Subtasks

- [x] 3.1 Establish the standalone package metadata, TypeScript/build configuration, export map, and private publication contract.
- [x] 3.2 Extract and normalize shared brand, typography, color, spacing, elevation, focus, and responsive tokens.
- [x] 3.3 Deliver the minimal accessible form, action, feedback, navigation, overlay, and content primitives required by the products.
- [x] 3.4 Deliver icons and product-identification primitives for unambiguous Admin, Dashboard, and Capture context.
- [x] 3.5 Document supported exports, versioning, accessibility behavior, and product-specific composition boundaries.
- [x] 3.6 Add package contract, accessibility, reduced-motion, and viewport unit coverage.
## Implementation Details

Follow ADR-005 and the TechSpec “Frontend Project Structure.” The current `apps/web` styles are input material, not a public API; extract only concrete reuse needed by all three applications and publish a built package that consumers install from the private registry.

Create `packages/inspection-design-system/package.json`, its independent lockfile, TypeScript/ESLint/Vitest/build configuration, `src/index.ts`, opt-in token/base CSS, feature-neutral React primitives/icons, tests, and README. Publish compiled ESM, declarations, and CSS through an explicit export map; React remains a peer dependency and CSS is declared as a side effect.

### Relevant Files

- `apps/web/app/styles.css` — existing visual tokens and global styles to assess for extraction.
- `apps/web/app/layout.tsx` — current shell, metadata, and font behavior.
- `apps/web/public/icon.svg` and `manifest.webmanifest` — existing product-family visual assets.
- `apps/web/src/components/` — current shared UI candidates.
- `apps/web/package.json` and `tsconfig.json` — pinned React/TypeScript baseline.
- `apps/web/vitest.config.ts` and unit-test patterns — package test baseline.
- `apps/web/app/(capture)/capture/[linkToken]/page.tsx` — touch, fieldset, status, and offline patterns that require generic primitives.

### Dependent Files

- `apps/admin/package.json`, `apps/dashboard/package.json`, and `apps/capture/package.json` — exact published-version consumers.
- Each frontend's root layout and product shell — product-specific composition of shared primitives.
- CI registry publication and dependency-install jobs in task 07.
- `.github/workflows/ci.yml` — task 07 consumes this package's build/publish commands and registry contract.

### Related ADRs

- [ADR-001: Split the Existing Web Experience into Three Independent Frontends](adrs/adr-001.md) — shared brand with distinct layouts.
- [ADR-005: Use Three Standalone Frontend Projects in One Repository](adrs/adr-005.md) — private package and no workspace coupling.

## Deliverables

- Independently built and tested private `@inspection/design-system` package.
- Minimal accessible token, icon, layout, form, action, feedback, and overlay API.
- Public export/versioning documentation and package publication metadata.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; read each full definition before implementation.

- [x] Unit: UT-067, UT-068, UT-069, UT-070 — primitive accessibility, responsive tokens, reduced motion, and export-boundary enforcement.

## Success Criteria

- Every assigned test case implemented and passing.
- All three apps can install the same exact immutable package version from the configured registry.
- No package export introduces product/domain coupling.
- Supported primitives pass the defined keyboard, focus, reduced-motion, and viewport contracts.
