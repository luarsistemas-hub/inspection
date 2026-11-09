# Test Specification: Descriptive Checklist and Report

Canonical test contract for the PRD and TechSpec in this directory. IDs are stable and each is assigned to exactly one implementation task.

## Strategy

- Go slice tests and existing PostgreSQL/tenant integration harness cover core rules, GraphQL and RLS. Frontend unit tests stay in the current app frameworks; Playwright remains the E2E runner.
- Sanitized, recorded LiteLLM responses exercise deterministic projections without a live model. Separate corpus evaluation checks semantic quality before a prompt version is activated.
- Test names include the ID; each unit case has a class tag. Execute Go test/vet/build, affected frontend tests, generated-contract check, and app E2E after implementation.

## Coverage Matrix

| Source | Behavior | Unit | Integration | E2E |
| --- | --- | --- | --- | --- |
| US-001 | Configure a model's expected scope | UT-001 | IT-001 | E2E-001 |
| US-001.EC-1 | invalid input: An expected component without a room or name is rejected with a Portuguese validation message. | UT-002 | — | — |
| US-001.EC-2 | empty/missing: A model with no expected component list remains usable, but its reports show observed items without a completeness percentage. | UT-003 | — | — |
| US-001.EC-3 | limits: A model exceeding supported component/view limits is rejected without partial publication of the new version. | UT-004 | — | — |
| US-001.EC-4 | permissions: An administrator outside the tenant cannot read or change its models. | UT-005 | — | — |
| US-001.EC-5 | concurrency/repetition: A stale or repeated save cannot silently overwrite a newer model version. | UT-006 | — | — |
| US-001.EC-6 | ordering/state: Changing a model after an inspection starts affects new inspections only; the started inspection keeps its confirmed scope. | UT-007 | — | — |
| US-002 | Confirm the inspection scope | UT-008 | IT-002, IT-003 | E2E-001 |
| US-002.EC-1 | invalid input: An unknown profile or component identity is rejected before capture starts. | UT-009 | — | — |
| US-002.EC-2 | empty/missing: No expected list yields an “observed items” report without an invented completion score. | UT-010 | — | — |
| US-002.EC-3 | permissions: A manager without asset scope cannot confirm or change its inspection scope. | UT-011 | — | — |
| US-002.EC-4 | concurrency: Two managers confirming different scope revisions receive a conflict rather than silently replacing each other. | UT-012 | — | — |
| US-002.EC-5 | interruption/repetition: Retrying the same confirmation after a lost response returns the same effective scope. | UT-013 | — | — |
| US-002.EC-6 | ordering/state: Once capture has begun, an unannounced template edit does not change the inspection's scope; a later scope change is refused and requires a new inspection. | UT-014 | — | — |
| US-003 | Capture visible coverage | UT-015 | IT-004, IT-005, IT-007 | E2E-002, E2E-009 |
| US-003.EC-1 | invalid input: “Not applicable” or “impossible” without a reason is rejected with a Portuguese prompt for the reason. | UT-016 | — | — |
| US-003.EC-2 | empty/missing: A room with no usable media is recorded as not observed and cannot be reported as evaluated. | UT-017 | — | — |
| US-003.EC-3 | limits: More media than the capture limit produces the existing limit message and preserves already accepted media. | UT-018 | — | — |
| US-003.EC-4 | permissions: An expired or wrong-tenant capture link cannot add room media. | UT-019 | — | — |
| US-003.EC-5 | interruption/repetition: Retrying a received upload after reconnecting does not create duplicate evidence. | UT-020 | — | — |
| US-003.EC-6 | ordering/state: A supplemental photo arriving after final submission creates an explicit new analysis/report version or is refused; it never changes a published version in place. | UT-021 | — | — |
| US-004 | Record nonvisual checklist information | UT-022 | IT-006 | E2E-002 |
| US-004.EC-1 | invalid input: A negative key quantity or malformed meter reading is rejected with a field-specific Portuguese message. | UT-023 | — | — |
| US-004.EC-2 | empty/missing: An absent required record becomes a pending entry; an optional absent record may be omitted. | UT-024 | — | — |
| US-004.EC-3 | limits: Overlong notes are rejected without truncating a previously saved value. | UT-025 | — | — |
| US-004.EC-4 | permissions: A participant can change only records in the inspection named by the capture link. | UT-026 | — | — |
| US-004.EC-5 | interruption/repetition: Retried record submission returns the saved value once. | UT-027 | — | — |
| US-004.EC-6 | state: A correction after issuance creates a new report version; the previous reading remains in the old version. | UT-028 | — | — |
| US-005 | See descriptive observations | UT-029 | IT-008, IT-009 | E2E-004 |
| US-005.EC-1 | invalid input: An AI result citing an unknown evidence ID or another room is excluded or marked invalid; it is never shown as supported. | UT-030 | — | — |
| US-005.EC-2 | empty/missing: No detected items does not become “all expected items checked”; expected components remain gaps. | UT-031 | — | — |
| US-005.EC-3 | limits: If model output exceeds the item limit, the report exposes a truncation/processing limitation rather than implying complete cataloging. | UT-032 | — | — |
| US-005.EC-4 | permissions: A customer sees only the approved customer projection, never internal confidence or review backstage. | UT-033 | — | — |
| US-005.EC-5 | interruption: Failed or inconclusive analysis is shown distinctly from an empty successful result. | UT-034 | — | — |
| US-005.EC-6 | ordering: A late voice transcript never silently replaces a manager's decision or an issued report. | UT-035 | — | — |
| US-006 | Review and activate a baseline | UT-036 | IT-010 | E2E-007 |
| US-006.EC-1 | invalid input: An edit with an unsupported condition or foreign evidence ID is rejected. | UT-037 | — | — |
| US-006.EC-2 | empty/missing: Activation without any included item is rejected; the automatic report remains readable. | UT-038 | — | — |
| US-006.EC-3 | permissions: A participant or customer cannot activate a baseline. | UT-039 | — | — |
| US-006.EC-4 | concurrency: Competing item decisions or activation attempts cannot create two active versions for one asset. | UT-040 | — | — |
| US-006.EC-5 | repetition: Retrying successful activation returns the active version without duplicating it. | UT-041 | — | — |
| US-006.EC-6 | ordering/state: A superseded baseline cannot be edited; corrections create a new draft/version. | UT-042 | — | — |
| US-007 | Receive an automatic internal report | UT-043 | IT-011, IT-012, IT-013 | E2E-003, E2E-004 |
| US-007.EC-1 | empty/missing: A report with no usable room evidence says “no evidence recorded”, not “no defects”. | UT-044 | — | — |
| US-007.EC-2 | limits: A long report keeps headings, captions, and legible photos together without a QR-only page. | UT-045 | — | — |
| US-007.EC-3 | permissions: An unauthorized user cannot open the internal document or its private media. | UT-046 | — | — |
| US-007.EC-4 | concurrency/repetition: Repeated completion events with identical inputs issue one version, not duplicate versions. | UT-047 | — | — |
| US-007.EC-5 | interruption: A PDF render failure leaves a visible retryable artifact state while the snapshot remains intact. | UT-048 | — | — |
| US-007.EC-6 | state: AI catalog and findings that disagree show the damage finding and an unresolved conflict, not a positive summary. | UT-049 | — | — |
| US-008 | Read a customer-safe report | UT-050 | IT-014, IT-015 | E2E-005 |
| US-008.EC-1 | invalid input: An altered or unknown report-version reference is rejected without exposing another report. | UT-051 | — | — |
| US-008.EC-2 | empty/missing: No customer-visible evidence yields an explicit unavailable/no-evidence state, not an empty image slot. | UT-052 | — | — |
| US-008.EC-3 | permissions: A revoked or cross-tenant reader cannot fetch the document or media. | UT-053 | — | — |
| US-008.EC-4 | limits: A many-photo room uses an indexed annex while keeping a readable room summary. | UT-054 | — | — |
| US-008.EC-5 | interruption: A failed media fetch does not change the version's text or leak a storage URL. | UT-055 | — | — |
| US-008.EC-6 | state: An expired media link can be refreshed only for a still-authorized reader; the issued PDF is not silently rewritten. | UT-056 | — | — |
| US-009 | Keep issued versions stable | UT-057 | IT-023 | E2E-006 |
| US-009.EC-1 | invalid input: An unknown prior version cannot be overwritten or selected as an existing version. | UT-058 | — | — |
| US-009.EC-2 | empty/missing: A correction that changes no report facts produces no duplicate version. | UT-059 | — | — |
| US-009.EC-3 | permissions: Only authorized staff can request a correction; the reader sees only versions they are allowed to see. | UT-060 | — | — |
| US-009.EC-4 | concurrency: Simultaneous corrections serialize into distinct versions or an explicit conflict. | UT-061 | — | — |
| US-009.EC-5 | interruption/repetition: Retrying a version request after timeout does not issue a duplicate artifact. | UT-062 | — | — |
| US-009.EC-6 | ordering/state: A newly published prompt or template never changes an already issued version. | UT-063 | — | — |
| US-010 | Start an eligible move-out inspection | UT-064 | IT-017, IT-018, IT-019 | E2E-007 |
| US-010.EC-1 | invalid input: A baseline for another asset or tenant is rejected. | UT-065 | — | — |
| US-010.EC-2 | empty/missing: No approved baseline blocks creation with a Portuguese explanation and path to prepare one. | UT-066 | — | — |
| US-010.EC-3 | permissions: A manager lacking the asset scope cannot use its baseline. | UT-067 | — | — |
| US-010.EC-4 | concurrency: A baseline superseded during creation yields an explicit version choice or conflict; no silent switch occurs. | UT-068 | — | — |
| US-010.EC-5 | repetition: Retrying creation does not create duplicate move-out inspections. | UT-069 | — | — |
| US-010.EC-6 | ordering/state: Later baseline activation never changes the version pinned by an existing move-out inspection. | UT-070 | — | — |
| US-011 | Review an explainable comparison | UT-071 | IT-020, IT-021 | E2E-008 |
| US-011.EC-1 | invalid input: An AI comparison citing another room's item or evidence cannot support a conclusion. | UT-072 | — | — |
| US-011.EC-2 | empty/missing: An item without an equivalent baseline view is labeled “no basis to compare”. | UT-073 | — | — |
| US-011.EC-3 | limits: Many unchanged items remain accessible through filters without hiding the total denominator. | UT-074 | — | — |
| US-011.EC-4 | permissions: Only authorized staff can revise pairings or comparison decisions. | UT-075 | — | — |
| US-011.EC-5 | interruption: Failed comparison processing is shown as failure, not “no changes”. | UT-076 | — | — |
| US-011.EC-6 | ordering/state: Correcting a pairing after issue creates a new report version; the old conclusion remains available. | UT-077 | — | — |
| US-012 | Read a customer comparison | UT-078 | IT-022 | E2E-008 |
| US-012.EC-1 | invalid input: An invalid filter or version reference yields a safe error without data from another report. | UT-079 | — | — |
| US-012.EC-2 | empty/missing: A comparison with no supported change shows coverage and unresolved items rather than “everything unchanged” by default. | UT-080 | — | — |
| US-012.EC-3 | limits: A large baseline displays total and displayed counts consistently across filters and PDF sections. | UT-081 | — | — |
| US-012.EC-4 | permissions: A customer cannot see internal confidence, rejected items, or evidence outside the authorized report. | UT-082 | — | — |
| US-012.EC-5 | interruption: Temporarily unavailable prior media is labeled unavailable while its historical conclusion and version remain. | UT-083 | — | — |
| US-012.EC-6 | state: A later baseline version does not change the baseline number or text in the issued comparison. | UT-084 | — | — |
| US-013 | Evolve analysis safely | UT-085 | IT-024 | — |
| US-013.EC-1 | invalid input: A response incompatible with its pinned schema is rejected as a processing failure, not rendered as success. | UT-086 | — | — |
| US-013.EC-2 | empty/missing: Missing model revision metadata is recorded as unknown rather than invented. | UT-087 | — | — |
| US-013.EC-3 | limits: Candidate and image limits produce explicit diagnostics and visible report limitations where coverage is affected. | UT-088 | — | — |
| US-013.EC-4 | permissions: Tenant administrators cannot edit platform-wide prompt definitions through tenant preferences. | UT-089 | — | — |
| US-013.EC-5 | interruption/repetition: Retrying the same analysis attempt does not replace an issued snapshot; a requested reanalysis creates a new version. | UT-090 | — | — |
| US-013.EC-6 | ordering/state: Rolling back the active prompt applies to future analyses only and preserves old schema readers. | UT-091 | — | — |

### Component and interface coverage

| TechSpec component/interface | Unit success/error | Integration | E2E |
| --- | --- | --- | --- |
| Template compiler and publishTemplateVersion | UT-001, UT-002 | IT-001 | E2E-001 |
| ResolveScope/ConfirmScope and createInspection | UT-008, UT-009, UT-064, UT-065 | IT-002, IT-003, IT-017, IT-018 | E2E-001, E2E-007 |
| Capture coverage, externalCapture, recordComponentAnswer, submitCapture | UT-015, UT-016 | IT-004, IT-005, IT-007 | E2E-002, E2E-009 |
| Handover records and upsertHandoverRecord | UT-022, UT-023 | IT-006 | E2E-002 |
| Catalog/request parser and inventoryDraft | UT-029, UT-030, UT-085, UT-086 | IT-008, IT-009, IT-024 | E2E-004 |
| Inventory review and activateBaselineVersion | UT-036, UT-037 | IT-010 | E2E-007 |
| BuildChecklist/classification/publication | UT-043, UT-044, UT-057, UT-058 | IT-011, IT-012, IT-013, IT-023 | E2E-003, E2E-006 |
| Customer projection, report/customerReport/reportDownload/verifyReport | UT-050, UT-051, UT-078, UT-079 | IT-014, IT-015, IT-016 | E2E-005, E2E-008 |
| Comparison parser and report projection | UT-071, UT-072 | IT-020, IT-021, IT-022 | E2E-008 |
| New tenant tables and media provenance | UT-008, UT-009, UT-015, UT-016, UT-022, UT-023 | IT-025, IT-026 | E2E-002 |
| GraphQL generated contracts | UT-001, UT-002, UT-008, UT-009 | IT-027 | E2E-001 |
| Admin and manager scope UI | UT-092, UT-093, UT-094 | IT-001, IT-003 | E2E-001 |
| Capture PWA component and handover UI | UT-095, UT-096, UT-097, UT-098, UT-099 | IT-004, IT-006 | E2E-002, E2E-009 |
| Dashboard and customer report/comparison UI | UT-100, UT-101, UT-102, UT-103, UT-104, UT-105, UT-106 | IT-014, IT-022 | E2E-004, E2E-005, E2E-008 |

## Unit Tests

### templates/catalog.Compile (US-001)

- **UT-001** (happy): templates/catalog.Compile — US-001.AC-1: Given a new model, when I save a room plan, then its expected components and views are visible before an inspection uses it.
- **UT-002** (error): templates/catalog.Compile — US-001.EC-1: An expected component without a room or name is rejected with a Portuguese validation message.
- **UT-003** (boundary): templates/catalog.Compile — US-001.EC-2: A model with no expected component list remains usable, but its reports show observed items without a completeness percentage.
- **UT-004** (boundary): templates/catalog.Compile — US-001.EC-3: A model exceeding supported component/view limits is rejected without partial publication of the new version.
- **UT-005** (error): template tenant authorizer — US-001.EC-4: An administrator outside the tenant cannot read or change its models.
- **UT-006** (concurrency): templates/publish_template.Setup — US-001.EC-5: A stale or repeated save cannot silently overwrite a newer model version.
- **UT-007** (ordering): templates/resolve_template.Setup — US-001.EC-6: Changing a model after an inspection starts affects new inspections only; the started inspection keeps its confirmed scope.

### scope.ResolveScope/ConfirmScope (US-002)

- **UT-008** (happy): scope.ResolveScope/ConfirmScope — US-002.AC-1: Given a model, when I prepare an inspection, then I can choose property components, furnished property, or selected items and see the effective list.
- **UT-009** (error): scope.ResolveScope/ConfirmScope — US-002.EC-1: An unknown profile or component identity is rejected before capture starts.
- **UT-010** (boundary): scope.ResolveScope/ConfirmScope — US-002.EC-2: No expected list yields an “observed items” report without an invented completion score.
- **UT-011** (error): inspection scope authorizer — US-002.EC-3: A manager without asset scope cannot confirm or change its inspection scope.
- **UT-012** (concurrency): scope.ConfirmScope — US-002.EC-4: Two managers confirming different scope revisions receive a conflict rather than silently replacing each other.
- **UT-013** (idempotency): scope.ConfirmScope — US-002.EC-5: Retrying the same confirmation after a lost response returns the same effective scope.
- **UT-014** (ordering): scope.ResolveScope/ConfirmScope — US-002.EC-6: Once capture has begun, an unannounced template edit does not change the inspection's scope; a later scope change is refused and requires a new inspection.

### capture.EvaluateCoverage and recordComponentAnswer (US-003)

- **UT-015** (happy): capture.EvaluateCoverage and recordComponentAnswer — US-003.AC-1: Given a room, when I add a general view and details, then the checklist distinguishes observed, partial, not observed, not applicable, and impossible to capture.
- **UT-016** (error): capture.EvaluateCoverage and recordComponentAnswer — US-003.EC-1: “Not applicable” or “impossible” without a reason is rejected with a Portuguese prompt for the reason.
- **UT-017** (boundary): capture.EvaluateCoverage and recordComponentAnswer — US-003.EC-2: A room with no usable media is recorded as not observed and cannot be reported as evaluated.
- **UT-018** (boundary): capture/core.ValidateMediaLimit — US-003.EC-3: More media than the capture limit produces the existing limit message and preserves already accepted media.
- **UT-019** (error): capture external-session authorizer — US-003.EC-4: An expired or wrong-tenant capture link cannot add room media.
- **UT-020** (idempotency): media upload idempotency — US-003.EC-5: Retrying a received upload after reconnecting does not create duplicate evidence.
- **UT-021** (ordering): capture/core.Submit — US-003.EC-6: A supplemental photo arriving after final submission creates an explicit new analysis/report version or is refused; it never changes a published version in place.

### upsertHandoverRecord (US-004)

- **UT-022** (happy): upsertHandoverRecord — US-004.AC-1: Given a required key record, when I enter its type and handed-over quantity, then the report identifies it as a declaration and links its photo if one exists.
- **UT-023** (error): upsertHandoverRecord — US-004.EC-1: A negative key quantity or malformed meter reading is rejected with a field-specific Portuguese message.
- **UT-024** (boundary): upsertHandoverRecord — US-004.EC-2: An absent required record becomes a pending entry; an optional absent record may be omitted.
- **UT-025** (boundary): upsertHandoverRecord — US-004.EC-3: Overlong notes are rejected without truncating a previously saved value.
- **UT-026** (error): upsertHandoverRecord — US-004.EC-4: A participant can change only records in the inspection named by the capture link.
- **UT-027** (idempotency): upsertHandoverRecord — US-004.EC-5: Retried record submission returns the saved value once.
- **UT-028** (state): upsertHandoverRecord — US-004.EC-6: A correction after issuance creates a new report version; the previous reading remains in the old version.

### process_inventory_job.normalizeCatalog (US-005)

- **UT-029** (happy): process_inventory_job.normalizeCatalog — US-005.AC-1: Given a visible component, when analysis finishes, then its description, apparent condition, assessed portion, unassessed portion, review state, and evidence are available.
- **UT-030** (error): process_inventory_job.normalizeCatalog — US-005.EC-1: An AI result citing an unknown evidence ID or another room is excluded or marked invalid; it is never shown as supported.
- **UT-031** (boundary): process_inventory_job.normalizeCatalog — US-005.EC-2: No detected items does not become “all expected items checked”; expected components remain gaps.
- **UT-032** (boundary): process_inventory_job.normalizeCatalog — US-005.EC-3: If model output exceeds the item limit, the report exposes a truncation/processing limitation rather than implying complete cataloging.
- **UT-033** (error): reports/customer/core.Project — US-005.EC-4: A customer sees only the approved customer projection, never internal confidence or review backstage.
- **UT-034** (boundary): process_inventory_job.normalizeCatalog — US-005.EC-5: Failed or inconclusive analysis is shown distinctly from an empty successful result.
- **UT-035** (ordering): inventory/core regeneration guard — US-005.EC-6: A late voice transcript never silently replaces a manager's decision or an issued report.

### inventory/core.Service decision and activation (US-006)

- **UT-036** (happy): inventory/core.Service decision and activation — US-006.AC-1: Given an unreviewed report, when I inspect an item, then I can see original suggestion, source evidence, and my correction without changing the issued version.
- **UT-037** (error): inventory/core.Service decision and activation — US-006.EC-1: An edit with an unsupported condition or foreign evidence ID is rejected.
- **UT-038** (boundary): inventory/core.Service decision and activation — US-006.EC-2: Activation without any included item is rejected; the automatic report remains readable.
- **UT-039** (error): inventory/core.Service decision and activation — US-006.EC-3: A participant or customer cannot activate a baseline.
- **UT-040** (concurrency): inventory/core.Service decision and activation — US-006.EC-4: Competing item decisions or activation attempts cannot create two active versions for one asset.
- **UT-041** (idempotency): inventory/core.Service decision and activation — US-006.EC-5: Retrying successful activation returns the active version without duplicating it.
- **UT-042** (ordering): inventory/core.Service decision and activation — US-006.EC-6: A superseded baseline cannot be edited; corrections create a new draft/version.

### reports/handle_classified.BuildChecklist (US-007)

- **UT-043** (happy): reports/handle_classified.BuildChecklist — US-007.AC-1: Given completed analysis, when the report is generated, then each room includes observed components, limitations, numbered evidence, and findings even if there are no alerts.
- **UT-044** (boundary): reports/handle_classified.BuildChecklist — US-007.EC-1: A report with no usable room evidence says “no evidence recorded”, not “no defects”.
- **UT-045** (boundary): reports/render_pdf.Render — US-007.EC-2: A long report keeps headings, captions, and legible photos together without a QR-only page.
- **UT-046** (error): report query authorizer — US-007.EC-3: An unauthorized user cannot open the internal document or its private media.
- **UT-047** (concurrency): reports/generate_snapshot.CreateSnapshotInTx — US-007.EC-4: Repeated completion events with identical inputs issue one version, not duplicate versions.
- **UT-048** (boundary): report PDF job handler — US-007.EC-5: A PDF render failure leaves a visible retryable artifact state while the snapshot remains intact.
- **UT-049** (state): reports/handle_classified.BuildChecklist — US-007.EC-6: AI catalog and findings that disagree show the damage finding and an unresolved conflict, not a positive summary.

### reports/customer/core.Project (US-008)

- **UT-050** (happy): reports/customer/core.Project — US-008.AC-1: Given authorization for a customer report, when I open HTML or PDF of the same version, then descriptions, limitations, dates, evidence identifiers, and revision labels agree.
- **UT-051** (error): report version resolver — US-008.EC-1: An altered or unknown report-version reference is rejected without exposing another report.
- **UT-052** (boundary): reports/customer/core.Project — US-008.EC-2: No customer-visible evidence yields an explicit unavailable/no-evidence state, not an empty image slot.
- **UT-053** (error): customer report authorizer — US-008.EC-3: A revoked or cross-tenant reader cannot fetch the document or media.
- **UT-054** (boundary): reports/render_pdf.Render — US-008.EC-4: A many-photo room uses an indexed annex while keeping a readable room summary.
- **UT-055** (boundary): customer media resolver — US-008.EC-5: A failed media fetch does not change the version's text or leak a storage URL.
- **UT-056** (state): customer media resolver — US-008.EC-6: An expired media link can be refreshed only for a still-authorized reader; the issued PDF is not silently rewritten.

### reports/generate_snapshot.CreateSnapshotInTx (US-009)

- **UT-057** (happy): reports/generate_snapshot.CreateSnapshotInTx — US-009.AC-1: Given an issued report, when I correct an item or supplemental record, then the old report remains retrievable with its original content.
- **UT-058** (error): reports/generate_snapshot.CreateSnapshotInTx — US-009.EC-1: An unknown prior version cannot be overwritten or selected as an existing version.
- **UT-059** (boundary): reports/generate_snapshot.CreateSnapshotInTx — US-009.EC-2: A correction that changes no report facts produces no duplicate version.
- **UT-060** (error): report correction authorizer — US-009.EC-3: Only authorized staff can request a correction; the reader sees only versions they are allowed to see.
- **UT-061** (concurrency): reports/generate_snapshot.CreateSnapshotInTx — US-009.EC-4: Simultaneous corrections serialize into distinct versions or an explicit conflict.
- **UT-062** (idempotency): reports/generate_snapshot.CreateSnapshotInTx — US-009.EC-5: Retrying a version request after timeout does not issue a duplicate artifact.
- **UT-063** (ordering): reports/generate_snapshot.CreateSnapshotInTx — US-009.EC-6: A newly published prompt or template never changes an already issued version.

### inspections/core.Service.Create (US-010)

- **UT-064** (happy): inspections/core.Service.Create — US-010.AC-1: Given an approved baseline, when I start move-out work, then its selected version and asset are shown and pinned to the inspection.
- **UT-065** (error): inspections/core.Service.Create — US-010.EC-1: A baseline for another asset or tenant is rejected.
- **UT-066** (boundary): inspections/core.Service.Create — US-010.EC-2: No approved baseline blocks creation with a Portuguese explanation and path to prepare one.
- **UT-067** (error): inspections/core.Service.Create — US-010.EC-3: A manager lacking the asset scope cannot use its baseline.
- **UT-068** (concurrency): inspections/core.Service.Create — US-010.EC-4: A baseline superseded during creation yields an explicit version choice or conflict; no silent switch occurs.
- **UT-069** (idempotency): inspections/core.Service.Create — US-010.EC-5: Retrying creation does not create duplicate move-out inspections.
- **UT-070** (ordering): inspections/core.Service.Create — US-010.EC-6: Later baseline activation never changes the version pinned by an existing move-out inspection.

### process_inventory_job.Compare and report comparison projection (US-011)

- **UT-071** (happy): process_inventory_job.Compare and report comparison projection — US-011.AC-1: Given equivalent entry and current views, when comparison finishes, then each conclusion opens both linked evidence views and identifies the baseline version.
- **UT-072** (error): process_inventory_job.Compare and report comparison projection — US-011.EC-1: An AI comparison citing another room's item or evidence cannot support a conclusion.
- **UT-073** (boundary): process_inventory_job.Compare and report comparison projection — US-011.EC-2: An item without an equivalent baseline view is labeled “no basis to compare”.
- **UT-074** (boundary): process_inventory_job.Compare and report comparison projection — US-011.EC-3: Many unchanged items remain accessible through filters without hiding the total denominator.
- **UT-075** (error): comparison revision authorizer — US-011.EC-4: Only authorized staff can revise pairings or comparison decisions.
- **UT-076** (boundary): process_inventory_job.Compare and report comparison projection — US-011.EC-5: Failed comparison processing is shown as failure, not “no changes”.
- **UT-077** (ordering): reports/generate_snapshot.CreateSnapshotInTx — US-011.EC-6: Correcting a pairing after issue creates a new report version; the old conclusion remains available.

### reports/customer/core.ProjectComparison (US-012)

- **UT-078** (happy): reports/customer/core.ProjectComparison — US-012.AC-1: Given an authorized comparison report, when I read a changed item, then I can see baseline version, current evidence, prior evidence, status, and limitation.
- **UT-079** (error): customer comparison filter resolver — US-012.EC-1: An invalid filter or version reference yields a safe error without data from another report.
- **UT-080** (boundary): reports/customer/core.ProjectComparison — US-012.EC-2: A comparison with no supported change shows coverage and unresolved items rather than “everything unchanged” by default.
- **UT-081** (boundary): reports/render_pdf.Render — US-012.EC-3: A large baseline displays total and displayed counts consistently across filters and PDF sections.
- **UT-082** (error): reports/customer/core.ProjectComparison — US-012.EC-4: A customer cannot see internal confidence, rejected items, or evidence outside the authorized report.
- **UT-083** (boundary): reports/customer/core.ProjectComparison — US-012.EC-5: Temporarily unavailable prior media is labeled unavailable while its historical conclusion and version remain.
- **UT-084** (state): reports/customer/core.ProjectComparison — US-012.EC-6: A later baseline version does not change the baseline number or text in the issued comparison.

### prompt.ValidateTask and inventory generation selection (US-013)

- **UT-085** (happy): prompt.ValidateTask and inventory generation selection — US-013.AC-1: Given a report version, when I inspect its provenance, then I can identify effective analysis mode, prompt revision, model revision, applied scope, and evidence set.
- **UT-086** (error): prompt.ValidateTask and inventory generation selection — US-013.EC-1: A response incompatible with its pinned schema is rejected as a processing failure, not rendered as success.
- **UT-087** (boundary): prompt.ValidateTask and inventory generation selection — US-013.EC-2: Missing model revision metadata is recorded as unknown rather than invented.
- **UT-088** (boundary): prompt.ValidateTask and inventory generation selection — US-013.EC-3: Candidate and image limits produce explicit diagnostics and visible report limitations where coverage is affected.
- **UT-089** (error): analysis prompt authorizer — US-013.EC-4: Tenant administrators cannot edit platform-wide prompt definitions through tenant preferences.
- **UT-090** (idempotency): prompt.ValidateTask and inventory generation selection — US-013.EC-5: Retrying the same analysis attempt does not replace an issued snapshot; a requested reanalysis creates a new version.
- **UT-091** (ordering): prompt.ValidateTask and inventory generation selection — US-013.EC-6: Rolling back the active prompt applies to future analyses only and preserves old schema readers.

### Additional acceptance-criteria cases

Added after the acceptance-criteria trace in the final section found stories without a dedicated case.

- **UT-107** (happy): templates/catalog.Compile — US-001.AC-2: A model with required keys and meters keeps them as handover records in their own section, not as rooms.
- **UT-108** (happy): scope.ResolveScope — US-002.AC-2: A selected-items profile puts exactly the selected components in the denominator and exposes the applied profile and selection.
- **UT-109** (boundary): capture recapture suggestion — US-003.AC-2: A PARTIAL or NOT_OBSERVED component yields a suggestion naming room, component and missing view from the pinned views, with no condition claim and no submission block.
- **UT-110** (happy): reports/handle_classified.BuildChecklist — US-004.AC-2: Meter type, reading, unit, date and evidence appear in the handover section, separate from visual condition.
- **UT-111** (boundary): process_inventory_job.normalizeCatalog — US-005.AC-2: Material, function, age or ownership without visual support or declaration stays unknown; a declared value carries its declaration source.
- **UT-112** (boundary): process_inventory_job.normalizeCatalog — US-005.AC-3: A visible object matching no pinned component is stored with null component_key, no condition and no baseline eligibility.
- **UT-113** (state): reports/handle_classified.BuildChecklist — US-005.AC-4: Conflicting voice and visual statements both remain visible with their sources and the unresolved difference.
- **UT-114** (boundary): inventory/core.Service decision and activation — US-006.AC-3: Pending items are recorded as not reviewed in the activation record and never appear as approved inventory.
- **UT-115** (boundary): reports/handle_classified.BuildChecklist — US-007.AC-2: Unreviewed AI items and coverage gaps appear with the exact denominator, or "unknown scope" when none exists, and do not block issue.
- **UT-117** (state): process_inventory_job.Compare and report comparison projection — US-011.AC-4: A prior damage still present keeps current condition and historical change as separate fields.
- **UT-118** (error): reports/customer/core.ProjectComparison — US-012.AC-3: No comparison output states tenant responsibility, normal wear or repair cost.
- **UT-119** (boundary): process_inventory_job diagnostics — US-013.AC-3: Dropped, invalid and truncated candidate counts are recorded by reason, and logs contain no image or transcript content.

### Frontend components (Admin, Capture, Dashboard)

- **UT-092** (happy): Admin template scope form — given kitchen with piso and armários, saving v3 displays both expected components (US-001).
- **UT-093** (error): Admin template scope form — given duplicate component key piso, saving shows the Portuguese field error and retains draft input (US-001.EC-1).
- **UT-094** (concurrency): Dashboard scope form — given stale expectedVersion=1 after version 2, confirmation shows conflict and reload action (US-002.EC-4).
- **UT-095** (happy): Capture component card — given kitchen.piso=NOT_OBSERVED, submitting displays one remaining gap (US-003).
- **UT-096** (error): Capture component card — given NOT_APPLICABLE with blank reason, submit stays on the card and shows the reason error (US-003.EC-1).
- **UT-097** (idempotency): Capture offline retry — given saved answer version 2, reconnect reuses that answer and does not add another row (US-003.EC-5).
- **UT-098** (happy): Capture meter form — given 123.4 kWh and a date, review shows exactly that reading/unit/date (US-004).
- **UT-099** (error): Capture meter form — given reading -1, the field error appears and previous value remains (US-004.EC-1).
- **UT-100** (happy): Dashboard checklist report — given NORMAL and zero findings with one catalog item, room evidence and item remain visible (US-007).
- **UT-101** (state): Dashboard checklist report — given AI status FAILED, the screen says “Falha na análise” and omits “sem alertas” (US-007.AC-3).
- **UT-102** (error): Customer report component — given internal confidence and rejected suggestion, neither appears in customer HTML (US-008.AC-2).
- **UT-103** (boundary): Dashboard coverage widget — given zero expected components, it says “Escopo não definido” and shows no percentage (US-002.EC-2).
- **UT-104** (happy): Dashboard comparison filters — given 20 baseline items with six preview rows, the header says six of twenty and filters preserve total (US-012).
- **UT-105** (boundary): Dashboard comparison view — given entry without interior view, item displays “Sem base para comparar” (US-011.EC-2).
- **UT-106** (error): Customer comparison view — given unavailable origin media, the page labels it unavailable without exposing the storage URL (US-012.EC-5).
- **UT-116** (happy): Dashboard report history — given versions 1 and 2, the list shows version, issue time, effective scope, review state and prior version (US-009.AC-2).

## Integration Tests

- **IT-001**: publishTemplateVersion with schemaVersion=3, room kitchen and component keys piso/armarios returns a template version that resolves both keys.
- **IT-002**: createInspection with a valid selected-items override stores its scope digest in the same transaction as inspection and capture draft.
- **IT-003**: confirmInspectionScope with expectedVersion=1 before capture persists revision 2 and returns its effective scope.
- **IT-004**: externalCapture with the inspection link returns only its pinned scope and saved component answers.
- **IT-005**: recordComponentAnswer with kitchen.piso=NOT_OBSERVED and expectedVersion=1 persists version 2 as a coverage gap.
- **IT-006**: upsertHandoverRecord with a meter reading of 123.4 kWh persists the reading, unit, declarant and linked evidence.
- **IT-007**: submitCapture(confirmIncomplete=true) with an expected kitchen floor view absent stores the submission with one coverage gap.
- **IT-008**: INVENTORY_CATALOG for furnished scope with zero voice notes sends the furnished visual component policy.
- **IT-009**: Catalog v2 response containing an item with a foreign evidence ID drops that candidate and records the rejected-evidence count.
- **IT-010**: activateBaselineVersion after ACCEPTED, EDITED, ADDED, PENDING and REJECTED decisions includes only the first three.
- **IT-011**: A terminal COMPLETED catalog with one sound item and zero findings produces report snapshot v3 with that item, evidence, review label and coverage count.
- **IT-012**: A FAILED catalog generation produces a published v3 snapshot with FAILED status and no normality claim.
- **IT-013**: Delivering the same classified event twice produces exactly one PUBLISHED row and one snapshot version.
- **IT-014**: customerReport on a v3 snapshot retains allowed descriptions and limitations while omitting internal findings/confidence/notes.
- **IT-015**: reportDownload(PDF_CUSTOMER) renders from the pinned customer projection and returns an artifact for the requested snapshot.
- **IT-016**: verifyReport with a valid code/version returns only report number, digest, date, version and publication status.
- **IT-017**: createInspection for move-out with a same-asset approved baseline pins that exact baseline ID.
- **IT-018**: A scheduled move-out creation without approved baseline is rejected by the same shared creation guard.
- **IT-019**: An existing media-only comparison report v2 remains readable after the new creation gate and v3 migrations.
- **IT-020**: INVENTORY_COMPARE with a confirmed same-room pair returns the expected result for that one baseline item.
- **IT-021**: A movable item absent only from its former location, with incomplete room coverage, remains NOT_OBSERVED.
- **IT-022**: Comparison v3 report projects the six outcome counts and original baseline version for a six-item fixture.
- **IT-023**: Correcting a published pairing creates v2 while v1 canonical JSON, HTML and PDF digests remain unchanged.
- **IT-024**: A v1 prompt and v2 report fixture still decode after v2 catalog/v3 report rollout.
- **IT-025**: With tenant A context, RLS denies reading a tenant B component-answer row.
- **IT-026**: A gallery EXIF date differing from server receipt is stored separately and presented as a review flag.
- **IT-027**: Running scripts/generate-graphql-contracts.sh twice yields identical gqlgen, TypeScript and operation-manifest files.
- **IT-028**: publishTemplateVersion with duplicate component key piso returns userErrors on definition and creates no version.
- **IT-029**: createInspection with a component identity from another tenant returns userErrors and persists no inspection.
- **IT-030**: confirmInspectionScope with expectedVersion=1 after the scope reached version 2 returns a conflict userError.
- **IT-031**: confirmInspectionScope after the first accepted photo returns an invalid-state userError and keeps the old scope.
- **IT-032**: externalCapture with an expired link returns no scope or answer data.
- **IT-033**: recordComponentAnswer with a foreign-room evidence ID returns userErrors and stores no answer.
- **IT-034**: recordComponentAnswer with stale expectedVersion returns a conflict userError and preserves the current answer.
- **IT-035**: upsertHandoverRecord with meter reading -1 returns a Portuguese reading error and keeps the previous value.
- **IT-036**: upsertHandoverRecord with media from another inspection returns userErrors and stores no record.
- **IT-037**: submitCapture(confirmIncomplete=false) with missing expected kitchen floor view returns a pending-coverage userError.
- **IT-038**: submitCapture(confirmIncomplete=true) with a failed attached photo returns a media-verification userError.
- **IT-039**: INVENTORY_CATALOG with furnished scope and one voice note retains the same visual scope while adding the note as a declaration.
- **IT-040**: activateBaselineVersion with zero included items returns userErrors and creates no active version.
- **IT-041**: When aiAnalysis=false, classification publishes a NOT_AUTHORIZED report with no normality claim. Per PRD D-1 consent stays mandatory, so this case runs as a projection test on a NOT_AUTHORIZED fixture (no producer in this release); FAILED/INCONCLUSIVE are covered by IT-012.
- **IT-042**: customerReport requested by a revoked membership returns no report or private media.
- **IT-043**: A PDF renderer failure leaves the snapshot intact and the PDF job retryable.
- **IT-044**: verifyReport with an unknown code returns no personal fields or media URL.
- **IT-045**: createInspection for move-out with no baseline returns a Portuguese baseline-required userError.
- **IT-046**: createInspection for move-out with a draft-only baseline returns a Portuguese baseline-required userError.
- **IT-047**: createInspection for move-out with another asset’s baseline returns userErrors and persists no inspection.
- **IT-048**: createInspection for move-out with a media-only legacy origin returns a catalog-first userError.
- **IT-049**: INVENTORY_COMPARE with a pair from another room rejects the pair and records a limitation.
- **IT-050**: INVENTORY_COMPARE with no equivalent current view returns NOT_OBSERVED for that baseline item.
- **IT-051**: A movable item with confirmed whole-room coverage and empty location yields MISSING with a supported pair.
- **IT-052**: An incompatible pinned catalog schema produces a terminal processing failure without rendering its fields.
- **IT-053**: With tenant A context, RLS denies updating tenant B scope and handover rows.
- **IT-054**: Device capture time absent is stored with SERVER_FALLBACK source instead of claiming a device timestamp.
- **IT-055**: A submitted v3 inspection with one expected component not observed publishes a report that shows that component as a gap in the denominator (US-003.AC-3).
- **IT-056**: A v3 inspection with a required key record absent publishes a report with that record as a pending entry and is not blocked (US-004.AC-3, US-007.AC-4).
- **IT-057**: planInspection and onboarding completion for FIXED_ORIGIN without an approved baseline return the same Portuguese baseline-required userError and persist nothing (US-010.AC-4).
- **IT-058**: createSchedule for FIXED_ORIGIN without an approved baseline is refused; a schedule created earlier that fails at materialization produces a visible attention item and no inspection (US-010.AC-4).
- **IT-059**: reissueReport after a corrected item creates version N+1; reissueReport with unchanged canonical facts returns the existing version and creates none (US-009).
- **IT-060**: reissueReport with a stale expectedVersion returns a conflict userError; a caller without staff permission is refused; version N stays unchanged (US-009.EC-3, US-009.EC-4).

## End-to-End Tests

- **E2E-001**: Admin publishes a v3 kitchen model → manager chooses furnished profile and removes one component → Capture shows the confirmed list.
- **E2E-002**: Participant records one kitchen photo, leaves floor unobserved, enters keys, submits incomplete → Dashboard inspection detail shows 1 gap and the key declaration, read from the capture coverage contract (task 2), not from the report.
- **E2E-003**: A terminal analysis failure occurs after submission → manager opens the automatically published report marked “Falha na análise”, with no “sem alertas” claim.
- **E2E-004**: Manager opens a catalog with sound cabinets and no findings → Dashboard and internal PDF show the cabinets, F01, limitation and unreviewed label.
- **E2E-005**: Customer opens authorized report version → HTML and PDF show the same permitted items/gaps; revoked access cannot fetch private media.
- **E2E-006**: Manager corrects an item after issue → version 2 appears while the version 1 PDF and verification result remain available unchanged.
- **E2E-007**: Manager attempts move-out with only media origin → creation is blocked; after catalog approval, new move-out pins that baseline.
- **E2E-008**: Manager compares entry and exit photos → filters show changed/improved/not-observed/no-basis counts and paired evidence; customer PDF omits blame and internal-only data.
- **E2E-009**: Capture on 390 px mobile shows room gap and recapture help without horizontal scrolling; retry after reconnect does not duplicate the saved answer.
- **E2E-010**: A short report with one photo and a long report with many photos render readable captions and no QR-only page.

### API failure-shape cases

| Public operation | Failure cases |
| --- | --- |
| `publishTemplateVersion` | IT-028 |
| `createInspection` / `planInspection` / scheduled and onboarding creation | IT-029, IT-045, IT-046, IT-047, IT-048, IT-057, IT-058 |
| `confirmInspectionScope` | IT-030, IT-031 |
| `externalCapture` | IT-032 |
| `recordComponentAnswer` | IT-033, IT-034 |
| `upsertHandoverRecord` | IT-035, IT-036 |
| `submitCapture` | IT-037, IT-038 |
| `INVENTORY_CATALOG` / `activateBaselineVersion` | IT-039, IT-040, IT-052 |
| `customerReport` / PDF job | IT-042, IT-043 |
| `reissueReport` | IT-060 |
| `verifyReport` | IT-044 |
| `INVENTORY_COMPARE` | IT-049, IT-050, IT-051 |


## Failure and journey coverage notes

- Every story and every recorded edge case has its own matrix row. An edge case maps to one targeted unit case; integration and E2E cases cover the public journey and cross-component boundaries.
- API success and documented failure shapes are exercised by IT-001–IT-054. GraphQL error assertions use `userErrors` with safe Portuguese text, not raw provider/database errors.
- Setup tests for new slices must also cover missing dependencies and dependency failure, even where the corresponding behavior is exercised by an existing integration ID.
- The corpus evaluation is a release gate for prompt quality, not a replacement for these deterministic cases.


## Acceptance-criteria trace

Every acceptance criterion that has no dedicated case in the matrix above is traced here. Acceptance criteria not listed are covered by the matrix rows.

| Acceptance criterion | Case(s) |
| --- | --- |
| US-001.AC-2 | UT-107 |
| US-001.AC-3 | IT-008, IT-039 |
| US-002.AC-2 | UT-108 |
| US-002.AC-3 | IT-004, E2E-001 |
| US-003.AC-2 | UT-109, UT-095 |
| US-003.AC-3 | IT-007, IT-055, E2E-002 |
| US-004.AC-2 | UT-098, UT-110 |
| US-004.AC-3 | IT-056 |
| US-005.AC-2 | UT-111 |
| US-005.AC-3 | UT-112 |
| US-005.AC-4 | UT-113 |
| US-006.AC-2 | IT-010 |
| US-006.AC-3 | UT-114 |
| US-007.AC-2 | UT-115 |
| US-007.AC-4 | IT-056 |
| US-008.AC-3 | UT-055, UT-083 |
| US-009.AC-2 | UT-116 |
| US-009.AC-3 | IT-016, IT-044 |
| US-010.AC-2 | IT-048, E2E-007 |
| US-010.AC-3 | IT-019 |
| US-010.AC-4 | IT-018, IT-057, IT-058 |
| US-011.AC-2 | UT-073, IT-050 |
| US-011.AC-3 | IT-021, IT-051 |
| US-011.AC-4 | UT-117 |
| US-012.AC-2 | IT-022, UT-104 |
| US-012.AC-3 | UT-118 |
| US-013.AC-2 | UT-063, UT-091 |
| US-013.AC-3 | UT-119 |
