---
status: pending
title: Complete Capture PWA and Recovery
type: frontend
complexity: high
---

# Task 7: Complete Capture PWA and Recovery

## Overview

Complete the standalone Capture PWA for US-031 and US-032 while preserving its invitation, OTP, HttpOnly-cookie, memory-CSRF, and one-responsibility trust boundary. Add public refusal, disclosure-bound consent, complete evidence metadata, honest local/upload/server states, multipart recovery, false-positive handling, safe reauthentication, and mobile accessibility without exposing internal product data.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- R1. Capture MUST keep /capture/[linkToken] as its only responsibility route, remove link secrets from browser history, and expose only the scoped origin, inspection, or recapture responsibility.
- R2. Capture MUST use link plus OTP, an HttpOnly external-session cookie, and memory-only CSRF; it MUST NOT store OIDC tokens or send X-Inspection-Membership-ID.
- R3. Expiry or revocation MUST discard authority while preserving clearly unauthorized local drafts for safe reauthentication.
- R4. Capture MUST use the authoritative disclosure version and record explicit photo-processing, AI-analysis, and GPS choices; refusal MUST call public revoke, clear authority, and show terminal safe guidance.
- R5. The journey MUST render responsibility status, policy/reference context, requirement instructions, media bounds, conditional descriptions, source policy, GPS, existing answers, recapture scope, and final/unavailable states without raw JSON.
- R6. Client guidance MUST enforce and explain JPEG/PNG/WebP/HEIC up to 20 MiB, 5 MiB parts, media-count, source, GPS, and metadata boundaries while retaining server authority.
- R7. Every item MUST distinguish draft, saved on this device, awaiting upload, uploading, server received/verified, and recoverable error, including queue/storage risk without promising background delivery while closed.
- R8. IndexedDB drafts MUST be responsibility-scoped and versioned, quarantine corrupt or mismatched data, serialize multi-tab recovery, reconcile server/object-store completion, and never duplicate evidence.
- R9. Impossibility MUST appear only when allowed and require incomplete confirmation; false-positive declaration MUST require a reason and apply only to currently sensitive-blocked media.
- R10. Completed, revoked, expired, invalidated, closed, or purged server state MUST dominate stale local state and prevent invalid upload or submission while preserving historical lineage.
- R11. Capture MUST use Task 4 generated operation types through its product-owned fetch transport and handle transport failures and payload userErrors consistently.
- R12. The journey MUST remain usable at 320 CSS pixels, 200-percent zoom, keyboard and screen reader, with visible focus, non-color states, reduced motion, announcements, and 44-pixel touch targets.
</requirements>

## Subtasks

- [ ] 7.1 Complete link exchange, OTP feedback, bootstrap, disclosure consent, public refusal/revoke, expiry, and reauthentication states.
- [ ] 7.2 Present origin, inspection, and recapture responsibilities as a guided requirement journey with policy, reference, progress, answers, and finality.
- [ ] 7.3 Complete camera/gallery, description, source, GPS, device context, media format/size/count, and requirement-bound validation experiences.
- [ ] 7.4 Expand IndexedDB and UI state for local drafts, upload queue, server receipt, capacity, cleanup, and restart recovery.
- [ ] 7.5 Reconcile multipart uploads across interruption, signed-part expiry, concurrent tabs, and server receipt without duplicate media.
- [ ] 7.6 Deliver policy-gated impossibility, explicit incomplete confirmation, false-positive declaration, and authoritative state reconciliation.
- [ ] 7.7 Make final capture and recapture submission idempotent and recover confirmed terminal state after interruption.
- [ ] 7.8 Replace manual operation typing with Task 4 generated result/variable types and consistent error handling.
- [ ] 7.9 Deliver externalized pt-BR, Capture-specific mobile composition, and WCAG 2.2 AA behavior.
- [ ] 7.10 Implement the four assigned Playwright cases and focused state/recovery unit regressions without claiming Task 8 cross-product evidence.

## Implementation Details

Follow the TechSpec Capture design and fixed operational boundaries. Current frontend refusal only changes local state, consent hard-codes choices/version, upload recovery trusts local completion, and restored answers lack enough media state for false-positive recovery. Consume the final Task 3 and Task 4 contracts; do not invent a browser-only substitute for authoritative media or multipart state. Preserve the service worker rule that never caches GraphQL, credentials, evidence, or signed URLs.

### Relevant Files

- `apps/capture/app/capture/[linkToken]/page.tsx` — current monolithic OTP, consent, capture, and submit route.
- `apps/capture/src/graphql/documents/capture.graphql` — Capture-owned query and mutation documents.
- `apps/capture/src/graphql/client.ts` — cookie/CSRF transport, typed payloads, userErrors, and session-expiry transition.
- `apps/capture/src/auth/capture-session.ts` — memory-only CSRF and explicit authority clearing.
- `apps/capture/src/pwa/drafts.ts` — responsibility-scoped versioned draft, queue, quarantine, and capacity state.
- `apps/capture/src/pwa/uploads.ts` — create, presign, PUT, complete, metadata, missing-part, retry, and canonical-state reconciliation.
- `apps/capture/src/components/capture-journey.tsx` and `recovery-queue.tsx` — recommended extracted guided and recovery composition.
- `apps/capture/src/capture/journey.ts` — recommended pure eligibility, progress, finality, and reconciliation state.
- `apps/capture/src/locales/pt-BR.ts` — recommended externalized copy.
- `apps/capture/app/styles.css` and `app/layout.tsx` — 320-pixel, progress, focus, identity, and locale framing.
- `apps/capture/public/sw.js` and `src/components/register-service-worker.tsx` — shell-only caching and no background evidence claim.
- `apps/capture/playwright.config.ts` — exact 320-pixel, Android Chromium, and iPhone WebKit projects.
- `apps/capture/tests/e2e/capture.spec.ts` and `authenticated-capture.spec.ts` — mocked and real-stack owned journeys.
- `apps/capture/tests/unit/drafts-and-transport.test.ts` and `uploads.test.ts` — local state, expiry, capacity, corruption, concurrency, and replay regressions.
- `apps/capture/next.config.ts` — preserve CSP and Permissions-Policy for camera, geolocation, API, and storage origins.

### Dependent Files

- `services/inspection/schema.graphqls` — Task 4 canonical externalCapture, invitation, upload, submission, and false-positive contract.
- `services/inspection/internal/features/capture/`, `invitations/`, `media/`, and `recapture/` — authoritative Task 3 scope, policy, session, upload, finality, and lineage behavior.
- `packages/inspection-design-system/src/` — consume neutral Task 4 primitives without importing another product shell.
- `apps/capture/codegen.ts` and `src/graphql/generated.ts` — Task 4 generated operation boundary; never hand-edit output.
- `apps/capture/Dockerfile`, `deploy/docker-compose.yml`, `.github/workflows/ci.yml`, and `scripts/verify.sh` — Task 8 runtime and parity consumers.
- `.compozy/tasks/graphql-frontend-capability-parity/_capability_matrix.md` — Task 8 records Capture journey evidence.

### Related ADRs

- [ADR-001: Full Capability Parity and Backend-First Delivery](adrs/ADR-001-full-capability-parity-and-delivery-order.md)
- [ADR-002: Admin Product and Design Direction](adrs/ADR-002-admin-product-and-design-direction.md)
- [ADR-004: Versioned Lifecycle, Origin Provenance and Retention](adrs/ADR-004-versioned-lifecycle-origin-and-retention.md)
- [ADR-007: Server-Resolved Membership Context](adrs/adr-007-server-resolved-membership-context.md)
- [ADR-008: Generated GraphQL Operations](adrs/adr-008-generated-graphql-operations.md)
- [ADR-011: Fixed Operational Boundaries](adrs/adr-011-operational-boundaries.md)

## Deliverables

- Complete public OTP, consent, capture, refusal/revoke, impossibility, false-positive, submission, and reauthentication experience.
- Honest responsibility-scoped IndexedDB, upload queue, multipart reconciliation, server receipt, recovery, cleanup, and terminal state.
- Generated typed operations over the preserved cookie/CSRF transport with no internal membership or OIDC authority.
- pt-BR mobile-first Capture composition with 320-pixel, multi-engine, keyboard, focus, announcement, zoom, and reduced-motion coverage.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract. Task 8 exclusively owns the cross-product recapture journey cases.

- [ ] E2E-031 — OTP, versioned disclosure choices, capture, multipart upload, metadata, and final submission at 320 pixels.
- [ ] E2E-032 — offline draft resume, permitted impossibility or false-positive, and safe reauthentication after expiry.
- [ ] E2E-064 — parameterized US-031 EC-1 through EC-10 Capture edge family.
- [ ] E2E-065 — parameterized US-032 EC-1 through EC-10 recovery edge family.

## Success Criteria

- Every assigned test case implemented and passing.
- Capture exposes only one authorized responsibility and never uses internal product authority.
- Local, upload, and server states remain truthful through restart, interruption, concurrent tabs, and expiry.
- Reconciliation uploads only missing work and never duplicates media or attaches it to invalid work.
- Refusal, impossibility, false-positive, and final submission return authoritative recoverable outcomes.
- The full journey passes at exactly 320 pixels and across the required mobile engines and accessibility checks.
