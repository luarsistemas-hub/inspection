# Task 07 Memory

## Current state

- Added the initial `apps/web` Next.js application with dashboard and capture route groups, a memory-only dashboard token holder, PKCE callback, cookie/CSRF capture client, static-only service-worker policy, and IndexedDB capture-draft persistence.
- `npm ci` installed the pinned web dependencies.
- `npm run test`, `npm run lint`, and `npm run build` pass for the web app.
- Playwright currently passes 108 browser executions across Chromium, Android emulation, and WebKit/iPhone. E2E-001 exercises Keycloak PKCE and the protected dashboard; E2E-003 creates a participant and confirms an email channel through the browser/API/PostgreSQL path. The remaining journey IDs are still superficial dashboard-smoke coverage and must be replaced before acceptance can be claimed.
- `go test ./...` passes from the repository root.
- With the local Compose PostgreSQL `inspection` database and non-superuser `inspection_runtime` role configured for tests, `go test -tags=integration ./services/inspection/...` passes; the dependency harness also passes uncached with `-count=1`.
- Web dependencies are now pinned to Next 15.5.25 and Vitest 3.2.7, with a PostCSS 8.5.28 override; `npm audit --audit-level=high` reports zero vulnerabilities.

## Decisions

- The capture service worker is restricted to same-origin static asset destinations; it does not cache GraphQL, pages, media, credentials, protected responses, or signed URLs.
- The Contract Service POC and its user-owned `.env` remain untouched: retirement is blocked until every assigned browser, performance, security, and cross-service acceptance gate has real passing evidence.
- The dashboard now reads real authorized GraphQL data for units, participants, catalog/asset counts, schedules, projects, inspections, audit, notifications, usage, retention, triage and summary. It can update a tenant with optimistic concurrency, create idempotent business units, and create/confirm participant email channels. Business-unit creation advances the tenant configuration version atomically, and migration 17 supplies a partial unique idempotency index.

## Remaining work

- Replace E2E-002 and E2E-004..034 with isolated, provider-backed public journeys. None may be treated as implemented merely because a named test passes.
- Implement the missing tenant/access operations (tenant/unit updates, internal invitation, role/scope changes and membership deactivation) and their dashboard UI; complete the remaining catalog, planning, report, retention, capture and recapture forms.
- Add assigned IT-367, IT-386..388 and IT-543..546 evidence, accessibility scans, capacity/load measurements, visual PDF checks, artifact retention, and a clean-volume Compose smoke before considering POC retirement.
- Do not mark Task 07 completed or remove the Contract Service while these conditions remain unmet. The project-local `scripts/validate-compozy-tasks.sh` now runs the pinned Compozy CLI in isolated configuration and validates successfully.
