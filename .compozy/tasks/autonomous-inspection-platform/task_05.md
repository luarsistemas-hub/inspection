---
status: pending
title: "Origin, Capture, Media & Directed Recapture"
type: backend
complexity: critical
---

# Task 5: Origin, Capture, Media & Directed Recapture

## Overview

Deliver the complete backend journey for accountless origin creation, guided inspection evidence, verified media, consent, provenance, and targeted correction. This task joins the established lifecycle, session, storage, and template contracts while preserving every original and restricting the external participant to one responsibility.

<critical>
- ALWAYS READ the PRD, the TechSpec, and their catalogs (`_user_stories.md`, `_tests.md`) before starting
- REFERENCE TECHSPEC for implementation details — do not duplicate here
- FOCUS ON "WHAT" — describe what needs to be accomplished, not how
- MINIMIZE CODE — show code only to illustrate current structure or problem areas
- TESTS REQUIRED — implement every test case assigned in ## Tests
</critical>

<requirements>
- Origin invitations MUST go to every marked verified channel and permit any recipient who has the link and valid OTP to access only that origin responsibility.
- Origin submission MUST contain at least one valid categorized photo with a nonblank description; activation MUST be automatic after blocking checks and version replacement MUST preserve history.
- External bootstrap MUST expose only the invited responsibility, effective template/reference/policy, versioned privacy disclosure, and no internal report/history data.
- Explicit acceptance of photo processing, AI analysis, and GPS use MUST be stored before capture; refusal MUST block progress.
- Every requirement MUST receive ready evidence or a permitted nonblank impossibility reason; incomplete submission MUST be explicit and classify at least `ATTENTION` later.
- Media operations MUST enforce supported types, 20 MiB originals, 200 active photos per inspection/stage, private multipart verification, immutable lineage, and described extra photos.
- GPS MUST accept accuracy of 50 m or better inside the guided 60-second window; required failure blocks, optional failure flags, and out-of-geofence capture only flags.
- Face/document-region detection MUST complete before submission; a participant false-positive declaration MUST allow the item with visible flag and audit.
- Submission MUST become immutable, revoke the two-hour session immediately, show confirmation only, and emit idempotent analysis intent.
- Directed recapture MUST create a new link/OTP/session, reopen only selected requirements, preserve originals, and finalize on correction or deadline.
- Property, construction, and cleaning capture behavior MUST be driven by the versioned template rather than segment-specific application forks.
</requirements>

## Subtasks

- [ ] 5.1 Deliver origin invitation, scoped access, capture, completion, automatic activation, replacement, and lineage slices.
- [ ] 5.2 Deliver external bootstrap, disclosure/acceptance, responsibility authorization, and confirmation-only terminal behavior.
- [ ] 5.3 Deliver requirement rendering data, answer drafts, descriptions, impossibility reasons, extras, and completeness decisions.
- [ ] 5.4 Deliver multipart media create/presign/complete operations and verification/derivative/screening consumers.
- [ ] 5.5 Deliver provenance, camera/gallery, GPS acquisition, geofence, device, quality, and hash metadata behavior.
- [ ] 5.6 Deliver sensitive-content block, retake/impossibility, participant false-positive, and audit behavior.
- [ ] 5.7 Deliver immutable complete/incomplete submission and analysis-event handoff.
- [ ] 5.8 Deliver system/manual directed recapture, selected-item responsibility, replacement lineage, expiry, and resubmission.
- [ ] 5.9 Deliver GraphQL external operations, origin/capture/media/recapture schemas, consumers, and contract fixtures.
- [ ] 5.10 Deliver tenant isolation, interruption, replay, concurrency, quota, session, and three-segment backend integration coverage.

## Implementation Details

Keep origin, invitations, capture, media, and recapture operations as separate vertical slices even when they share one external journey. Business state and evidence metadata live in capability schemas; bytes remain in private object storage. External resolvers derive tenant, inspection, asset, and responsibility from the server-side session and never trust widening identifiers from the client.

### Relevant Files

- `services/inspection/internal/features/{templates,assets,participants}/` — effective capture configuration supplied by Task 3.
- `services/inspection/internal/features/{inspections,projects}/` — responsibility and stage snapshots supplied by Task 4.
- `services/inspection/internal/platform/{security,objectstore,sensitivecontent,messaging,tenanttx}/` — boundary adapters supplied by Tasks 1–2; authoritative session state remains in invitation slices.
- `services/inspection/internal/contracts/events/` — versioned media/capture/recapture contracts.
- `.compozy/tasks/autonomous-inspection-platform/_user_stories.md` — US-008–US-010, US-015–US-020, US-023–US-024, and US-032.

### Dependent Files

- `services/inspection/internal/features/origins/` — origin invitation, capture, activation, and version queries.
- `services/inspection/internal/features/invitations/` — OTP/session operations and expiry/revocation integration.
- `services/inspection/internal/features/capture/` — bootstrap, metadata, answers, submission, and recapture submission.
- `services/inspection/internal/features/media/` — upload control, verification, derivatives, screening, and cleanup integration.
- `services/inspection/internal/features/recapture/` — request, deadline, selected requirements, and replacement lineage.
- `services/inspection/internal/platform/database/migrations/` — evidence schemas, immutable constraints, indexes, and RLS.
- `services/inspection/cmd/{inspection-api,inspection-worker}/main.go` — resolver/consumer registration.

### Related ADRs

- [ADR-002: Treat Evidence Metadata as Risk Signals, Not Proof of Authenticity](adrs/adr-002.md) — provenance and geofence semantics.
- [ADR-003: Support Recurring, Milestone, Manual, and Multi-Stage Inspection Lifecycles](adrs/adr-003.md) — stage capture context.
- [ADR-005: Use Scoped, Passwordless External Participation with Directed Recapture](adrs/adr-005.md) — external and correction journey.
- [ADR-006: Enforce Configurable Retention with Fixed Default Periods](adrs/adr-006.md) — consent and sensitive content.
- [ADR-008: Build a New Inspection Service as Vertical Slices](adrs/adr-008.md) — capability boundaries.
- [ADR-009: Isolate Tenants with PostgreSQL Row-Level Security](adrs/adr-009.md) — evidence isolation.
- [ADR-011: Coordinate Slices Through a Typed In-Process Bus](adrs/adr-011.md) — prerequisite lookup.
- [ADR-012: Use gqlgen and Browser-Appropriate Session Models](adrs/adr-012.md) — external GraphQL/session boundary.
- [ADR-013: Use Transactional Outbox, RabbitMQ, and Idempotent Consumers](adrs/adr-013.md) — media and analysis handoff.
- [ADR-014: Keep Media Private and Detect Sensitive Content Locally](adrs/adr-014.md) — media and screening contract.
- [ADR-015: Use One Next.js SPA and PWA for Dashboard and Capture](adrs/adr-015.md) — frontend-facing resume contract.
- [ADR-017: Design for the Confirmed MVP Capacity and Latency Envelope](adrs/adr-017.md) — capture/media capacity.

## Deliverables

- Versioned origin invitation, capture, activation, and replacement behavior.
- Complete external access, disclosure, consent, requirement, provenance, and submission APIs.
- Verified private media pipeline with immutable originals, derivatives, sensitive screening, and flags.
- Directed recapture with new authorization, selected-item correction, deadline finalization, and preserved lineage.
- Backend capture support for property, construction, and cleaning templates.
- Every test case assigned in `## Tests` implemented and passing **(REQUIRED)**

## Tests

Cases assigned from `_tests.md`, the test contract — read each ID's full definition there before writing tests.

- [ ] Unit: UT-030, UT-031, UT-032, UT-033, UT-060, UT-061, UT-063
- [ ] Integration: IT-071, IT-072, IT-073, IT-074, IT-075, IT-076, IT-077, IT-078, IT-079, IT-080, IT-081, IT-082, IT-083, IT-084, IT-085, IT-086, IT-087, IT-088, IT-089, IT-090, IT-091, IT-092, IT-093, IT-094, IT-095, IT-096, IT-097, IT-098, IT-099, IT-100, IT-141, IT-142, IT-143, IT-144, IT-145, IT-146, IT-147, IT-148, IT-149, IT-150, IT-151, IT-152, IT-153, IT-154, IT-155, IT-156, IT-157, IT-158, IT-159, IT-160, IT-161, IT-162, IT-163, IT-164, IT-165, IT-166, IT-167, IT-168, IT-169, IT-170, IT-171, IT-172, IT-173, IT-174, IT-175, IT-176, IT-177, IT-178, IT-179, IT-180, IT-181, IT-182, IT-183, IT-184, IT-185, IT-186, IT-187, IT-188, IT-189, IT-190, IT-191, IT-192, IT-193, IT-194, IT-195, IT-196, IT-197, IT-198, IT-199, IT-200, IT-221, IT-222, IT-223, IT-224, IT-225, IT-226, IT-227, IT-228, IT-229, IT-230, IT-231, IT-232, IT-233, IT-234, IT-235, IT-236, IT-237, IT-238, IT-239, IT-240, IT-311, IT-312, IT-313, IT-314, IT-315, IT-316, IT-317, IT-318, IT-319, IT-320, IT-411, IT-412, IT-473, IT-474, IT-475, IT-476, IT-477, IT-478, IT-509, IT-510, IT-511, IT-512, IT-513, IT-514, IT-515, IT-516, IT-517, IT-518, IT-519, IT-520, IT-521, IT-522, IT-523, IT-524, IT-525, IT-526, IT-549, IT-550, IT-555, IT-556, IT-557, IT-558, IT-559, IT-560, IT-563, IT-564, IT-565, IT-566

## Success Criteria

- Every assigned test case implemented and passing.
- External users can access and mutate only their active responsibility and cannot retrieve submitted evidence or internal results.
- Originals and replacements remain immutable and traceable through upload, screening, submission, and recapture.
- Required GPS/content/completeness rules block exactly where specified while optional/geofence/gallery conditions remain transparent flags.
- Duplicate, interrupted, expired, and concurrent flows preserve one correct business outcome and actionable recovery.
