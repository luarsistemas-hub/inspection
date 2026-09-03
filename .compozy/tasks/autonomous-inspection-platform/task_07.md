---
status: pending
title: "Unified Next.js Experience & Acceptance"
type: frontend
complexity: critical
---

# Task 7: Unified Next.js Experience & Acceptance

## Overview

Deliver the single Next.js product experience for internal administration/review and external mobile capture, then prove every user journey against the completed backend. This task owns browser trust-zone separation, installable/resumable PWA behavior, accessibility, cross-segment acceptance, performance/security gates, and safe retirement of the Contract Service POC.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- One TypeScript Next.js application MUST use separate dashboard/capture route groups, clients, caches, and credentials while sharing generated GraphQL types, localization, and safe design primitives.
- Internal routes MUST implement OIDC Authorization Code with PKCE, keep access tokens only in memory, reauthenticate after reload, and show role/scope-appropriate navigation/actions.
- External routes MUST exchange/remove the link token, use the Secure HttpOnly session plus bound CSRF proof, and reveal only the current responsibility until confirmation.
- The capture experience MUST be mobile-first, Portuguese (Brazil), keyboard/screen-reader usable, visibly focused, non-color-dependent, and explicit about permissions and recovery.
- IndexedDB MUST preserve pending blobs, upload IDs, completed part ETags, hashes, metadata, answers, and progress across refresh/connectivity loss without caching protected responses or durable URLs.
- Final verification/submission MUST require connectivity and the UI MUST never claim server completion before media is ready.
- Dashboard workflows MUST cover tenant/access, participants, catalogs, assets, schedules, projects, inspections, reports, triage, audit, notification status, usage, and retention within scope.
- Capture workflows MUST cover origin, property, construction, cleaning, consent, GPS, sensitive content, complete/incomplete submission, and directed recapture.
- Playwright acceptance MUST implement all 34 E2E contracts with real public surfaces plus accessibility, mobile, geolocation, service worker, upload, auth, isolation, and failure assertions.
- The completed system MUST meet the confirmed latency/concurrency envelope and pass migration, security, local-compose, and smoke gates before the Contract Service source/binary/Swagger and public bucket setup are removed.
- POC retirement MUST target known source, generated binary, README, and Swagger artifacts explicitly and MUST preserve `services/contract/cmd/contract-service/.env` unless separately authorized.
</requirements>

## Subtasks

- [ ] 7.1 Establish the Next.js/TypeScript app, route groups, localization, design/accessibility primitives, GraphQL code generation, and production build.
- [ ] 7.2 Deliver internal PKCE authentication, membership/scope state, protected routing, navigation, and session renewal.
- [ ] 7.3 Deliver tenant, access, participant, catalog, asset, schedule, project, audit, retention, and usage management screens.
- [ ] 7.4 Deliver dashboard summary, triage, inspection comparison, report/history, notification status, and safe downloads.
- [ ] 7.5 Deliver external link exchange, OTP, disclosure/consent, two-hour session, CSRF, and confirmation-only routing.
- [ ] 7.6 Deliver guided origin/capture/recapture UI, reference overlay, requirement progress, GPS/geofence, sensitive-content, and recovery states.
- [ ] 7.7 Deliver installable PWA shell, safe service-worker caching, IndexedDB persistence, multipart resume, and online-only finalization.
- [ ] 7.8 Deliver Playwright fixtures and all property, construction, cleaning, administration, correction, privacy, and review journeys.
- [ ] 7.9 Deliver accessibility, responsive/mobile, browser-security, tenant-isolation, concurrency, latency, and visual report acceptance gates.
- [ ] 7.10 Remove the obsolete Contract Service and unsafe public MinIO setup only after all foundation and smoke gates pass.

## Implementation Details

The app is a real frontend boundary, not a BFF. Generated GraphQL types define client contracts; the browser sends internal bearer tokens directly to the API and relies on HttpOnly cookies only for external responsibilities. Keep protected GraphQL responses, evidence, report content, tokens, and signed URLs out of the service-worker cache and persistent credential storage.

### Relevant Files

- `services/inspection/internal/platform/graphql/` — completed schema and generated-operation source.
- `services/inspection/cmd/inspection-api/main.go` — browser-facing GraphQL/REST surface.
- `deploy/docker-compose.yml` — full local environment and browser origins.
- `services/contract/` — obsolete POC removed only after acceptance.
- `services/contract/cmd/contract-service/contract-service` — generated POC binary removed explicitly only after all gates.
- `services/contract/cmd/contract-service/.env` — user-owned local configuration that must not be read, copied, or removed implicitly.
- `.compozy/tasks/autonomous-inspection-platform/_user_stories.md` — all 34 public journeys and edge behavior.
- `.compozy/tasks/autonomous-inspection-platform/_tests.md` — Playwright and browser contract definitions.

### Dependent Files

- `apps/web/package.json`, lockfile, `apps/web/tsconfig.json`, `apps/web/next.config.*`, `apps/web/playwright.config.*`, `apps/web/codegen.*` — pinned frontend, GraphQL, and test toolchain.
- `apps/web/app/(dashboard)/` — internal administration, triage, report, and privacy route group.
- `apps/web/app/(capture)/capture/[linkToken]/` — concrete external entry route inside the capture route group.
- `apps/web/app/auth/callback/` — concrete PKCE callback route.
- `apps/web/src/{auth,graphql,features,components,locales,pwa}/` — trust-zone clients and shared product primitives.
- `apps/web/public/` — manifest, icons, and safe static service-worker assets.
- `apps/web/tests/` — Playwright fixtures, page objects where justified, and E2E cases.
- `deploy/docker-compose.yml` — web service and final private MinIO configuration.
- `services/contract/` — deleted only after migration/health/smoke success.

### Related ADRs

- [ADR-001: Use a Declarative Multi-Segment Inspection Product Model](adrs/adr-001.md) — generic frontend rendering.
- [ADR-002: Treat Evidence Metadata as Risk Signals, Not Proof of Authenticity](adrs/adr-002.md) — evidence wording and flags.
- [ADR-003: Support Recurring, Milestone, Manual, and Multi-Stage Inspection Lifecycles](adrs/adr-003.md) — project journeys.
- [ADR-004: Keep AI Reports Internal, Immutable, and Advisory](adrs/adr-004.md) — report trust boundary.
- [ADR-005: Use Scoped, Passwordless External Participation with Directed Recapture](adrs/adr-005.md) — capture access and correction.
- [ADR-006: Enforce Configurable Retention with Fixed Default Periods](adrs/adr-006.md) — disclosure/privacy UI.
- [ADR-007: Apply Hierarchical Tenant and Business-Unit Access](adrs/adr-007.md) — internal navigation/actions.
- [ADR-008: Build a New Inspection Service as Vertical Slices](adrs/adr-008.md) — frontend/API integration boundary.
- [ADR-012: Use gqlgen and Browser-Appropriate Session Models](adrs/adr-012.md) — PKCE/cookie separation.
- [ADR-014: Keep Media Private and Detect Sensitive Content Locally](adrs/adr-014.md) — capture/media UI.
- [ADR-015: Use One Next.js SPA and PWA for Dashboard and Capture](adrs/adr-015.md) — primary frontend decision.
- [ADR-016: Render Immutable PDFs Through Gotenberg](adrs/adr-016.md) — report acceptance.
- [ADR-017: Design for the Confirmed MVP Capacity and Latency Envelope](adrs/adr-017.md) — browser performance/load gates.

## Deliverables

- Production-built shared Next.js dashboard and installable capture PWA with separated trust zones.
- Complete internal administration, planning, triage, report, audit, notification, usage, and privacy experience.
- Complete external origin, inspection, multi-stage, upload, submission, and recapture experience.
- Playwright implementation of every assigned E2E/browser contract plus accessibility, security, and performance gates.
- Successful local full-system smoke and removal of the Contract Service POC/public bucket behavior.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there before writing tests.

- [ ] Unit: UT-052, UT-053
- [ ] Integration: IT-367, IT-386, IT-387, IT-388, IT-543, IT-544, IT-545, IT-546
- [ ] End-to-end: E2E-001, E2E-002, E2E-003, E2E-004, E2E-005, E2E-006, E2E-007, E2E-008, E2E-009, E2E-010, E2E-011, E2E-012, E2E-013, E2E-014, E2E-015, E2E-016, E2E-017, E2E-018, E2E-019, E2E-020, E2E-021, E2E-022, E2E-023, E2E-024, E2E-025, E2E-026, E2E-027, E2E-028, E2E-029, E2E-030, E2E-031, E2E-032, E2E-033, E2E-034

## Success Criteria

- Every assigned test case implemented and passing.
- All 34 user journeys pass through the actual browser/API/provider-fake surfaces in supported mobile and desktop viewports.
- No internal token is persisted, no external credential crosses into dashboard state, and no protected response/media/report is cached publicly.
- Capture recovers from refresh/connectivity loss and clearly requires connectivity for final verification/submission.
- Accessibility, tenant isolation, capacity, latency, migration, full Compose, and smoke gates pass before the POC is removed.
