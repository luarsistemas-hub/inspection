# Task 05 Memory

## Current state

- Implemented the standalone `apps/dashboard` project with product-specific PKCE/session, route tree, GraphQL transport/documents/code generation, role capability composition, internal/customer shells, and notification polling/read/preferences UI.
- `npm run codegen:check`, `npm run lint`, `npm run test`, `npm run build`, and `npm run test:e2e` succeeded using the available local toolchain. The browser suite covered Chromium and mobile safe-auth boundaries.
- Clean installation is not verified: `npm ci --ignore-scripts` remained blocked waiting for the unavailable private package registry and was stopped after 90 seconds without output.

## Decisions

- Customer composition never issues internal summary, triage, report, or media queries; it uses only `customerPortfolio`, `customerTimeline`, `customerReport`, `customerEvidence`, and recipient notification projections.
- `VIEWER` and `CUSTOMER_VIEWER` get capability-derived navigation without mutation controls; the backend remains the authorization boundary for direct mutation attempts and revocation.
- Notification polling makes requests every 30 seconds only in visible documents, refreshes on focus, preserves the returned cursor, and marks read idempotently.

## Touched surfaces

- `apps/dashboard/` project, route shells, auth, GraphQL, Dashboard/notification features, generated schema types, and unit/E2E tests.

## Follow-ups

- Configure the private registry for `@inspection/design-system@0.1.0`, generate a complete integrity-bearing lockfile, then rerun `npm ci` and the independent package scripts.
- Add real API-backed integration and full end-to-end journeys after the dependent access, publication, customer projection, and notification slices are available in the local runtime.
