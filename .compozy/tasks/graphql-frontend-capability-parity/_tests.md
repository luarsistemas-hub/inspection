# Test Specification: GraphQL and Frontend Capability Parity

Canonical test contract for GraphQL and Frontend Capability Parity. Companion to
_techspec.md. Derived from _user_stories.md and _techspec.md.

## Strategy

- Frameworks and harnesses: Go table-driven unit tests, existing GraphQL contract tests, PostgreSQL/RLS integration harness, and Vitest/Playwright in each product. Fakes are limited to object storage and delivery/provider I/O boundaries.
- Execution: Go tests run from repository root; each standalone application runs codegen, lint, unit, build and Playwright; the parity CI job runs a seeded Keycloak/PostgreSQL/RabbitMQ/MinIO stack.
- Conventions: Each state mutation asserts safe user error, idempotent replay, optimistic conflict and correlation where applicable. Browser cases include keyboard, focus, contrast/status announcement and 200 percent zoom checks for changed flows.

## Coverage Matrix

| Source | Behavior | Unit | Integration | E2E |
|---|---|---|---|---|
| US-001 | Contract and operation ownership | UT-001 to UT-004 | IT-001 | E2E-001 |
| US-002 | Membership tenant entry | UT-005 to UT-008 | IT-002 | E2E-002 |
| US-003 | Scoped Admin console | UT-009 to UT-012 | IT-003 | E2E-003 |
| US-004 to US-008 | Tenant, unit and access lifecycle | UT-013 to UT-032 | IT-004 to IT-008 | E2E-004 to E2E-008 |
| US-009 to US-017 | Participation, catalog, assets, origins and bulk | UT-033 to UT-068 | IT-009 to IT-017 | E2E-009 to E2E-017 |
| US-018 to US-021 | Policy, retention, purge and audit | UT-069 to UT-084 | IT-018 to IT-021 | E2E-018 to E2E-021 |
| US-022 to US-027 | Operational Dashboard and reports | UT-085 to UT-108 | IT-022 to IT-027 | E2E-022 to E2E-027 |
| US-028 to US-030 | Customer and notifications | UT-109 to UT-120 | IT-028 to IT-030 | E2E-028 to E2E-030 |
| US-031 to US-032 | Capture and offline recovery | UT-121 to UT-128 | IT-031 to IT-032 | E2E-031 to E2E-032 |
| US-033 | Legacy retirement | UT-129 to UT-132 | IT-033 | E2E-033 |

## Required Edge-Case Expansion

Each row below is a permanent parameterized case family. N is one distinct
case, not a range assertion: the implementation task must materialize all ten
named cases with the stated source edge condition and retain its stable suffix.

| Source rows | Mandatory distinct cases | Unit | Integration | E2E |
|---|---|---|---|---|
| US-001.EC-1 through US-001.EC-10 | invalid field, empty data, limits, denial, drift, generation interruption, retry, order, lifecycle, scale | UT-133.01 to UT-133.10 | IT-034.01 to IT-034.10 | E2E-034.01 |
| US-002.EC-1 through US-002.EC-10 | unknown/no membership, scale, inactive, multi-tab, provisioning, replay, deep link, session change, similar names | UT-134.01 to UT-134.10 | IT-035.01 to IT-035.10 | E2E-035.01 |
| US-003.EC-1 through US-003.EC-10 | filters, empty, long content, denial, conflict, interruption, duplicate, deep link, archive, scale | UT-135.01 to UT-135.10 | IT-036.01 to IT-036.10 | E2E-036.01 |
| US-004.EC-1 through US-008.EC-10 | validation, empty, limits, denial, conflict, interruption, replay, ordering, lifecycle, scale | UT-136.01 to UT-140.10 | IT-037.01 to IT-041.10 | E2E-037 to E2E-041 |
| US-009.EC-1 through US-017.EC-10 | validation, empty, limits, scope denial, conflict, interruption, replay, ordering, lifecycle, scale | UT-141.01 to UT-149.10 | IT-042.01 to IT-050.10 | E2E-042 to E2E-050 |
| US-018.EC-1 through US-021.EC-10 | validation, empty/default, limits, denial, concurrency, interruption, idempotency, order, lifecycle, scale | UT-150.01 to UT-153.10 | IT-051.01 to IT-054.10 | E2E-051 to E2E-054 |
| US-022.EC-1 through US-027.EC-10 | validation, empty, limits, scope, concurrency, interruption, replay, order, final state, scale | UT-154.01 to UT-159.10 | IT-055.01 to IT-060.10 | E2E-055 to E2E-060 |
| US-028.EC-1 through US-030.EC-10 | invalid input, empty, limits, non-disclosure, publication race, interruption, replay, order, lifecycle, scale | UT-160.01 to UT-162.10 | IT-061.01 to IT-063.10 | E2E-061 to E2E-063 |
| US-031.EC-1 through US-032.EC-10 | link/media validation, empty local state, limits, expired session, concurrency, interruption, replay, order, invalid work, device scale | UT-163.01 to UT-164.10 | IT-064.01 to IT-065.10 | E2E-064 to E2E-065 |
| US-033.EC-1 through US-033.EC-10 | malformed route, replacement empty state, hidden dependency, denial, inventory drift, interruption, bookmark, premature removal, history, scale | UT-165.01 to UT-165.10 | IT-066.01 to IT-066.10 | E2E-066 |

## Unit Tests

### Contract, authorization and data slices

- **UT-001** (happy): capability validator reads schema and matrix and accepts every owned query and mutation.
- **UT-002** (error): capability validator names an unmapped schema field.
- **UT-003** (concurrency): generated operation manifest rejects mismatched schema hash.
- **UT-004** (idempotency): mutation contracts require client mutation identity.
- **UT-005** (happy): membership resolver accepts an active membership owned by the OIDC subject.
- **UT-006** (error): membership resolver rejects a foreign, malformed, disabled or inactive context without existence disclosure.
- **UT-007** (state): tenant context switch clears prior product state before the next protected query.
- **UT-008** (idempotency): tenant bootstrap replay returns one Admin membership.
- **UT-013** (error): role/scope, effective-access, lifecycle and policy validators return safe field errors.
- **UT-033** (state): catalog/origin models preserve immutable version and provenance lineage.
- **UT-069** (concurrency): retention/publication/hold commands reject stale versions and retain the current state.
- **UT-085** (ordering): schedule, inspection, project and report transitions reject invalid predecessor state.
- **UT-109** (error): customer projection mapper excludes internal fields and unavailable publication content.
- **UT-121** (idempotency): Capture OTP/upload/metadata/submission and false-positive replay return one logical result.
- **UT-129** (happy): legacy inventory parser detects every route, document, script, package and deployment reference.

### Product components

- **UT-130** (state): Admin collection state distinguishes first-use, no-result, loading, stale, denied and error.
- **UT-131** (concurrency): Admin conflict UI retains draft and offers current server values.
- **UT-132** (state): Dashboard capability controls hide unavailable actions without changing server authorization.

## Integration Tests

### Server and external boundaries

- **IT-001**: gqlgen schema, generated Go output and generated operation artifacts agree after a canonical schema change.
- **IT-002**: two memberships for one subject select independent RLS tenant transactions; a foreign membership header returns denial without rows.
- **IT-003**: Admin route context, filters and detail route restore a valid tenant/scope after navigation.
- **IT-004**: tenancy/unit mutations use RLS, expected version and idempotency key with audit record.
- **IT-005**: delegated role/scope assignment and effective-access explanation agree with Authorizer outcomes.
- **IT-009**: participant import preview/apply streams CSV, records accepted/rejected/conflicted/unprocessed rows and does not duplicate replay.
- **IT-012**: profile/template/segment activation preserves immutable prior versions and blocks retired selection.
- **IT-016**: administrative origin upload verifies object digest/provenance and blocks activation before verification.
- **IT-018**: publication policy restricts customer data and delivery attempts record per destination/retry state.
- **IT-019**: legal hold applied during purge final check prevents media/object deletion.
- **IT-020**: completed purge leaves only allowlisted tombstone fields.
- **IT-021**: audit/usage export applies tenant scope and current filters, expires after 24 hours.
- **IT-022**: schedule materialization and cancellation preserve historical inspections.
- **IT-026**: report publication/invalidation changes active customer access immediately with no older-publication fallback.
- **IT-028**: customer portfolio/timeline/report/evidence return only allowed fields and time-limited authorized media.
- **IT-030**: notification preference selection accepts only verified active destinations and mark-read is idempotent.
- **IT-031**: Capture session requires OTP and consent before upload/metadata/submission.
- **IT-032**: multipart resume reconciles object-store completion with local draft and invalidated work.
- **IT-033**: legacy-removal CI inventory fails while any apps/web dependency remains.

## End-to-End Tests

### Owned journeys

- **E2E-001**: release owner opens parity evidence, follows an owned operation to its product journey and sees no raw GraphQL executor.
- **E2E-002**: Admin identity selects a tenant, sees persistent context, switches a second tab and cannot read foreign tenant data.
- **E2E-003**: administrative user navigates grouped Admin, filters a dense collection, opens detail/history and completes keyboard/zoom checks.
- **E2E-004**: tenant administrator updates tenant defaults and sees timezone context after refresh.
- **E2E-005**: organization administrator creates, impacts-checks and archives a unit.
- **E2E-006**: access administrator invites then disables a membership with channel outcomes.
- **E2E-007**: access administrator assigns scopes and reviews effective result.
- **E2E-008**: access administrator compares two memberships without mutation.
- **E2E-009**: participation administrator manages participant/contact, preview-imports and reviews row outcomes.
- **E2E-010**: administrator verifies contact then selects delivery destination.
- **E2E-011**: administrator publishes and activates a segment version.
- **E2E-012**: configuration administrator publishes and activates a template version.
- **E2E-013**: configuration administrator publishes, activates and retires an analysis profile.
- **E2E-014**: configuration administrator registers, edits and archives an asset.
- **E2E-015**: organization administrator sends origin capture invitation and reviews delivery.
- **E2E-016**: organization administrator uploads, activates and invalidates an origin version.
- **E2E-017**: administrator exports filtered authorized results.
- **E2E-018**: governance administrator configures restrictive publication and notification policy.
- **E2E-019**: governance administrator configures retention and applies/releases a legal hold.
- **E2E-020**: governance administrator requests deletion and observes terminal tombstone state.
- **E2E-021**: auditor filters, inspects and exports history.
- **E2E-022**: manager creates, updates and cancels a schedule.
- **E2E-023**: manager creates and transitions inspection with conflict recovery.
- **E2E-024**: manager creates project and executes eligible stage transitions.
- **E2E-025**: viewer sees triage but cannot mutate.
- **E2E-026**: manager downloads, publishes then invalidates report; customer access disappears.
- **E2E-027**: manager requests recapture and participant completes replacement.
- **E2E-028**: customer browses only authorized portfolio and timeline.
- **E2E-029**: customer opens published report and explicitly shared evidence, then receives safe unavailable state after invalidation.
- **E2E-030**: recipient marks notification read and manages verified preference.
- **E2E-031**: participant completes OTP, consent, capture, upload, metadata and submission on 320-pixel viewport.
- **E2E-032**: participant resumes offline draft, declares permitted impossibility or false positive, and reauthenticates after expiry.
- **E2E-033**: release owner passes complete parity gate, verifies intentional legacy URL behavior, then removes apps/web.
