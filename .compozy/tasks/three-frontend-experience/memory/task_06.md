# Task 06 Workflow Memory

## Current state

- Implemented the standalone `apps/capture` Next.js PWA, including generated GraphQL client, capture-only transport, invitation route, PWA assets, Dockerfile, and mobile test configuration.

## Decisions

- Capture uses only the HttpOnly external-session cookie plus an in-memory CSRF proof; its transport has no OIDC branch or bearer-token storage.
- Local persistence uses `inspection-capture-v3`, responsibility-scoped drafts, corrupt-data quarantine, a quota guard, and server-final-state cleanup.
- The service worker caches only static shell assets and excludes routes, GraphQL, credentials, media, and signed URLs.

## Learnings

- The independent registry-backed clean install remains dependent on the private `@inspection/design-system@0.1.0` registry endpoint noted in shared memory; local verification used already available dependency artifacts.

## Touched surfaces

- `apps/capture/` package, route, GraphQL, auth, PWA, assets, container configuration, and unit/E2E tests.

## Follow-ups

- Runtime ingress, CORS deployment wiring, and removal of the legacy Capture route remain task 07 scope.
