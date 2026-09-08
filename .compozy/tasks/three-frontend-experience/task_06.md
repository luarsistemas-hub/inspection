---
status: pending
title: Standalone Capture PWA
type: frontend
complexity: high
---

# Task 6: Standalone Capture PWA

## Overview

Move the invitation-scoped Capture experience into its own mobile-first PWA and origin without changing submission finality. This task preserves origin, inspection, directed recapture, consent, camera, offline draft, multipart resume, and confirmation behavior while removing every internal-account and report concern.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- `apps/capture` MUST be a standalone Next.js PWA with its own package metadata, lockfile, generated client, build, tests, Dockerfile, manifest, icon, and service worker.
- Its only responsibility route MUST be `/capture/[linkToken]`; no Admin, Dashboard, OIDC callback, report, finding, or tenant route may exist.
- Capture MUST authenticate only by invitation link, OTP, short-lived external session cookie, and bound CSRF proof; it MUST never send or store OIDC tokens.
- Origin, inspection, and recapture modes MUST render only the current responsibility, policy, immutable reference, requirements, accepted answers, and safe state.
- Capture MUST preserve direct private multipart upload, per-item hash/metadata/GPS, limits, verification, impossibility, consent, and online finalization contracts.
- IndexedDB MUST store only responsibility-scoped recoverable drafts and multipart state, quarantine corrupt/version-mismatched data, and never restore expired/revoked/final authority.
- Recapture MUST accept only requested items and preserve original/replacement lineage without mutating the prior submission.
- Service-worker caching MUST exclude protected GraphQL responses, credentials, evidence, report content, and signed URLs.
- Finalization MUST reconcile server state after interruption and produce one immutable outcome plus confirmation-only UI under repetition/concurrency.
- The PWA MUST meet 320 px touch, Safari/iOS, Chrome/Android, camera, location, offline, accessibility, Portuguese, and performance contracts.
- No old `apps/web` Capture URL or local draft migration MUST be implemented because the product has not entered production.
</requirements>

## Subtasks

- [ ] 6.1 Establish standalone Capture package/build/codegen configuration, route shell, manifest, icons, and scoped service worker.
- [ ] 6.2 Move link exchange, OTP, consent, external session/CSRF, bootstrap, and confirmation-only routing.
- [ ] 6.3 Move guided origin and inspection requirements, camera/file/GPS/metadata, limits, impossibility, and progress experiences.
- [ ] 6.4 Move directed recapture selection, reasons, replacement lineage, deadline, and finalization experience.
- [ ] 6.5 Move/version IndexedDB draft persistence and multipart create/presign/upload/complete reconciliation.
- [ ] 6.6 Deliver permission denial, browser interruption, offline, quota, multi-tab, retry, revoked/expired, and maximum-scale recovery states.
- [ ] 6.7 Deliver mobile/responsive/accessibility behavior and assigned Vitest/integration/Playwright suites.
- [ ] 6.8 Point new invitations at the Capture origin without implementing legacy redirects.
## Implementation Details

Move the existing Capture trust zone rather than reimplementing its business rules. Follow the TechSpec “Capture Migration”; server bootstrap remains authoritative, service-worker scope is the Capture origin, and all final operations require current server authorization.

### Relevant Files

- `apps/web/app/(capture)/capture/[linkToken]/page.tsx` — current guided Capture route.
- `apps/web/src/auth/capture-session.ts` — external CSRF/session browser state.
- `apps/web/src/pwa/drafts.ts` and `uploads.ts` — IndexedDB and multipart resume logic.
- `apps/web/src/graphql/documents/capture.graphql` and capture operations in `client.ts` — Capture API contract.
- `apps/web/public/sw.js`, `manifest.webmanifest`, and `icon.svg` — PWA assets.
- `apps/web/src/components/register-service-worker.tsx` — service-worker registration.
- `apps/web/tests/unit/drafts.test.ts`, `capture-session.test.ts`, and `tests/e2e/journeys.spec.ts` — current coverage patterns.
- `services/inspection/internal/features/capture/`, `invitations/`, `media/`, `origins/`, and `recapture/` — existing server flows.
- `services/inspection/schema.graphqls` — canonical external Capture operations.

### Dependent Files

- `apps/capture/app/`, `src/auth/`, `src/pwa/`, `src/graphql/`, `public/`, and `tests/` — new standalone PWA ownership.
- `packages/inspection-design-system/` — exact versioned primitives.
- `services/inspection/internal/features/invitations/dispatch_capture_invitation/setup.go` — new invitation base URL.
- `deploy/docker-compose.yml`, CORS environment, smoke scripts, and reverse-proxy configuration — task 07 runtime wiring.
- `apps/web/` — task 07 removes it only after Capture parity.

### Related ADRs

- [ADR-004: Immutable Capture Is Separate from Result Review](adrs/adr-004.md) — Capture scope/finality.
- [ADR-005: Use Three Standalone Frontend Projects in One Repository](adrs/adr-005.md) — standalone app/origin.
- [ADR-006: Use Separate OIDC Clients with Shared SSO and One GraphQL API](adrs/adr-006.md) — external session boundary.
- [ADR-008: Publish Reports Through an Audited Visibility Ledger and Customer-Safe Projections](adrs/adr-008.md) — prevents result leakage into Capture.

## Deliverables

- Independently installable, buildable, deployable, and testable `apps/capture` PWA.
- Complete origin, inspection, recapture, offline/resume, immutable submission, and confirmation journeys.
- Capture-only GraphQL/session boundary, generated client, scoped service worker, and versioned IndexedDB state.
- Assigned unit, integration, mobile E2E, accessibility, interruption, concurrency, and scale coverage.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`; read each full definition before implementation.

- [ ] Unit: UT-059, UT-060, UT-061, UT-062, UT-063, UT-064, UT-065, UT-066, UT-073 — Capture draft, multipart, session, bootstrap, finalization, and transport.
- [ ] Integration: IT-121, IT-122, IT-123, IT-124, IT-125, IT-126, IT-127, IT-128, IT-129, IT-130, IT-131, IT-132, IT-133, IT-134, IT-135, IT-136, IT-137, IT-138, IT-139, IT-140, IT-141, IT-142, IT-143, IT-144, IT-145, IT-146, IT-147, IT-148, IT-149, IT-150, IT-151, IT-152, IT-153, IT-154, IT-155, IT-156, IT-157, IT-158, IT-159, IT-160, IT-161, IT-162, IT-163, IT-164, IT-165, IT-166, IT-167, IT-168, IT-169, IT-170, IT-171, IT-172, IT-173, IT-174, IT-175, IT-176, IT-177, IT-178, IT-179, IT-180 — all US-013 through US-018 edge contracts.
- [ ] End-to-end: E2E-053, E2E-054, E2E-055, E2E-056, E2E-057, E2E-058, E2E-059, E2E-060, E2E-061, E2E-062, E2E-063, E2E-064, E2E-065, E2E-066, E2E-067, E2E-068, E2E-069, E2E-070, E2E-071, E2E-072, E2E-073, E2E-074, E2E-075, E2E-076 — all US-013 through US-018 Capture acceptance journeys.

## Success Criteria

- Every assigned test case implemented and passing.
- Capture exposes exactly one authorized responsibility and no internal/report surface.
- Offline and interrupted work resumes without duplicate evidence or restored expired authority.
- iOS Safari and Android Chrome camera/upload/finalization journeys pass at supported viewports.
- Capture clean install, codegen, lint, unit tests, production build, and browser suite run independently.
