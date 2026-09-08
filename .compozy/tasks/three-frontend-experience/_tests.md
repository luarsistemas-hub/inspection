# Test Specification: Three Frontend Experience

Canonical test contract for the separation into Admin, Dashboard, and Capture. Companion to `_techspec.md`.
Derived from `_user_stories.md` and `_techspec.md`.

## Strategy

- Frameworks: Go table-driven tests with migrated PostgreSQL integration; Vitest/jsdom/fake-indexeddb per app; GraphQL contract tests; Playwright plus axe.
- Execution: verify Go first, then independent codegen, lint, unit, build, and E2E commands for Admin, Dashboard, and Capture, followed by cross-product journeys.
- Fixtures: every worker owns a tenant, identities, grants, resources, report state, media flags, invitation, and outbox namespace; fakes exist only at I/O boundaries.
- Conventions: mutation tests assert state, audit, outbox count, clientMutationId, and userErrors; denial tests assert protected fields are absent; cursors order by timestamp plus ID.

## Coverage Matrix

| Source | Behavior | Unit | Integration | E2E |
|---|---|---|---|---|
| US-001 | Enter Admin and move safely to Dashboard | — | — | E2E-001–E2E-004 |
| US-001.EC-1 | Invalid input: A malformed return destination is supplied → it is ignored and the user lands on a safe product home. | — | IT-001 | — |
| US-001.EC-2 | Empty / missing: The user has an identity but no product entitlement → no tenant data appears and access-request guidance is shown. | — | IT-002 | — |
| US-001.EC-3 | Limits: The user belongs to many tenants → tenant choices remain searchable and do not grant implicit cross-tenant access. | — | IT-003 | — |
| US-001.EC-4 | Permissions: A Dashboard-only customer opens Admin → Admin reveals no configuration data and returns a clear denial. | — | IT-004 | — |
| US-001.EC-5 | Concurrency: An entitlement is revoked while two product tabs are open → both products deny the next protected interaction. | — | IT-005 | — |
| US-001.EC-6 | Interruption: Sign-in is interrupted during a product transition → the user can restart without landing in the wrong product. | — | IT-006 | — |
| US-001.EC-7 | Repetition: The transition link is opened repeatedly → it produces one stable destination without duplicate side effects. | — | IT-007 | — |
| US-001.EC-8 | Ordering: A deep link is opened before authentication → sign-in returns the user only to that authorized context. | — | IT-008 | — |
| US-001.EC-9 | State transitions: A tenant becomes inactive during a session → both products stop exposing tenant content and explain the state. | — | IT-009 | — |
| US-001.EC-10 | Scale: The identity has many scoped memberships → product entry remains understandable and only current effective memberships are displayed. | — | IT-010 | — |
| US-002 | Configure tenant and business units | — | — | E2E-005–E2E-008 |
| US-002.EC-1 | Invalid input: Invalid timezone, language, code, or name → the affected field is rejected with corrective guidance. | — | IT-011 | — |
| US-002.EC-2 | Empty / missing: A required tenant identity or initial business unit is missing → activation or save is blocked with the missing requirement identified. | — | IT-012 | — |
| US-002.EC-3 | Limits: Names, codes, or unit counts exceed supported limits → Admin prevents the excess and states the applicable limit. | — | IT-013 | — |
| US-002.EC-4 | Permissions: A non-administrator reaches a configuration link → no configuration is disclosed. | — | IT-014 | — |
| US-002.EC-5 | Concurrency: Two administrators edit the same setting → the stale save is rejected and current values can be reloaded. | — | IT-015 | — |
| US-002.EC-6 | Interruption: Navigation or connection loss occurs with unsaved changes → Admin warns before discarding recoverable input. | — | IT-016 | — |
| US-002.EC-7 | Repetition: The same successful save is retried → the effective configuration remains singular and unchanged. | — | IT-017 | — |
| US-002.EC-8 | Ordering: A dependent default is selected before its prerequisite exists → Admin blocks the selection and explains the prerequisite. | — | IT-018 | — |
| US-002.EC-9 | State transitions: An archived unit is selected for new work → it remains historical but is unavailable for new assignments. | — | IT-019 | — |
| US-002.EC-10 | Scale: A tenant has many units → search, filtering, and pagination preserve the administrator's place and scope. | — | IT-020 | — |
| US-003 | Invite users and grant hierarchical access | — | — | E2E-009–E2E-013 |
| US-003.EC-1 | Invalid input: Invalid recipient, role, tenant, or resource → the invitation or grant is rejected without partial access. | — | IT-021 | — |
| US-003.EC-2 | Empty / missing: No scope is selected → the invitation cannot create a data-bearing Dashboard membership. | — | IT-022 | — |
| US-003.EC-3 | Limits: Invitation or grant volume exceeds a tenant limit → the excess is rejected with actionable guidance and existing grants remain intact. | — | IT-023 | — |
| US-003.EC-4 | Permissions: An administrator tries to grant beyond their own tenant → the target is undiscoverable and the action is denied. | — | IT-024 | — |
| US-003.EC-5 | Concurrency: Access is granted and revoked concurrently → the final effective state is explicit and never broader than the latest authorized decision. | — | IT-025 | — |
| US-003.EC-6 | Interruption: Invitation delivery fails → the pending invitation remains visible and can be resent without creating duplicate memberships. | — | IT-026 | — |
| US-003.EC-7 | Repetition: The same invitation is accepted twice → one membership exists and the second attempt reports the existing result. | — | IT-027 | — |
| US-003.EC-8 | Ordering: A grant targets a resource before the membership is accepted → the scope is preserved but no data is available before activation. | — | IT-028 | — |
| US-003.EC-9 | State transitions: A granted resource is archived or moved → historical access follows recorded authorization, while any changed effective access is explained before confirmation. | — | IT-029 | — |
| US-003.EC-10 | Scale: A user has many direct and inherited grants → Admin provides searchable effective-access inspection without flattening tenant boundaries. | — | IT-030 | — |
| US-004 | Manage participants, catalogs, templates, and assets | — | — | E2E-014–E2E-017 |
| US-004.EC-1 | Invalid input: Invalid contact, catalog definition, template reference, asset data, or location → publication or save is rejected with field-level guidance. | — | IT-031 | — |
| US-004.EC-2 | Empty / missing: Required participant, template, or asset context is absent → the resource cannot become active. | — | IT-032 | — |
| US-004.EC-3 | Limits: Catalog, template, asset, participant, or channel limits are reached → the user sees the exact blocked operation and no partial record. | — | IT-033 | — |
| US-004.EC-4 | Permissions: A user without Admin entitlement follows a catalog link → no administrative data or mutation is exposed. | — | IT-034 | — |
| US-004.EC-5 | Concurrency: Two edits target the same mutable resource → stale input is rejected while immutable published versions remain unchanged. | — | IT-035 | — |
| US-004.EC-6 | Interruption: Publication or asset save is interrupted → Admin shows whether nothing changed or a complete version was created. | — | IT-036 | — |
| US-004.EC-7 | Repetition: A publication request is retried → it resolves to one version rather than duplicating effective configuration. | — | IT-037 | — |
| US-004.EC-8 | Ordering: Activation is attempted before validation and publication → Admin blocks it and identifies missing steps. | — | IT-038 | — |
| US-004.EC-9 | State transitions: An inactive participant, channel, template, or asset is selected for new work → the selection is unavailable but history remains visible. | — | IT-039 | — |
| US-004.EC-10 | Scale: Catalogs and assets grow to large volumes → search, filters, and pagination remain scoped and stable. | — | IT-040 | — |
| US-005 | Configure publication, notification, retention, and audit governance | — | — | E2E-018–E2E-022 |
| US-005.EC-1 | Invalid input: Unsupported policy or retention values → save is blocked with the accepted range or choice. | — | IT-041 | — |
| US-005.EC-2 | Empty / missing: Publication policy is absent → manual publication remains the effective default. | — | IT-042 | — |
| US-005.EC-3 | Limits: Notification recipients or audit results exceed one page → pagination preserves ordering and scope. | — | IT-043 | — |
| US-005.EC-4 | Permissions: A non-administrator attempts policy changes → values remain visible only where permitted and no change occurs. | — | IT-044 | — |
| US-005.EC-5 | Concurrency: Two administrators change one policy → stale confirmation is rejected and the effective policy is shown. | — | IT-045 | — |
| US-005.EC-6 | Interruption: A policy save loses connectivity → Admin does not claim success until the effective value is confirmed. | — | IT-046 | — |
| US-005.EC-7 | Repetition: The same policy update or notification event repeats → configuration stays singular and customers do not receive duplicate notices for one event. | — | IT-047 | — |
| US-005.EC-8 | Ordering: Automatic publication is enabled after reports are already final → existing reports remain unchanged unless explicitly published; future finalizations follow the new policy. | — | IT-048 | — |
| US-005.EC-9 | State transitions: Automatic publication is disabled while a report finalizes → the policy effective at finalization determines visibility and is auditable. | — | IT-049 | — |
| US-005.EC-10 | Scale: Audit and notification history becomes large → filters and stable chronological ordering remain usable. | — | IT-050 | — |
| US-006 | See a role-adaptive operational home | — | — | E2E-023–E2E-026 |
| US-006.EC-1 | Invalid input: An invalid filter or date range is supplied → it is rejected or safely reset without broadening scope. | — | IT-051 | — |
| US-006.EC-2 | Empty / missing: No inspections or projects are visible → a role-specific empty state appears instead of zero-value noise. | — | IT-052 | — |
| US-006.EC-3 | Limits: Result counts exceed one page → stable pagination or progressive loading preserves priority order. | — | IT-053 | — |
| US-006.EC-4 | Permissions: A user deep-links to an out-of-scope card → existence and data remain hidden. | — | IT-054 | — |
| US-006.EC-5 | Concurrency: Classification changes while the home is open → refresh identifies the change without applying an action to stale state. | — | IT-055 | — |
| US-006.EC-6 | Interruption: Loading fails partway → already displayed data is marked stale and recovery is explicit. | — | IT-056 | — |
| US-006.EC-7 | Repetition: Refresh or back navigation repeats the same query → filters and selected context remain stable. | — | IT-057 | — |
| US-006.EC-8 | Ordering: Lower-priority work has an earlier timestamp → configured risk ordering still leads, with time used consistently within a priority. | — | IT-058 | — |
| US-006.EC-9 | State transitions: A visible inspection becomes canceled or invalidated → its card updates and leaves valid outcome totals as required. | — | IT-059 | — |
| US-006.EC-10 | Scale: A manager sees 100 times typical portfolio volume → summaries, filters, and drill-down remain usable without cross-scope leakage. | — | IT-060 | — |
| US-007 | Perform permitted inspection and project actions | — | — | E2E-027–E2E-030 |
| US-007.EC-1 | Invalid input: Invalid dates, recurrence, transition, target, or reason → no state changes and the correction is explained. | — | IT-061 | — |
| US-007.EC-2 | Empty / missing: A required asset, participant, template, stage, or reason is missing → submission remains blocked. | — | IT-062 | — |
| US-007.EC-3 | Limits: A bulk or recurring operation exceeds supported bounds → excess work is rejected with item-level outcomes. | — | IT-063 | — |
| US-007.EC-4 | Permissions: A customer, viewer, or out-of-scope internal user invokes an operation directly → it is denied without disclosing hidden state. | — | IT-064 | — |
| US-007.EC-5 | Concurrency: Another actor changes the resource first → the stale action is rejected and current state is offered. | — | IT-065 | — |
| US-007.EC-6 | Interruption: The connection drops after confirmation → Dashboard reconciles whether the action succeeded before offering retry. | — | IT-066 | — |
| US-007.EC-7 | Repetition: The same action is submitted twice → one business outcome occurs and the prior result is returned. | — | IT-067 | — |
| US-007.EC-8 | Ordering: A later lifecycle step is attempted before its prerequisite → the action is unavailable and the required prior state is named. | — | IT-068 | — |
| US-007.EC-9 | State transitions: An operation targets a closed, archived, canceled, or invalidated resource → only transitions explicitly allowed by policy remain available. | — | IT-069 | — |
| US-007.EC-10 | Scale: Many operations are visible across units → filters and grouped outcomes remain attributable to each resource. | — | IT-070 | — |
| US-008 | Triage findings, reports, and recapture needs | — | — | E2E-031–E2E-035 |
| US-008.EC-1 | Invalid input: Unknown evidence, blank recapture reason, or nonfinal report publication → the action is rejected with a specific explanation. | — | IT-071 | — |
| US-008.EC-2 | Empty / missing: No finding or comparison is available → the report explains absence instead of implying a normal result. | — | IT-072 | — |
| US-008.EC-3 | Limits: Evidence or report history is large → navigation remains grouped by requirement, stage, and version. | — | IT-073 | — |
| US-008.EC-4 | Permissions: A reviewer outside scope opens a comparison or download → resource existence and signed access remain hidden. | — | IT-074 | — |
| US-008.EC-5 | Concurrency: Two reviewers publish or request the same correction → one effective outcome exists and both see current state. | — | IT-075 | — |
| US-008.EC-6 | Interruption: Report or media loading fails → surrounding metadata remains safe and unavailable content is clearly marked. | — | IT-076 | — |
| US-008.EC-7 | Repetition: Publication, download authorization, or recapture request is retried → no duplicate report, access grant, or correction responsibility is created. | — | IT-077 | — |
| US-008.EC-8 | Ordering: Publication is attempted before finalization → Dashboard preserves internal state and explains the prerequisite. | — | IT-078 | — |
| US-008.EC-9 | State transitions: An inspection is invalidated after publication → history remains visible to authorized users and current status is unmistakable. | — | IT-079 | — |
| US-008.EC-10 | Scale: Hundreds of findings or versions exist → search and structured grouping prevent loss of evidence lineage. | — | IT-080 | — |
| US-009 | Review authorized data without mutation controls | — | — | E2E-036–E2E-039 |
| US-009.EC-1 | Invalid input: A malformed resource identifier or filter is used → no unrelated resource is shown. | — | IT-081 | — |
| US-009.EC-2 | Empty / missing: Scope contains no current reports → Dashboard shows an explanatory empty state. | — | IT-082 | — |
| US-009.EC-3 | Limits: Read results exceed one page → paging does not alter permissions or ordering. | — | IT-083 | — |
| US-009.EC-4 | Permissions: A viewer attempts a hidden mutation through a direct request → it is denied and no state changes. | — | IT-084 | — |
| US-009.EC-5 | Concurrency: Access is revoked while a report is open → the next read or download is denied immediately. | — | IT-085 | — |
| US-009.EC-6 | Interruption: Download is interrupted → retry creates fresh authorized access rather than exposing a reusable URL. | — | IT-086 | — |
| US-009.EC-7 | Repetition: The same report is opened repeatedly → immutable content remains identical for that version. | — | IT-087 | — |
| US-009.EC-8 | Ordering: A deep link arrives before tenant selection → only an authorized tenant context can resolve it. | — | IT-088 | — |
| US-009.EC-9 | State transitions: A report is superseded → the current version leads while the viewed version stays labeled historical. | — | IT-089 | — |
| US-009.EC-10 | Scale: A viewer has access to many units → filters never expand beyond effective scope. | — | IT-090 | — |
| US-010 | Follow shared assets and projects over time | — | — | E2E-040–E2E-043 |
| US-010.EC-1 | Invalid input: An invalid timeline filter or identifier is supplied → it is rejected without revealing resource existence. | — | IT-091 | — |
| US-010.EC-2 | Empty / missing: A newly shared asset has no inspections → the customer sees an understandable first-use state. | — | IT-092 | — |
| US-010.EC-3 | Limits: Timeline history is long → chronological paging preserves version and stage continuity. | — | IT-093 | — |
| US-010.EC-4 | Permissions: A customer follows a link to an unshared sibling asset → no tenant or resource detail is disclosed. | — | IT-094 | — |
| US-010.EC-5 | Concurrency: A grant is revoked while the timeline is open → content disappears on the next interaction and access guidance replaces it. | — | IT-095 | — |
| US-010.EC-6 | Interruption: Timeline loading is interrupted → partial information is marked incomplete and can be retried safely. | — | IT-096 | — |
| US-010.EC-7 | Repetition: The customer revisits a timeline → selected context and published immutable content remain consistent. | — | IT-097 | — |
| US-010.EC-8 | Ordering: Events arrive or are processed late → the displayed order follows business occurrence time and explains pending state where needed. | — | IT-098 | — |
| US-010.EC-9 | State transitions: A project closes or reopens → both transitions remain in history and current status is prominent. | — | IT-099 | — |
| US-010.EC-10 | Scale: A customer owns many assets or projects → search and grouping keep the asset/project entry model usable. | — | IT-100 | — |
| US-011 | Inspect published reports and evidence in simple or advanced mode | — | — | E2E-044–E2E-048 |
| US-011.EC-1 | Invalid input: A modified evidence or report reference is requested → access is denied without disclosing hidden media. | — | IT-101 | — |
| US-011.EC-2 | Empty / missing: A published report has no displayable visual evidence → the explanation and safe metadata remain meaningful. | — | IT-102 | — |
| US-011.EC-3 | Limits: Advanced mode contains many media items → grouped progressive navigation avoids loading or presenting an unbounded wall of content. | — | IT-103 | — |
| US-011.EC-4 | Permissions: A customer has asset access but not the targeted report or inspection → content remains unavailable until explicitly inherited or granted. | — | IT-104 | — |
| US-011.EC-5 | Concurrency: Publication or access is revoked while media is opening → the media request is denied and stale access is not reusable. | — | IT-105 | — |
| US-011.EC-6 | Interruption: A permitted image fails to load → no broken state implies deletion or a different classification; retry guidance is shown. | — | IT-106 | — |
| US-011.EC-7 | Repetition: A published immutable version is downloaded or reopened repeatedly → content and hashes remain consistent for that version. | — | IT-107 | — |
| US-011.EC-8 | Ordering: Advanced mode is opened before the report is published → no unpublished result or evidence becomes visible. | — | IT-108 | — |
| US-011.EC-9 | State transitions: Evidence is later replaced through recapture → prior and replacement records remain distinct and the current lineage is identified. | — | IT-109 | — |
| US-011.EC-10 | Scale: Hundreds of evidence records exist → mode switching, grouping, and pagination preserve context and accessibility. | — | IT-110 | — |
| US-012 | Receive safe progress and publication notifications | — | — | E2E-049–E2E-052 |
| US-012.EC-1 | Invalid input: An invalid or unverified delivery channel is selected → it cannot receive customer notifications. | — | IT-111 | — |
| US-012.EC-2 | Empty / missing: No channel is selected → in-product notification remains available and Admin shows the delivery limitation. | — | IT-112 | — |
| US-012.EC-3 | Limits: Events occur in a burst → notices remain attributable and avoid unbounded duplication. | — | IT-113 | — |
| US-012.EC-4 | Permissions: Access is revoked before delivery or link opening → the message reveals no protected detail and Dashboard denies the destination. | — | IT-114 | — |
| US-012.EC-5 | Concurrency: Publication and revocation happen together → no usable content is exposed after revocation. | — | IT-115 | — |
| US-012.EC-6 | Interruption: A provider fails → the in-product state remains accurate and delivery status is visible to authorized administrators. | — | IT-116 | — |
| US-012.EC-7 | Repetition: An event is replayed → the customer does not receive duplicate notices for the same business change. | — | IT-117 | — |
| US-012.EC-8 | Ordering: Notifications arrive out of order → each opens current authorized state and does not present stale status as current. | — | IT-118 | — |
| US-012.EC-9 | State transitions: A report is unpublished or invalidated after notice → the destination reflects current visibility and status. | — | IT-119 | — |
| US-012.EC-10 | Scale: A customer follows many assets → notifications remain grouped, filterable, and scoped to permitted resources. | — | IT-120 | — |
| US-013 | Enter one scoped responsibility with link and OTP | — | — | E2E-053–E2E-056 |
| US-013.EC-1 | Invalid input: Malformed link or OTP → generic failure prevents secret or resource inference. | — | IT-121 | — |
| US-013.EC-2 | Empty / missing: Link, OTP, or required consent is absent → Capture cannot proceed. | — | IT-122 | — |
| US-013.EC-3 | Limits: OTP attempts or resend limits are reached → further attempts pause according to the stated recovery window. | — | IT-123 | — |
| US-013.EC-4 | Permissions: A valid session requests another responsibility → the target remains undiscoverable. | — | IT-124 | — |
| US-013.EC-5 | Concurrency: The same invitation is verified on multiple devices → session rules remain explicit and never broaden responsibility access. | — | IT-125 | — |
| US-013.EC-6 | Interruption: Verification is interrupted → the participant can restart only while the invitation remains valid. | — | IT-126 | — |
| US-013.EC-7 | Repetition: A successful link or OTP action is repeated → it does not create duplicate responsibilities or extend authorization unexpectedly. | — | IT-127 | — |
| US-013.EC-8 | Ordering: Capture APIs are called before link exchange, OTP, or consent → each premature step is denied. | — | IT-128 | — |
| US-013.EC-9 | State transitions: The inspection is canceled, invalidated, submitted, or completed during access → the session stops and explains that access is no longer available. | — | IT-129 | — |
| US-013.EC-10 | Scale: Many participants open independent links concurrently → each sees only their own responsibility and receives timely feedback. | — | IT-130 | — |
| US-014 | Register guided origin evidence | — | — | E2E-057–E2E-060 |
| US-014.EC-1 | Invalid input: Unsupported media, excessive size, invalid metadata, or hostile text → the item is rejected with safe corrective guidance. | — | IT-131 | — |
| US-014.EC-2 | Empty / missing: Required description, category, evidence, GPS, or impossibility reason is absent → affected progress remains incomplete. | — | IT-132 | — |
| US-014.EC-3 | Limits: Photo or active-evidence limits are reached → additional items are blocked without losing accepted work. | — | IT-133 | — |
| US-014.EC-4 | Permissions: The origin session attempts inspection or tenant data → access is denied. | — | IT-134 | — |
| US-014.EC-5 | Concurrency: The same draft is uploaded from two active views → one evidence record and clear current progress result. | — | IT-135 | — |
| US-014.EC-6 | Interruption: Camera, location, network, or upload fails → accepted local progress remains recoverable and next steps are explicit. | — | IT-136 | — |
| US-014.EC-7 | Repetition: Upload completion or metadata save repeats → one logical evidence item remains. | — | IT-137 | — |
| US-014.EC-8 | Ordering: Submission occurs before verification → finalization is blocked and pending items are identified. | — | IT-138 | — |
| US-014.EC-9 | State transitions: Origin responsibility is revoked during capture → no further submission is accepted and local state is safely explained. | — | IT-139 | — |
| US-014.EC-10 | Scale: A valid origin contains many requirements and photos → progress remains navigable by section and requirement. | — | IT-140 | — |
| US-015 | Complete guided inspection evidence | — | — | E2E-061–E2E-064 |
| US-015.EC-1 | Invalid input: Unsupported file, invalid answer, inaccurate required GPS, or malformed description → the exact item remains incomplete with recovery guidance. | — | IT-141 | — |
| US-015.EC-2 | Empty / missing: No applicable requirements exist → Capture explains the condition and does not create an empty success silently. | — | IT-142 | — |
| US-015.EC-3 | Limits: Media, answer, or description bounds are reached → additional input is prevented without corrupting saved progress. | — | IT-143 | — |
| US-015.EC-4 | Permissions: The participant tries to view findings, reports, or another inspection → no internal or unrelated content is exposed. | — | IT-144 | — |
| US-015.EC-5 | Concurrency: Two devices update one requirement → conflicting progress is reconciled without silently overwriting accepted evidence. | — | IT-145 | — |
| US-015.EC-6 | Interruption: Permission denial, browser refresh, app backgrounding, or connection loss occurs → recoverable progress and next action are explicit. | — | IT-146 | — |
| US-015.EC-7 | Repetition: The same answer or media action repeats → one logical outcome is retained. | — | IT-147 | — |
| US-015.EC-8 | Ordering: A later section is reached before a blocking prerequisite → Capture guides the participant back to the unmet requirement. | — | IT-148 | — |
| US-015.EC-9 | State transitions: Deadline expires or responsibility closes during capture → further finalization is blocked and current server state is explained. | — | IT-149 | — |
| US-015.EC-10 | Scale: The template contains many requirements → section navigation and progress prevent disorientation on mobile. | — | IT-150 | — |
| US-016 | Replace only requested evidence | — | — | E2E-065–E2E-068 |
| US-016.EC-1 | Invalid input: Replacement targets an unrequested item → the action is rejected without broadening scope. | — | IT-151 | — |
| US-016.EC-2 | Empty / missing: A recapture request has no valid active requirement → Capture shows unavailable access and accepts no evidence. | — | IT-152 | — |
| US-016.EC-3 | Limits: Replacement evidence exceeds policy limits → only excess items are blocked and prior lineage stays intact. | — | IT-153 | — |
| US-016.EC-4 | Permissions: An original or other recapture session accesses this request → authorization is denied. | — | IT-154 | — |
| US-016.EC-5 | Concurrency: System and reviewer reasons or two replacement attempts overlap → one active lineage is preserved with all applicable reasons. | — | IT-155 | — |
| US-016.EC-6 | Interruption: Replacement upload is interrupted → resumable progress remains tied only to the recapture responsibility. | — | IT-156 | — |
| US-016.EC-7 | Repetition: Replacement submission is replayed → no duplicate lineage or analysis work becomes visible. | — | IT-157 | — |
| US-016.EC-8 | Ordering: Submission is attempted before all selected items are resolved → blocking requirements and allowed incomplete behavior are explicit. | — | IT-158 | — |
| US-016.EC-9 | State transitions: The recapture expires during use → no late submission is accepted and the participant sees a final unavailable state. | — | IT-159 | — |
| US-016.EC-10 | Scale: Many requested corrections exist → grouping and progress keep each reason associated with the correct evidence. | — | IT-160 | — |
| US-017 | Resume interrupted capture and uploads | — | — | E2E-069–E2E-072 |
| US-017.EC-1 | Invalid input: A local draft is corrupt or no longer matches accepted policy → it is isolated and recovery does not affect valid drafts. | — | IT-161 | — |
| US-017.EC-2 | Empty / missing: No recoverable draft exists → Capture starts from confirmed server progress. | — | IT-162 | — |
| US-017.EC-3 | Limits: Device storage is insufficient → the participant is warned before relying on local persistence and receives cleanup guidance. | — | IT-163 | — |
| US-017.EC-4 | Permissions: The responsibility expires before resume → local data cannot restore server authorization or expose protected context. | — | IT-164 | — |
| US-017.EC-5 | Concurrency: Resume runs in multiple tabs → completed parts are reconciled and not uploaded as duplicate evidence. | — | IT-165 | — |
| US-017.EC-6 | Interruption: Connectivity repeatedly drops → each accepted part remains recognizable and the participant sees current pending state. | — | IT-166 | — |
| US-017.EC-7 | Repetition: Resume is requested after success → Capture confirms completed state without restarting the upload. | — | IT-167 | — |
| US-017.EC-8 | Ordering: Final submission is attempted before pending uploads resume → the action is blocked with the pending count. | — | IT-168 | — |
| US-017.EC-9 | State transitions: Server-side evidence becomes blocked during resume → it cannot become ready and the reason is presented safely. | — | IT-169 | — |
| US-017.EC-10 | Scale: Many pending media parts exist → progress remains responsive, attributable, and resumable per item. | — | IT-170 | — |
| US-018 | Submit immutable evidence and receive confirmation | — | — | E2E-073–E2E-076 |
| US-018.EC-1 | Invalid input: Submission contains unresolved invalid evidence or answers → it is rejected with the unresolved items identified. | — | IT-171 | — |
| US-018.EC-2 | Empty / missing: Required evidence or reasons are missing → complete submission is blocked and only permitted incomplete behavior is offered. | — | IT-172 | — |
| US-018.EC-3 | Limits: Submission reaches media or requirement limits → accepted items remain stable and only policy-compliant finalization is allowed. | — | IT-173 | — |
| US-018.EC-4 | Permissions: A closed or unrelated session submits → the request is denied without reopening responsibility. | — | IT-174 | — |
| US-018.EC-5 | Concurrency: Two final submissions race → one immutable result exists and both attempts resolve to that final state. | — | IT-175 | — |
| US-018.EC-6 | Interruption: The connection drops after the final action → Capture checks authoritative state before inviting a retry. | — | IT-176 | — |
| US-018.EC-7 | Repetition: Final submission is replayed → no evidence, origin version, recapture response, or downstream work is duplicated. | — | IT-177 | — |
| US-018.EC-8 | Ordering: Submission is attempted before media verification → the participant sees pending verification and cannot complete. | — | IT-178 | — |
| US-018.EC-9 | State transitions: Cancellation, invalidation, expiry, or prior completion occurs before final acceptance → no new submission is created. | — | IT-179 | — |
| US-018.EC-10 | Scale: A maximum-size valid submission completes → final status remains attributable to every requirement and item. | — | IT-180 | — |

### Component, Interface, and API Coverage

| Component or interface | Unit | Integration | E2E |
|---|---|---|---|
| `ProductAuthorizer` | UT-001–UT-008 | IT-181, IT-205, IT-221–IT-224 | E2E-001–E2E-004 |
| `ScopeResolver` | UT-009–UT-016 | IT-182–IT-185, IT-229 | E2E-010–E2E-014 |
| Customer invitation and `IdentityInvitationPort` | UT-017–UT-023 | IT-197–IT-200, IT-225 | E2E-009–E2E-013 |
| Publication policy and ledger | UT-024–UT-032 | IT-186, IT-206–IT-216 | E2E-018–E2E-022, E2E-032–E2E-035 |
| `CustomerEvidenceMapper` and `MediaURLIssuer` | UT-033–UT-040 | IT-190–IT-194, IT-220, IT-228 | E2E-044–E2E-048 |
| Recipient notifications | UT-041–UT-047 | IT-111–IT-112, IT-195–IT-196, IT-217–IT-218, IT-226–IT-227 | E2E-049–E2E-052 |
| Multi-audience OIDC and CORS | UT-048–UT-052 | IT-221–IT-224 | E2E-001–E2E-004 |
| Admin/Dashboard sessions and capability composition | UT-053–UT-058 | IT-181, IT-222–IT-223 | E2E-001–E2E-004, E2E-023–E2E-027 |
| Capture draft/session/upload modules | UT-059–UT-066 | IT-224 | E2E-053–E2E-076 |
| Private design-system package | UT-067–UT-070 | IT-219 | E2E-001, E2E-023, E2E-053 |
| Product GraphQL transports and route ownership | UT-071–UT-075 | IT-219–IT-224 | E2E-001–E2E-076 |
| New database models and RLS | UT-017–UT-047 | IT-197–IT-218, IT-229–IT-230 | E2E-009–E2E-052 |
| Existing outbox and provider adapters | UT-021–UT-023, UT-041–UT-043 | IT-199, IT-208–IT-216, IT-226–IT-227 | E2E-018–E2E-022, E2E-049–E2E-052 |

| GraphQL operation | Success | Failure and state coverage |
|---|---|---|
| `me` | IT-181 | IT-181, IT-222–IT-224 |
| `effectiveAccess` | IT-182 | IT-183 |
| `scopePreview` | IT-184 | IT-185 |
| `publicationPolicy` | IT-186 | IT-207 |
| `customerPortfolio` | IT-187 | IT-188 |
| `customerTimeline` | IT-189 | IT-189 |
| `customerReport` | IT-190 | IT-191, IT-194 |
| `customerEvidence` | IT-192 | IT-193–IT-194, IT-220, IT-228 |
| `myNotifications` | IT-195 | IT-196 |
| `inviteCustomerUser` | IT-197 | IT-198–IT-200 |
| modified `inviteInternalUser` | IT-201 | IT-202 |
| `assignMembershipAccess` | IT-203 | IT-204–IT-205 |
| `configurePublicationPolicy` | IT-206 | IT-207 |
| automatic finalization contract | IT-208 | IT-209 |
| `publishReport` | IT-210, IT-213 | IT-211–IT-212 |
| `invalidateReportPublication` | IT-214 | IT-215–IT-216 |
| `markNotificationRead` | IT-217 | IT-218 |
| `configureMyNotificationPreferences` | E2E-052 | IT-111–IT-112 |

## Unit Tests

- **UT-001** (happy): `ProductAuthorizer.Authorize` accepts `inspection-admin`, `ADMIN`, `TENANT_ADMIN`, and the requested tenant.
- **UT-002** (error): `ProductAuthorizer.Authorize` rejects `inspection-dashboard` when `Product=ADMIN` with `FORBIDDEN`.
- **UT-003** (error): `ProductAuthorizer.Authorize` rejects an active membership missing the requested entitlement with `FORBIDDEN`.
- **UT-004** (error): `ProductAuthorizer.Authorize` maps a cross-tenant target to `NOT_FOUND`.
- **UT-005** (error): `ProductAuthorizer.Authorize` rejects `Mutate=true` for `VIEWER` and `CUSTOMER_VIEWER`.
- **UT-006** (state): `ProductAuthorizer.Authorize` reloads a disabled membership and rejects before resource I/O.
- **UT-007** (error): `ProductAuthorizer.Authorize` wraps `MembershipStore` failure without database details.
- **UT-008** (boundary): `TENANT_ADMIN` bypasses resource grants only for tenant-scoped Admin operations.
- **UT-009** (happy): `ScopeResolver.Resolve` maps a `BUSINESS_UNIT` grant to descendant assets, projects, and inspections and returns that grant.
- **UT-010** (happy): `ScopeResolver.Resolve` maps an `ASSET` grant to its projects and inspections.
- **UT-011** (happy): `ScopeResolver.Resolve` maps a `PROJECT` grant only to that project's inspections.
- **UT-012** (boundary): `ScopeResolver.Resolve` maps an `INSPECTION` grant only to the exact inspection.
- **UT-013** (error): `ScopeResolver.Resolve` fails closed when any hierarchy row belongs to another tenant.
- **UT-014** (state): `ScopeResolver.Resolve` follows the current parent after a resource move and rejects the old hierarchy.
- **UT-015** (error): `ScopeResolver.Resolve` denies an archived target requested for new work.
- **UT-016** (error): `ScopeResolver.Resolve` propagates context cancellation from its repository.
- **UT-017** (happy): `invite_customer_user` accepts normalized email, `CUSTOMER_VIEWER`, `DASHBOARD`, and one valid scope.
- **UT-018** (error): `invite_customer_user` rejects empty scopes with `INVALID_INPUT` and creates no event.
- **UT-019** (error): `invite_customer_user` rejects `ADMIN` for `CUSTOMER_VIEWER`.
- **UT-020** (idempotency): a repeated customer invitation key returns the existing active invitation.
- **UT-021** (state): the invitation consumer moves `PENDING` to `PROVISIONED` after Keycloak returns a subject.
- **UT-022** (error): Keycloak 429/5xx leaves the invitation retryable and records no membership.
- **UT-023** (concurrency): a Keycloak create conflict resolves by verified email and binds one subject.
- **UT-024** (happy): `configure_publication_policy` stores `MANUAL` or `AUTOMATIC` and increments version.
- **UT-025** (boundary): missing publication policy resolves as synthetic `MANUAL`, version 0.
- **UT-026** (concurrency): stale `expectedVersion` returns `CONFLICT` without changing policy.
- **UT-027** (happy): report finalization creates one publication in its snapshot transaction for `AUTOMATIC`.
- **UT-028** (state): report finalization records a `MANUAL` policy version and creates no publication.
- **UT-029** (error): `publish_report` rejects a non-final snapshot with `INVALID_STATE`.
- **UT-030** (idempotency): repeated `publish_report.clientMutationId` reuses one result and event.
- **UT-031** (state): publishing a newer snapshot marks the former active publication `SUPERSEDED`.
- **UT-032** (state): invalidation requires a reason and permits only `PUBLISHED` to `INVALIDATED`.
- **UT-033** (happy): `CustomerEvidenceMapper` returns only approved current items in `SIMPLE` mode.
- **UT-034** (happy): `CustomerEvidenceMapper` adds current, replaced, and discarded lineage in `ADVANCED` mode.
- **UT-035** (error): sensitive mapping omits URL, thumbnail, object key, and visual payload.
- **UT-036** (error): `CustomerEvidenceMapper` rejects an unpublished snapshot.
- **UT-037** (state): superseded lineage keeps immutable content hashes and receives a historical label.
- **UT-038** (happy): `MediaURLIssuer` signs only after entitlement, scope, publication, and sensitivity checks.
- **UT-039** (error): `MediaURLIssuer` returns `NOT_FOUND` after publication or grant revocation.
- **UT-040** (error): storage signing failure returns no partial URL.
- **UT-041** (happy): recipient fan-out creates one safe record per currently entitled membership.
- **UT-042** (idempotency): replay of the same event/recipient/kind creates no second record.
- **UT-043** (state): fan-out excludes a membership revoked before processing.
- **UT-044** (ordering): notification cursors use `createdAt` plus ID for stable descending order.
- **UT-045** (happy): mark-read sets `readAt` for the current membership and decrements unread count.
- **UT-046** (idempotency): repeated mark-read preserves the first `readAt`.
- **UT-047** (error): another recipient's notification maps to `NOT_FOUND`.
- **UT-048** (happy): config parses three exact origins and the two exact OIDC audiences.
- **UT-049** (error): config rejects wildcard, malformed, duplicate, and non-HTTPS production origins.
- **UT-050** (error): OIDC rejects an audience outside the configured set.
- **UT-051** (happy): OIDC preserves verified audience in request metadata.
- **UT-052** (boundary): CORS permits credentials only for exact Capture origin and never reflects unknown origins.
- **UT-053** (happy): Admin PKCE validates state and restores one same-origin relative return path.
- **UT-054** (error): an absolute or cross-origin return path becomes the safe product home.
- **UT-055** (state): in-memory session clears token/capabilities after `FORBIDDEN` or inactive tenant.
- **UT-056** (happy): Dashboard capability map composes manager, employee, viewer, and customer navigation from `me`.
- **UT-057** (error): capability composition never emits mutation controls for `VIEWER` or `CUSTOMER_VIEWER`.
- **UT-058** (state): notification polling pauses while hidden, refreshes on focus, and preserves its cursor.
- **UT-059** (happy): Capture draft store restores a valid responsibility-scoped draft.
- **UT-060** (error): Capture quarantines corrupt or policy-version-mismatched draft data.
- **UT-061** (concurrency): multipart reconciliation uploads only server-missing parts when two tabs resume.
- **UT-062** (idempotency): repeated multipart completion keeps one media record.
- **UT-063** (state): server final/expired/revoked bootstrap removes actionable local state.
- **UT-064** (error): Capture sends CSRF only and never sends an OIDC bearer token.
- **UT-065** (boundary): storage warning appears before a draft exceeds the tested browser quota threshold.
- **UT-066** (ordering): Capture finalization blocks while media is unverified or uploading.
- **UT-067** (happy): design-system `Button` supports keyboard activation, visible focus, and accessible disabled state.
- **UT-068** (boundary): layout tokens avoid horizontal overflow at 320, 360, 768, and 1440 px.
- **UT-069** (state): motion primitives honor `prefers-reduced-motion`.
- **UT-070** (error): package exports contain no auth, GraphQL, routing, or domain module.
- **UT-071** (happy): Admin transport sends only in-memory `inspection-admin` token with `credentials=omit`.
- **UT-072** (happy): Dashboard transport sends only in-memory `inspection-dashboard` token with `credentials=omit`.
- **UT-073** (happy): Capture transport uses `credentials=include` and external CSRF.
- **UT-074** (error): all transports map GraphQL failure to one stable safe error shape.
- **UT-075** (boundary): route manifests contain no Admin route in Dashboard/Capture and no report route in Capture.

## Integration Tests

### User-story edge contracts

#### US-001: Enter Admin and move safely to Dashboard

- **IT-001** (US-001.EC-1, Invalid input): Through Admin/Dashboard PKCE, me, and product authorization, create the state where a malformed return destination is supplied; assert it is ignored and the user lands on a safe product home.
- **IT-002** (US-001.EC-2, Empty / missing): Through Admin/Dashboard PKCE, me, and product authorization, create the state where the user has an identity but no product entitlement; assert no tenant data appears and access-request guidance is shown.
- **IT-003** (US-001.EC-3, Limits): Through Admin/Dashboard PKCE, me, and product authorization, create the state where the user belongs to many tenants; assert tenant choices remain searchable and do not grant implicit cross-tenant access.
- **IT-004** (US-001.EC-4, Permissions): Through Admin/Dashboard PKCE, me, and product authorization, create the state where a dashboard-only customer opens admin; assert admin reveals no configuration data and returns a clear denial.
- **IT-005** (US-001.EC-5, Concurrency): Through Admin/Dashboard PKCE, me, and product authorization, create the state where an entitlement is revoked while two product tabs are open; assert both products deny the next protected interaction.
- **IT-006** (US-001.EC-6, Interruption): Through Admin/Dashboard PKCE, me, and product authorization, create the state where sign-in is interrupted during a product transition; assert the user can restart without landing in the wrong product.
- **IT-007** (US-001.EC-7, Repetition): Through Admin/Dashboard PKCE, me, and product authorization, create the state where the transition link is opened repeatedly; assert it produces one stable destination without duplicate side effects.
- **IT-008** (US-001.EC-8, Ordering): Through Admin/Dashboard PKCE, me, and product authorization, create the state where a deep link is opened before authentication; assert sign-in returns the user only to that authorized context.
- **IT-009** (US-001.EC-9, State transitions): Through Admin/Dashboard PKCE, me, and product authorization, create the state where a tenant becomes inactive during a session; assert both products stop exposing tenant content and explain the state.
- **IT-010** (US-001.EC-10, Scale): Through Admin/Dashboard PKCE, me, and product authorization, create the state where the identity has many scoped memberships; assert product entry remains understandable and only current effective memberships are displayed.

#### US-002: Configure tenant and business units

- **IT-011** (US-002.EC-1, Invalid input): Through Admin tenancy and business-unit GraphQL flows, create the state where invalid timezone, language, code, or name; assert the affected field is rejected with corrective guidance.
- **IT-012** (US-002.EC-2, Empty / missing): Through Admin tenancy and business-unit GraphQL flows, create the state where a required tenant identity or initial business unit is missing; assert activation or save is blocked with the missing requirement identified.
- **IT-013** (US-002.EC-3, Limits): Through Admin tenancy and business-unit GraphQL flows, create the state where names, codes, or unit counts exceed supported limits; assert admin prevents the excess and states the applicable limit.
- **IT-014** (US-002.EC-4, Permissions): Through Admin tenancy and business-unit GraphQL flows, create the state where a non-administrator reaches a configuration link; assert no configuration is disclosed.
- **IT-015** (US-002.EC-5, Concurrency): Through Admin tenancy and business-unit GraphQL flows, create the state where two administrators edit the same setting; assert the stale save is rejected and current values can be reloaded.
- **IT-016** (US-002.EC-6, Interruption): Through Admin tenancy and business-unit GraphQL flows, create the state where navigation or connection loss occurs with unsaved changes; assert admin warns before discarding recoverable input.
- **IT-017** (US-002.EC-7, Repetition): Through Admin tenancy and business-unit GraphQL flows, create the state where the same successful save is retried; assert the effective configuration remains singular and unchanged.
- **IT-018** (US-002.EC-8, Ordering): Through Admin tenancy and business-unit GraphQL flows, create the state where a dependent default is selected before its prerequisite exists; assert admin blocks the selection and explains the prerequisite.
- **IT-019** (US-002.EC-9, State transitions): Through Admin tenancy and business-unit GraphQL flows, create the state where an archived unit is selected for new work; assert it remains historical but is unavailable for new assignments.
- **IT-020** (US-002.EC-10, Scale): Through Admin tenancy and business-unit GraphQL flows, create the state where a tenant has many units; assert search, filtering, and pagination preserve the administrator's place and scope.

#### US-003: Invite users and grant hierarchical access

- **IT-021** (US-003.EC-1, Invalid input): Through Admin invitation, entitlement, and hierarchy flows, create the state where invalid recipient, role, tenant, or resource; assert the invitation or grant is rejected without partial access.
- **IT-022** (US-003.EC-2, Empty / missing): Through Admin invitation, entitlement, and hierarchy flows, create the state where no scope is selected; assert the invitation cannot create a data-bearing dashboard membership.
- **IT-023** (US-003.EC-3, Limits): Through Admin invitation, entitlement, and hierarchy flows, create the state where invitation or grant volume exceeds a tenant limit; assert the excess is rejected with actionable guidance and existing grants remain intact.
- **IT-024** (US-003.EC-4, Permissions): Through Admin invitation, entitlement, and hierarchy flows, create the state where an administrator tries to grant beyond their own tenant; assert the target is undiscoverable and the action is denied.
- **IT-025** (US-003.EC-5, Concurrency): Through Admin invitation, entitlement, and hierarchy flows, create the state where access is granted and revoked concurrently; assert the final effective state is explicit and never broader than the latest authorized decision.
- **IT-026** (US-003.EC-6, Interruption): Through Admin invitation, entitlement, and hierarchy flows, create the state where invitation delivery fails; assert the pending invitation remains visible and can be resent without creating duplicate memberships.
- **IT-027** (US-003.EC-7, Repetition): Through Admin invitation, entitlement, and hierarchy flows, create the state where the same invitation is accepted twice; assert one membership exists and the second attempt reports the existing result.
- **IT-028** (US-003.EC-8, Ordering): Through Admin invitation, entitlement, and hierarchy flows, create the state where a grant targets a resource before the membership is accepted; assert the scope is preserved but no data is available before activation.
- **IT-029** (US-003.EC-9, State transitions): Through Admin invitation, entitlement, and hierarchy flows, create the state where a granted resource is archived or moved; assert historical access follows recorded authorization, while any changed effective access is explained before confirmation.
- **IT-030** (US-003.EC-10, Scale): Through Admin invitation, entitlement, and hierarchy flows, create the state where a user has many direct and inherited grants; assert admin provides searchable effective-access inspection without flattening tenant boundaries.

#### US-004: Manage participants, catalogs, templates, and assets

- **IT-031** (US-004.EC-1, Invalid input): Through Admin participant, catalog, template, and asset flows, create the state where invalid contact, catalog definition, template reference, asset data, or location; assert publication or save is rejected with field-level guidance.
- **IT-032** (US-004.EC-2, Empty / missing): Through Admin participant, catalog, template, and asset flows, create the state where required participant, template, or asset context is absent; assert the resource cannot become active.
- **IT-033** (US-004.EC-3, Limits): Through Admin participant, catalog, template, and asset flows, create the state where catalog, template, asset, participant, or channel limits are reached; assert the user sees the exact blocked operation and no partial record.
- **IT-034** (US-004.EC-4, Permissions): Through Admin participant, catalog, template, and asset flows, create the state where a user without admin entitlement follows a catalog link; assert no administrative data or mutation is exposed.
- **IT-035** (US-004.EC-5, Concurrency): Through Admin participant, catalog, template, and asset flows, create the state where two edits target the same mutable resource; assert stale input is rejected while immutable published versions remain unchanged.
- **IT-036** (US-004.EC-6, Interruption): Through Admin participant, catalog, template, and asset flows, create the state where publication or asset save is interrupted; assert admin shows whether nothing changed or a complete version was created.
- **IT-037** (US-004.EC-7, Repetition): Through Admin participant, catalog, template, and asset flows, create the state where a publication request is retried; assert it resolves to one version rather than duplicating effective configuration.
- **IT-038** (US-004.EC-8, Ordering): Through Admin participant, catalog, template, and asset flows, create the state where activation is attempted before validation and publication; assert admin blocks it and identifies missing steps.
- **IT-039** (US-004.EC-9, State transitions): Through Admin participant, catalog, template, and asset flows, create the state where an inactive participant, channel, template, or asset is selected for new work; assert the selection is unavailable but history remains visible.
- **IT-040** (US-004.EC-10, Scale): Through Admin participant, catalog, template, and asset flows, create the state where catalogs and assets grow to large volumes; assert search, filters, and pagination remain scoped and stable.

#### US-005: Configure publication, notification, retention, and audit governance

- **IT-041** (US-005.EC-1, Invalid input): Through Admin governance, publication-policy, retention, and audit flows, create the state where unsupported policy or retention values; assert save is blocked with the accepted range or choice.
- **IT-042** (US-005.EC-2, Empty / missing): Through Admin governance, publication-policy, retention, and audit flows, create the state where publication policy is absent; assert manual publication remains the effective default.
- **IT-043** (US-005.EC-3, Limits): Through Admin governance, publication-policy, retention, and audit flows, create the state where notification recipients or audit results exceed one page; assert pagination preserves ordering and scope.
- **IT-044** (US-005.EC-4, Permissions): Through Admin governance, publication-policy, retention, and audit flows, create the state where a non-administrator attempts policy changes; assert values remain visible only where permitted and no change occurs.
- **IT-045** (US-005.EC-5, Concurrency): Through Admin governance, publication-policy, retention, and audit flows, create the state where two administrators change one policy; assert stale confirmation is rejected and the effective policy is shown.
- **IT-046** (US-005.EC-6, Interruption): Through Admin governance, publication-policy, retention, and audit flows, create the state where a policy save loses connectivity; assert admin does not claim success until the effective value is confirmed.
- **IT-047** (US-005.EC-7, Repetition): Through Admin governance, publication-policy, retention, and audit flows, create the state where the same policy update or notification event repeats; assert configuration stays singular and customers do not receive duplicate notices for one event.
- **IT-048** (US-005.EC-8, Ordering): Through Admin governance, publication-policy, retention, and audit flows, create the state where automatic publication is enabled after reports are already final; assert existing reports remain unchanged unless explicitly published; future finalizations follow the new policy.
- **IT-049** (US-005.EC-9, State transitions): Through Admin governance, publication-policy, retention, and audit flows, create the state where automatic publication is disabled while a report finalizes; assert the policy effective at finalization determines visibility and is auditable.
- **IT-050** (US-005.EC-10, Scale): Through Admin governance, publication-policy, retention, and audit flows, create the state where audit and notification history becomes large; assert filters and stable chronological ordering remain usable.

#### US-006: See a role-adaptive operational home

- **IT-051** (US-006.EC-1, Invalid input): Through Dashboard summary, triage, filters, and role composition, create the state where an invalid filter or date range is supplied; assert it is rejected or safely reset without broadening scope.
- **IT-052** (US-006.EC-2, Empty / missing): Through Dashboard summary, triage, filters, and role composition, create the state where no inspections or projects are visible; assert a role-specific empty state appears instead of zero-value noise.
- **IT-053** (US-006.EC-3, Limits): Through Dashboard summary, triage, filters, and role composition, create the state where result counts exceed one page; assert stable pagination or progressive loading preserves priority order.
- **IT-054** (US-006.EC-4, Permissions): Through Dashboard summary, triage, filters, and role composition, create the state where a user deep-links to an out-of-scope card; assert existence and data remain hidden.
- **IT-055** (US-006.EC-5, Concurrency): Through Dashboard summary, triage, filters, and role composition, create the state where classification changes while the home is open; assert refresh identifies the change without applying an action to stale state.
- **IT-056** (US-006.EC-6, Interruption): Through Dashboard summary, triage, filters, and role composition, create the state where loading fails partway; assert already displayed data is marked stale and recovery is explicit.
- **IT-057** (US-006.EC-7, Repetition): Through Dashboard summary, triage, filters, and role composition, create the state where refresh or back navigation repeats the same query; assert filters and selected context remain stable.
- **IT-058** (US-006.EC-8, Ordering): Through Dashboard summary, triage, filters, and role composition, create the state where lower-priority work has an earlier timestamp; assert configured risk ordering still leads, with time used consistently within a priority.
- **IT-059** (US-006.EC-9, State transitions): Through Dashboard summary, triage, filters, and role composition, create the state where a visible inspection becomes canceled or invalidated; assert its card updates and leaves valid outcome totals as required.
- **IT-060** (US-006.EC-10, Scale): Through Dashboard summary, triage, filters, and role composition, create the state where a manager sees 100 times typical portfolio volume; assert summaries, filters, and drill-down remain usable without cross-scope leakage.

#### US-007: Perform permitted inspection and project actions

- **IT-061** (US-007.EC-1, Invalid input): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where invalid dates, recurrence, transition, target, or reason; assert no state changes and the correction is explained.
- **IT-062** (US-007.EC-2, Empty / missing): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where a required asset, participant, template, stage, or reason is missing; assert submission remains blocked.
- **IT-063** (US-007.EC-3, Limits): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where a bulk or recurring operation exceeds supported bounds; assert excess work is rejected with item-level outcomes.
- **IT-064** (US-007.EC-4, Permissions): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where a customer, viewer, or out-of-scope internal user invokes an operation directly; assert it is denied without disclosing hidden state.
- **IT-065** (US-007.EC-5, Concurrency): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where another actor changes the resource first; assert the stale action is rejected and current state is offered.
- **IT-066** (US-007.EC-6, Interruption): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where the connection drops after confirmation; assert dashboard reconciles whether the action succeeded before offering retry.
- **IT-067** (US-007.EC-7, Repetition): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where the same action is submitted twice; assert one business outcome occurs and the prior result is returned.
- **IT-068** (US-007.EC-8, Ordering): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where a later lifecycle step is attempted before its prerequisite; assert the action is unavailable and the required prior state is named.
- **IT-069** (US-007.EC-9, State transitions): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where an operation targets a closed, archived, canceled, or invalidated resource; assert only transitions explicitly allowed by policy remain available.
- **IT-070** (US-007.EC-10, Scale): Through Dashboard inspection, schedule, project, and recapture mutations, create the state where many operations are visible across units; assert filters and grouped outcomes remain attributable to each resource.

#### US-008: Triage findings, reports, and recapture needs

- **IT-071** (US-008.EC-1, Invalid input): Through Dashboard report, publication, evidence, and recapture review, create the state where unknown evidence, blank recapture reason, or nonfinal report publication; assert the action is rejected with a specific explanation.
- **IT-072** (US-008.EC-2, Empty / missing): Through Dashboard report, publication, evidence, and recapture review, create the state where no finding or comparison is available; assert the report explains absence instead of implying a normal result.
- **IT-073** (US-008.EC-3, Limits): Through Dashboard report, publication, evidence, and recapture review, create the state where evidence or report history is large; assert navigation remains grouped by requirement, stage, and version.
- **IT-074** (US-008.EC-4, Permissions): Through Dashboard report, publication, evidence, and recapture review, create the state where a reviewer outside scope opens a comparison or download; assert resource existence and signed access remain hidden.
- **IT-075** (US-008.EC-5, Concurrency): Through Dashboard report, publication, evidence, and recapture review, create the state where two reviewers publish or request the same correction; assert one effective outcome exists and both see current state.
- **IT-076** (US-008.EC-6, Interruption): Through Dashboard report, publication, evidence, and recapture review, create the state where report or media loading fails; assert surrounding metadata remains safe and unavailable content is clearly marked.
- **IT-077** (US-008.EC-7, Repetition): Through Dashboard report, publication, evidence, and recapture review, create the state where publication, download authorization, or recapture request is retried; assert no duplicate report, access grant, or correction responsibility is created.
- **IT-078** (US-008.EC-8, Ordering): Through Dashboard report, publication, evidence, and recapture review, create the state where publication is attempted before finalization; assert dashboard preserves internal state and explains the prerequisite.
- **IT-079** (US-008.EC-9, State transitions): Through Dashboard report, publication, evidence, and recapture review, create the state where an inspection is invalidated after publication; assert history remains visible to authorized users and current status is unmistakable.
- **IT-080** (US-008.EC-10, Scale): Through Dashboard report, publication, evidence, and recapture review, create the state where hundreds of findings or versions exist; assert search and structured grouping prevent loss of evidence lineage.

#### US-009: Review authorized data without mutation controls

- **IT-081** (US-009.EC-1, Invalid input): Through Dashboard viewer queries and direct mutation authorization, create the state where a malformed resource identifier or filter is used; assert no unrelated resource is shown.
- **IT-082** (US-009.EC-2, Empty / missing): Through Dashboard viewer queries and direct mutation authorization, create the state where scope contains no current reports; assert dashboard shows an explanatory empty state.
- **IT-083** (US-009.EC-3, Limits): Through Dashboard viewer queries and direct mutation authorization, create the state where read results exceed one page; assert paging does not alter permissions or ordering.
- **IT-084** (US-009.EC-4, Permissions): Through Dashboard viewer queries and direct mutation authorization, create the state where a viewer attempts a hidden mutation through a direct request; assert it is denied and no state changes.
- **IT-085** (US-009.EC-5, Concurrency): Through Dashboard viewer queries and direct mutation authorization, create the state where access is revoked while a report is open; assert the next read or download is denied immediately.
- **IT-086** (US-009.EC-6, Interruption): Through Dashboard viewer queries and direct mutation authorization, create the state where download is interrupted; assert retry creates fresh authorized access rather than exposing a reusable url.
- **IT-087** (US-009.EC-7, Repetition): Through Dashboard viewer queries and direct mutation authorization, create the state where the same report is opened repeatedly; assert immutable content remains identical for that version.
- **IT-088** (US-009.EC-8, Ordering): Through Dashboard viewer queries and direct mutation authorization, create the state where a deep link arrives before tenant selection; assert only an authorized tenant context can resolve it.
- **IT-089** (US-009.EC-9, State transitions): Through Dashboard viewer queries and direct mutation authorization, create the state where a report is superseded; assert the current version leads while the viewed version stays labeled historical.
- **IT-090** (US-009.EC-10, Scale): Through Dashboard viewer queries and direct mutation authorization, create the state where a viewer has access to many units; assert filters never expand beyond effective scope.

#### US-010: Follow shared assets and projects over time

- **IT-091** (US-010.EC-1, Invalid input): Through Customer portfolio and timeline queries, create the state where an invalid timeline filter or identifier is supplied; assert it is rejected without revealing resource existence.
- **IT-092** (US-010.EC-2, Empty / missing): Through Customer portfolio and timeline queries, create the state where a newly shared asset has no inspections; assert the customer sees an understandable first-use state.
- **IT-093** (US-010.EC-3, Limits): Through Customer portfolio and timeline queries, create the state where timeline history is long; assert chronological paging preserves version and stage continuity.
- **IT-094** (US-010.EC-4, Permissions): Through Customer portfolio and timeline queries, create the state where a customer follows a link to an unshared sibling asset; assert no tenant or resource detail is disclosed.
- **IT-095** (US-010.EC-5, Concurrency): Through Customer portfolio and timeline queries, create the state where a grant is revoked while the timeline is open; assert content disappears on the next interaction and access guidance replaces it.
- **IT-096** (US-010.EC-6, Interruption): Through Customer portfolio and timeline queries, create the state where timeline loading is interrupted; assert partial information is marked incomplete and can be retried safely.
- **IT-097** (US-010.EC-7, Repetition): Through Customer portfolio and timeline queries, create the state where the customer revisits a timeline; assert selected context and published immutable content remain consistent.
- **IT-098** (US-010.EC-8, Ordering): Through Customer portfolio and timeline queries, create the state where events arrive or are processed late; assert the displayed order follows business occurrence time and explains pending state where needed.
- **IT-099** (US-010.EC-9, State transitions): Through Customer portfolio and timeline queries, create the state where a project closes or reopens; assert both transitions remain in history and current status is prominent.
- **IT-100** (US-010.EC-10, Scale): Through Customer portfolio and timeline queries, create the state where a customer owns many assets or projects; assert search and grouping keep the asset/project entry model usable.

#### US-011: Inspect published reports and evidence in simple or advanced mode

- **IT-101** (US-011.EC-1, Invalid input): Through Customer report, evidence, and guarded media access, create the state where a modified evidence or report reference is requested; assert access is denied without disclosing hidden media.
- **IT-102** (US-011.EC-2, Empty / missing): Through Customer report, evidence, and guarded media access, create the state where a published report has no displayable visual evidence; assert the explanation and safe metadata remain meaningful.
- **IT-103** (US-011.EC-3, Limits): Through Customer report, evidence, and guarded media access, create the state where advanced mode contains many media items; assert grouped progressive navigation avoids loading or presenting an unbounded wall of content.
- **IT-104** (US-011.EC-4, Permissions): Through Customer report, evidence, and guarded media access, create the state where a customer has asset access but not the targeted report or inspection; assert content remains unavailable until explicitly inherited or granted.
- **IT-105** (US-011.EC-5, Concurrency): Through Customer report, evidence, and guarded media access, create the state where publication or access is revoked while media is opening; assert the media request is denied and stale access is not reusable.
- **IT-106** (US-011.EC-6, Interruption): Through Customer report, evidence, and guarded media access, create the state where a permitted image fails to load; assert no broken state implies deletion or a different classification; retry guidance is shown.
- **IT-107** (US-011.EC-7, Repetition): Through Customer report, evidence, and guarded media access, create the state where a published immutable version is downloaded or reopened repeatedly; assert content and hashes remain consistent for that version.
- **IT-108** (US-011.EC-8, Ordering): Through Customer report, evidence, and guarded media access, create the state where advanced mode is opened before the report is published; assert no unpublished result or evidence becomes visible.
- **IT-109** (US-011.EC-9, State transitions): Through Customer report, evidence, and guarded media access, create the state where evidence is later replaced through recapture; assert prior and replacement records remain distinct and the current lineage is identified.
- **IT-110** (US-011.EC-10, Scale): Through Customer report, evidence, and guarded media access, create the state where hundreds of evidence records exist; assert mode switching, grouping, and pagination preserve context and accessibility.

#### US-012: Receive safe progress and publication notifications

- **IT-111** (US-012.EC-1, Invalid input): Through Recipient notification projection and external delivery, create the state where an invalid or unverified delivery channel is selected; assert it cannot receive customer notifications.
- **IT-112** (US-012.EC-2, Empty / missing): Through Recipient notification projection and external delivery, create the state where no channel is selected; assert in-product notification remains available and admin shows the delivery limitation.
- **IT-113** (US-012.EC-3, Limits): Through Recipient notification projection and external delivery, create the state where events occur in a burst; assert notices remain attributable and avoid unbounded duplication.
- **IT-114** (US-012.EC-4, Permissions): Through Recipient notification projection and external delivery, create the state where access is revoked before delivery or link opening; assert the message reveals no protected detail and dashboard denies the destination.
- **IT-115** (US-012.EC-5, Concurrency): Through Recipient notification projection and external delivery, create the state where publication and revocation happen together; assert no usable content is exposed after revocation.
- **IT-116** (US-012.EC-6, Interruption): Through Recipient notification projection and external delivery, create the state where a provider fails; assert the in-product state remains accurate and delivery status is visible to authorized administrators.
- **IT-117** (US-012.EC-7, Repetition): Through Recipient notification projection and external delivery, create the state where an event is replayed; assert the customer does not receive duplicate notices for the same business change.
- **IT-118** (US-012.EC-8, Ordering): Through Recipient notification projection and external delivery, create the state where notifications arrive out of order; assert each opens current authorized state and does not present stale status as current.
- **IT-119** (US-012.EC-9, State transitions): Through Recipient notification projection and external delivery, create the state where a report is unpublished or invalidated after notice; assert the destination reflects current visibility and status.
- **IT-120** (US-012.EC-10, Scale): Through Recipient notification projection and external delivery, create the state where a customer follows many assets; assert notifications remain grouped, filterable, and scoped to permitted resources.

#### US-013: Enter one scoped responsibility with link and OTP

- **IT-121** (US-013.EC-1, Invalid input): Through Capture link, OTP, consent, session, and bootstrap, create the state where malformed link or otp; assert generic failure prevents secret or resource inference.
- **IT-122** (US-013.EC-2, Empty / missing): Through Capture link, OTP, consent, session, and bootstrap, create the state where link, otp, or required consent is absent; assert capture cannot proceed.
- **IT-123** (US-013.EC-3, Limits): Through Capture link, OTP, consent, session, and bootstrap, create the state where otp attempts or resend limits are reached; assert further attempts pause according to the stated recovery window.
- **IT-124** (US-013.EC-4, Permissions): Through Capture link, OTP, consent, session, and bootstrap, create the state where a valid session requests another responsibility; assert the target remains undiscoverable.
- **IT-125** (US-013.EC-5, Concurrency): Through Capture link, OTP, consent, session, and bootstrap, create the state where the same invitation is verified on multiple devices; assert session rules remain explicit and never broaden responsibility access.
- **IT-126** (US-013.EC-6, Interruption): Through Capture link, OTP, consent, session, and bootstrap, create the state where verification is interrupted; assert the participant can restart only while the invitation remains valid.
- **IT-127** (US-013.EC-7, Repetition): Through Capture link, OTP, consent, session, and bootstrap, create the state where a successful link or otp action is repeated; assert it does not create duplicate responsibilities or extend authorization unexpectedly.
- **IT-128** (US-013.EC-8, Ordering): Through Capture link, OTP, consent, session, and bootstrap, create the state where capture apis are called before link exchange, otp, or consent; assert each premature step is denied.
- **IT-129** (US-013.EC-9, State transitions): Through Capture link, OTP, consent, session, and bootstrap, create the state where the inspection is canceled, invalidated, submitted, or completed during access; assert the session stops and explains that access is no longer available.
- **IT-130** (US-013.EC-10, Scale): Through Capture link, OTP, consent, session, and bootstrap, create the state where many participants open independent links concurrently; assert each sees only their own responsibility and receives timely feedback.

#### US-014: Register guided origin evidence

- **IT-131** (US-014.EC-1, Invalid input): Through Capture origin drafts, upload, metadata, and submission, create the state where unsupported media, excessive size, invalid metadata, or hostile text; assert the item is rejected with safe corrective guidance.
- **IT-132** (US-014.EC-2, Empty / missing): Through Capture origin drafts, upload, metadata, and submission, create the state where required description, category, evidence, gps, or impossibility reason is absent; assert affected progress remains incomplete.
- **IT-133** (US-014.EC-3, Limits): Through Capture origin drafts, upload, metadata, and submission, create the state where photo or active-evidence limits are reached; assert additional items are blocked without losing accepted work.
- **IT-134** (US-014.EC-4, Permissions): Through Capture origin drafts, upload, metadata, and submission, create the state where the origin session attempts inspection or tenant data; assert access is denied.
- **IT-135** (US-014.EC-5, Concurrency): Through Capture origin drafts, upload, metadata, and submission, create the state where the same draft is uploaded from two active views; assert one evidence record and clear current progress result.
- **IT-136** (US-014.EC-6, Interruption): Through Capture origin drafts, upload, metadata, and submission, create the state where camera, location, network, or upload fails; assert accepted local progress remains recoverable and next steps are explicit.
- **IT-137** (US-014.EC-7, Repetition): Through Capture origin drafts, upload, metadata, and submission, create the state where upload completion or metadata save repeats; assert one logical evidence item remains.
- **IT-138** (US-014.EC-8, Ordering): Through Capture origin drafts, upload, metadata, and submission, create the state where submission occurs before verification; assert finalization is blocked and pending items are identified.
- **IT-139** (US-014.EC-9, State transitions): Through Capture origin drafts, upload, metadata, and submission, create the state where origin responsibility is revoked during capture; assert no further submission is accepted and local state is safely explained.
- **IT-140** (US-014.EC-10, Scale): Through Capture origin drafts, upload, metadata, and submission, create the state where a valid origin contains many requirements and photos; assert progress remains navigable by section and requirement.

#### US-015: Complete guided inspection evidence

- **IT-141** (US-015.EC-1, Invalid input): Through Capture inspection requirements, upload, and submission, create the state where unsupported file, invalid answer, inaccurate required gps, or malformed description; assert the exact item remains incomplete with recovery guidance.
- **IT-142** (US-015.EC-2, Empty / missing): Through Capture inspection requirements, upload, and submission, create the state where no applicable requirements exist; assert capture explains the condition and does not create an empty success silently.
- **IT-143** (US-015.EC-3, Limits): Through Capture inspection requirements, upload, and submission, create the state where media, answer, or description bounds are reached; assert additional input is prevented without corrupting saved progress.
- **IT-144** (US-015.EC-4, Permissions): Through Capture inspection requirements, upload, and submission, create the state where the participant tries to view findings, reports, or another inspection; assert no internal or unrelated content is exposed.
- **IT-145** (US-015.EC-5, Concurrency): Through Capture inspection requirements, upload, and submission, create the state where two devices update one requirement; assert conflicting progress is reconciled without silently overwriting accepted evidence.
- **IT-146** (US-015.EC-6, Interruption): Through Capture inspection requirements, upload, and submission, create the state where permission denial, browser refresh, app backgrounding, or connection loss occurs; assert recoverable progress and next action are explicit.
- **IT-147** (US-015.EC-7, Repetition): Through Capture inspection requirements, upload, and submission, create the state where the same answer or media action repeats; assert one logical outcome is retained.
- **IT-148** (US-015.EC-8, Ordering): Through Capture inspection requirements, upload, and submission, create the state where a later section is reached before a blocking prerequisite; assert capture guides the participant back to the unmet requirement.
- **IT-149** (US-015.EC-9, State transitions): Through Capture inspection requirements, upload, and submission, create the state where deadline expires or responsibility closes during capture; assert further finalization is blocked and current server state is explained.
- **IT-150** (US-015.EC-10, Scale): Through Capture inspection requirements, upload, and submission, create the state where the template contains many requirements; assert section navigation and progress prevent disorientation on mobile.

#### US-016: Replace only requested evidence

- **IT-151** (US-016.EC-1, Invalid input): Through Capture recapture scope, replacement lineage, and submission, create the state where replacement targets an unrequested item; assert the action is rejected without broadening scope.
- **IT-152** (US-016.EC-2, Empty / missing): Through Capture recapture scope, replacement lineage, and submission, create the state where a recapture request has no valid active requirement; assert capture shows unavailable access and accepts no evidence.
- **IT-153** (US-016.EC-3, Limits): Through Capture recapture scope, replacement lineage, and submission, create the state where replacement evidence exceeds policy limits; assert only excess items are blocked and prior lineage stays intact.
- **IT-154** (US-016.EC-4, Permissions): Through Capture recapture scope, replacement lineage, and submission, create the state where an original or other recapture session accesses this request; assert authorization is denied.
- **IT-155** (US-016.EC-5, Concurrency): Through Capture recapture scope, replacement lineage, and submission, create the state where system and reviewer reasons or two replacement attempts overlap; assert one active lineage is preserved with all applicable reasons.
- **IT-156** (US-016.EC-6, Interruption): Through Capture recapture scope, replacement lineage, and submission, create the state where replacement upload is interrupted; assert resumable progress remains tied only to the recapture responsibility.
- **IT-157** (US-016.EC-7, Repetition): Through Capture recapture scope, replacement lineage, and submission, create the state where replacement submission is replayed; assert no duplicate lineage or analysis work becomes visible.
- **IT-158** (US-016.EC-8, Ordering): Through Capture recapture scope, replacement lineage, and submission, create the state where submission is attempted before all selected items are resolved; assert blocking requirements and allowed incomplete behavior are explicit.
- **IT-159** (US-016.EC-9, State transitions): Through Capture recapture scope, replacement lineage, and submission, create the state where the recapture expires during use; assert no late submission is accepted and the participant sees a final unavailable state.
- **IT-160** (US-016.EC-10, Scale): Through Capture recapture scope, replacement lineage, and submission, create the state where many requested corrections exist; assert grouping and progress keep each reason associated with the correct evidence.

#### US-017: Resume interrupted capture and uploads

- **IT-161** (US-017.EC-1, Invalid input): Through Capture IndexedDB and multipart resume reconciliation, create the state where a local draft is corrupt or no longer matches accepted policy; assert it is isolated and recovery does not affect valid drafts.
- **IT-162** (US-017.EC-2, Empty / missing): Through Capture IndexedDB and multipart resume reconciliation, create the state where no recoverable draft exists; assert capture starts from confirmed server progress.
- **IT-163** (US-017.EC-3, Limits): Through Capture IndexedDB and multipart resume reconciliation, create the state where device storage is insufficient; assert the participant is warned before relying on local persistence and receives cleanup guidance.
- **IT-164** (US-017.EC-4, Permissions): Through Capture IndexedDB and multipart resume reconciliation, create the state where the responsibility expires before resume; assert local data cannot restore server authorization or expose protected context.
- **IT-165** (US-017.EC-5, Concurrency): Through Capture IndexedDB and multipart resume reconciliation, create the state where resume runs in multiple tabs; assert completed parts are reconciled and not uploaded as duplicate evidence.
- **IT-166** (US-017.EC-6, Interruption): Through Capture IndexedDB and multipart resume reconciliation, create the state where connectivity repeatedly drops; assert each accepted part remains recognizable and the participant sees current pending state.
- **IT-167** (US-017.EC-7, Repetition): Through Capture IndexedDB and multipart resume reconciliation, create the state where resume is requested after success; assert capture confirms completed state without restarting the upload.
- **IT-168** (US-017.EC-8, Ordering): Through Capture IndexedDB and multipart resume reconciliation, create the state where final submission is attempted before pending uploads resume; assert the action is blocked with the pending count.
- **IT-169** (US-017.EC-9, State transitions): Through Capture IndexedDB and multipart resume reconciliation, create the state where server-side evidence becomes blocked during resume; assert it cannot become ready and the reason is presented safely.
- **IT-170** (US-017.EC-10, Scale): Through Capture IndexedDB and multipart resume reconciliation, create the state where many pending media parts exist; assert progress remains responsive, attributable, and resumable per item.

#### US-018: Submit immutable evidence and receive confirmation

- **IT-171** (US-018.EC-1, Invalid input): Through Capture finalization idempotency and confirmation state, create the state where submission contains unresolved invalid evidence or answers; assert it is rejected with the unresolved items identified.
- **IT-172** (US-018.EC-2, Empty / missing): Through Capture finalization idempotency and confirmation state, create the state where required evidence or reasons are missing; assert complete submission is blocked and only permitted incomplete behavior is offered.
- **IT-173** (US-018.EC-3, Limits): Through Capture finalization idempotency and confirmation state, create the state where submission reaches media or requirement limits; assert accepted items remain stable and only policy-compliant finalization is allowed.
- **IT-174** (US-018.EC-4, Permissions): Through Capture finalization idempotency and confirmation state, create the state where a closed or unrelated session submits; assert the request is denied without reopening responsibility.
- **IT-175** (US-018.EC-5, Concurrency): Through Capture finalization idempotency and confirmation state, create the state where two final submissions race; assert one immutable result exists and both attempts resolve to that final state.
- **IT-176** (US-018.EC-6, Interruption): Through Capture finalization idempotency and confirmation state, create the state where the connection drops after the final action; assert capture checks authoritative state before inviting a retry.
- **IT-177** (US-018.EC-7, Repetition): Through Capture finalization idempotency and confirmation state, create the state where final submission is replayed; assert no evidence, origin version, recapture response, or downstream work is duplicated.
- **IT-178** (US-018.EC-8, Ordering): Through Capture finalization idempotency and confirmation state, create the state where submission is attempted before media verification; assert the participant sees pending verification and cannot complete.
- **IT-179** (US-018.EC-9, State transitions): Through Capture finalization idempotency and confirmation state, create the state where cancellation, invalidation, expiry, or prior completion occurs before final acceptance; assert no new submission is created.
- **IT-180** (US-018.EC-10, Scale): Through Capture finalization idempotency and confirmation state, create the state where a maximum-size valid submission completes; assert final status remains attributable to every requirement and item.

### GraphQL, infrastructure, and persistence contracts

- **IT-181**: `me` returns current local role, entitlements, capabilities, and scopes for a valid audience; a wrong audience or missing entitlement returns `FORBIDDEN` before tenant data.
- **IT-182**: `effectiveAccess` returns a direct `BUSINESS_UNIT` grant and paged asset/project/inspection descendants with `inheritedFrom`.
- **IT-183**: `effectiveAccess` for an unknown or cross-tenant membership returns `NOT_FOUND` without membership metadata.
- **IT-184**: `scopePreview` returns current descendants in stable cursor order and labels archived resources historical.
- **IT-185**: `scopePreview` rejects `UNKNOWN` kind or a foreign resource with `INVALID_INPUT`/`NOT_FOUND` and no descendants.
- **IT-186**: `publicationPolicy` with no row returns `MANUAL`, version 0.
- **IT-187**: `customerPortfolio` returns only scoped assets/projects and a stable cursor at page size 100.
- **IT-188**: `customerPortfolio` rejects malformed filters without widening effective scope.
- **IT-189**: `customerTimeline` orders late-processed events by `occurredAt` and returns `NOT_FOUND` for an unshared sibling.
- **IT-190**: `customerReport` returns the current or requested historically `PUBLISHED` safe projection and returns null for a manual-unpublished version.
- **IT-191**: after invalidation, `customerReport` returns status metadata without body, evidence URL, `canonicalJSON`, or HTML.
- **IT-192**: `customerEvidence(SIMPLE)` returns approved current items; `ADVANCED` additionally returns replaced/discarded lineage.
- **IT-193**: blocked evidence returns `mediaAvailability=SENSITIVE_BLOCKED` and a null URL.
- **IT-194**: unpublished or out-of-scope customer evidence returns no report or evidence content.
- **IT-195**: `myNotifications` returns only the current membership, stable paging, and server unread count.
- **IT-196**: after membership disable, `myNotifications` returns `FORBIDDEN` and no previous page data.
- **IT-197**: `inviteCustomerUser` creates one `PENDING` `CUSTOMER_VIEWER` invite with `DASHBOARD` and one explicit scope.
- **IT-198**: invalid email, empty scope, cross-tenant scope, or `ADMIN` entitlement rejects the customer invite atomically.
- **IT-199**: Keycloak 5xx remains retryable; replay provisions one subject and one membership.
- **IT-200**: an invite for existing active access returns the existing outcome without another membership or email.
- **IT-201**: modified `inviteInternalUser` accepts explicit product entitlements and `PROJECT` scope for a valid internal role.
- **IT-202**: modified `inviteInternalUser` rejects `ADMIN` entitlement for a non-`TENANT_ADMIN` role.
- **IT-203**: `assignMembershipAccess` atomically replaces role, entitlements, and scopes at the expected version.
- **IT-204**: stale `assignMembershipAccess` returns `CONFLICT` and leaves every access row unchanged.
- **IT-205**: removing an entitlement or scope denies the next protected request without waiting for token expiry.
- **IT-206**: `configurePublicationPolicy` creates `AUTOMATIC` from version 0 and returns version 1.
- **IT-207**: unsupported mode or stale policy version leaves the effective policy unchanged.
- **IT-208**: automatic finalization stores snapshot, policy version, publication, audit, and outbox atomically for `CRITICAL` and `ATTENTION` with reason code `INCONCLUSIVE`.
- **IT-209**: manual finalization stores snapshot and policy version while `customerReport` remains null.
- **IT-210**: `publishReport` publishes one final snapshot, emits one event, and echoes `clientMutationId`.
- **IT-211**: `publishReport` maps non-final/missing snapshot to `INVALID_STATE` and out-of-scope target to `NOT_FOUND`.
- **IT-212**: repeated `publishReport` with one `clientMutationId` keeps one publication and one event.
- **IT-213**: publishing a newer snapshot supersedes the former active publication and preserves both audit records.
- **IT-214**: `invalidateReportPublication` records reason/actor/time, removes customer content, and emits one safe notice.
- **IT-215**: blank reason or stale version leaves the publication `PUBLISHED`.
- **IT-216**: repeated invalidation returns current `INVALIDATED` state without another event.
- **IT-217**: `markNotificationRead` sets `readAt` only for the current membership and decrements unread count once.
- **IT-218**: an unknown/foreign notification returns `NOT_FOUND`; repetition preserves the original `readAt`.
- **IT-219**: Admin, Dashboard, and Capture independently generate clients from the same schema revision.
- **IT-220**: a customer GraphQL document selecting `canonicalJSON`, HTML, object key, or internal reasoning fails schema validation.
- **IT-221**: CORS preflight accepts exact configured origins and rejects an unconfigured lookalike origin.
- **IT-222**: an `inspection-admin` token cannot call Dashboard customer operations without `DASHBOARD`.
- **IT-223**: an `inspection-dashboard` token cannot call Admin mutations without `ADMIN`, even for `TENANT_ADMIN`.
- **IT-224**: a Capture external session cannot call any OIDC Admin/Dashboard operation.
- **IT-225**: Keycloak activation binds the provisioned subject and enables Dashboard only after current membership activation.
- **IT-226**: fan-out plus the existing dispatcher creates one in-app record and one attempt per selected verified channel.
- **IT-227**: outbox replay creates no duplicate in-app record or external notification intent.
- **IT-228**: media signing rechecks access after page load and denies revoked or sensitive media.
- **IT-229**: RLS blocks cross-tenant reads for every new access, publication, and notification table.
- **IT-230**: API, worker, and scheduler start at every schema version in the declared deployment compatibility window.

## End-to-End Tests

### US-001: Enter Admin and move safely to Dashboard

- **E2E-001** (US-001.AC-1): In the relevant public product UI, Given an administrator entitled to both products, when sign-in succeeds, then Admin opens in the administrator's tenant and offers an identifiable transition to Dashboard.
- **E2E-002** (US-001.AC-2): In the relevant public product UI, Given an existing authenticated identity, when the user opens Dashboard, then the product reuses sign-in continuity while independently confirming Dashboard permission.
- **E2E-003** (US-001.AC-3): In the relevant public product UI, Given a user entitled to only one product, when they try to open the other product, then they see a denial with a safe route back to an allowed destination.
- **E2E-004** (US-001.AC-4): In the relevant public product UI, Given a product transition, when it completes, then the user can always identify which product and tenant they are using.

### US-002: Configure tenant and business units

- **E2E-005** (US-002.AC-1): In the relevant public product UI, Given an active tenant, when the administrator opens Admin, then current tenant identity, language, timezone, defaults, and business units are visible as configuration.
- **E2E-006** (US-002.AC-2): In the relevant public product UI, Given valid changes, when the administrator saves them, then future work uses the new values while historical inspection snapshots remain unchanged.
- **E2E-007** (US-002.AC-3): In the relevant public product UI, Given a business unit with dependent resources, when its lifecycle changes, then Admin explains the impact before the change and preserves historical visibility.
- **E2E-008** (US-002.AC-4): In the relevant public product UI, Given inherited defaults, when the administrator views a resource, then platform, tenant, and resource-specific values are distinguishable.

### US-003: Invite users and grant hierarchical access

- **E2E-009** (US-003.AC-1): In the relevant public product UI, Given a known recipient, when the administrator invites them, then the invitation identifies whether access is internal or customer-facing and names its intended scope.
- **E2E-010** (US-003.AC-2): In the relevant public product UI, Given a user, when the administrator grants business-unit, asset, project, or inspection access, then Admin previews the effective descendants before confirmation.
- **E2E-011** (US-003.AC-3): In the relevant public product UI, Given a customer role, when access becomes active, then the user can enter Dashboard but cannot enter Admin or use internal-only actions.
- **E2E-012** (US-003.AC-4): In the relevant public product UI, Given an active grant, when the administrator revokes it, then access stops immediately across future views and actions.
- **E2E-013** (US-003.AC-5): In the relevant public product UI, Given multiple grants, when effective access is reviewed, then the product explains which direct or inherited grant permits each resource.

### US-004: Manage participants, catalogs, templates, and assets

- **E2E-014** (US-004.AC-1): In the relevant public product UI, Given tenant scope, when the administrator registers or updates participants and verified delivery channels, then future invitations use only active selected channels.
- **E2E-015** (US-004.AC-2): In the relevant public product UI, Given valid catalog content, when the administrator publishes a segment, template, or analysis profile, then the published version is immutable and available to future work.
- **E2E-016** (US-004.AC-3): In the relevant public product UI, Given an asset, when the administrator assigns its unit, segment data, template, location, participants, and policy overrides, then effective configuration is visible before use.
- **E2E-017** (US-004.AC-4): In the relevant public product UI, Given later configuration changes, when historical work is viewed, then it continues to display its original fixed versions.

### US-005: Configure publication, notification, retention, and audit governance

- **E2E-018** (US-005.AC-1): In the relevant public product UI, Given no explicit publication policy, when a report becomes final, then it remains internal until an authorized user publishes it manually.
- **E2E-019** (US-005.AC-2): In the relevant public product UI, Given automatic publication is enabled, when any report becomes final, then it becomes customer-visible even if critical or inconclusive and retains clear advisory labels.
- **E2E-020** (US-005.AC-3): In the relevant public product UI, Given configured customer channels, when a relevant progress or publication event occurs, then safe notifications use those channels and appear in Dashboard.
- **E2E-021** (US-005.AC-4): In the relevant public product UI, Given retention and privacy rules, when the administrator reviews them, then effective periods, restrictions, and pending requests are clear.
- **E2E-022** (US-005.AC-5): In the relevant public product UI, Given an administrative or sensitive operational action, when it completes or fails, then the audit view records actor, tenant, target, time, reason when required, and outcome.

### US-006: See a role-adaptive operational home

- **E2E-023** (US-006.AC-1): In the relevant public product UI, Given an internal manager, when Dashboard opens, then critical, attention, overdue, pending, and recently changed work is prioritized within scope.
- **E2E-024** (US-006.AC-2): In the relevant public product UI, Given an employee, viewer, or customer, when Dashboard opens, then the home changes to the resources and actions allowed for that role rather than exposing disabled administrative concepts.
- **E2E-025** (US-006.AC-3): In the relevant public product UI, Given selected filters, when the user drills into a result, then the destination preserves tenant, unit, asset, project, period, status, classification, and flag context where applicable.
- **E2E-026** (US-006.AC-4): In the relevant public product UI, Given no matching work, when the home loads, then it explains the empty state and offers only permitted next actions.

### US-007: Perform permitted inspection and project actions

- **E2E-027** (US-007.AC-1): In the relevant public product UI, Given adequate permission, when the user creates or cancels an inspection, manages a schedule or stage, closes or reopens a project, or requests recapture, then Dashboard shows the resulting state and audit-relevant reason.
- **E2E-028** (US-007.AC-2): In the relevant public product UI, Given read-only or customer access, when the same resource opens, then mutation controls are absent and direct attempts are denied.
- **E2E-029** (US-007.AC-3): In the relevant public product UI, Given an action with downstream processing, when it is accepted, then Dashboard distinguishes accepted, pending, completed, and failed outcomes.
- **E2E-030** (US-007.AC-4): In the relevant public product UI, Given a destructive or history-affecting action, when confirmation is requested, then the user sees the target, consequence, and required reason before committing.

### US-008: Triage findings, reports, and recapture needs

- **E2E-031** (US-008.AC-1): In the relevant public product UI, Given completed analysis, when the reviewer opens an inspection, then reference and current evidence, findings, confidence, quality, flags, and advisory classification are connected visibly.
- **E2E-032** (US-008.AC-2): In the relevant public product UI, Given incomplete, critical, or inconclusive analysis, when it is reviewed, then uncertainty and pending work are explicit and never presented as fact or fault.
- **E2E-033** (US-008.AC-3): In the relevant public product UI, Given deficient evidence, when an authorized reviewer requests recapture, then selected items and reasons are visible before confirmation.
- **E2E-034** (US-008.AC-4): In the relevant public product UI, Given manual publication mode and a final report, when an authorized reviewer publishes it, then permitted customers see that exact immutable version.
- **E2E-035** (US-008.AC-5): In the relevant public product UI, Given report history, when a user selects a prior version, then the product distinguishes it from the current version and preserves its original context.

### US-009: Review authorized data without mutation controls

- **E2E-036** (US-009.AC-1): In the relevant public product UI, Given viewer access, when Dashboard loads, then only resources inside effective scope are visible.
- **E2E-037** (US-009.AC-2): In the relevant public product UI, Given an authorized report, when it opens, then published and internal visibility follows the viewer's role and the report's current state.
- **E2E-038** (US-009.AC-3): In the relevant public product UI, Given a read-only role, when navigation renders, then mutation controls and administrative destinations are absent.
- **E2E-039** (US-009.AC-4): In the relevant public product UI, Given a permitted download, when it is requested, then only the selected authorized immutable report is delivered.

### US-010: Follow shared assets and projects over time

- **E2E-040** (US-010.AC-1): In the relevant public product UI, Given customer access, when Dashboard opens, then permitted assets and projects are the primary entry points.
- **E2E-041** (US-010.AC-2): In the relevant public product UI, Given a selected asset or project, when its timeline opens, then inspection status, milestones, progress, recapture state, and published reports appear chronologically.
- **E2E-042** (US-010.AC-3): In the relevant public product UI, Given multiple direct and inherited grants, when the customer changes context, then only permitted descendants are discoverable.
- **E2E-043** (US-010.AC-4): In the relevant public product UI, Given unpublished internal work, when the timeline loads, then safe progress may appear but internal notes and unpublished result content do not.

### US-011: Inspect published reports and evidence in simple or advanced mode

- **E2E-044** (US-011.AC-1): In the relevant public product UI, Given a published report, when it opens, then simple mode shows the outcome, advisory explanation, key evidence, progress context, and current report version.
- **E2E-045** (US-011.AC-2): In the relevant public product UI, Given advanced mode, when the customer enables it, then original, replacement, superseded, and discarded evidence records are organized by requirement, stage, and lineage.
- **E2E-046** (US-011.AC-3): In the relevant public product UI, Given evidence blocked for possible sensitive content, when either mode renders, then the visual file is never shown and only safe metadata, state, and reason are available.
- **E2E-047** (US-011.AC-4): In the relevant public product UI, Given a critical or inconclusive automatically published report, when it opens, then its exact classification, uncertainty, and advisory nature are prominent.
- **E2E-048** (US-011.AC-5): In the relevant public product UI, Given a prior report version, when it is selected, then it is clearly historical and cannot be mistaken for the current published result.

### US-012: Receive safe progress and publication notifications

- **E2E-049** (US-012.AC-1): In the relevant public product UI, Given configured and verified channels, when an authorized asset or project reaches a relevant progress event, then the customer receives one safe notification per selected channel and one in-product notification.
- **E2E-050** (US-012.AC-2): In the relevant public product UI, Given a newly published report, when notification is sent, then it identifies safe context and links to an authorization-checked Dashboard destination.
- **E2E-051** (US-012.AC-3): In the relevant public product UI, Given recapture or delay that changes visible progress, when it occurs, then the notice explains the status without attaching evidence or internal reasoning.
- **E2E-052** (US-012.AC-4): In the relevant public product UI, Given notification preferences change, when saved, then future notices follow the new selection without changing prior delivery records.

### US-013: Enter one scoped responsibility with link and OTP

- **E2E-053** (US-013.AC-1): In the relevant public product UI, Given a valid invitation link, when it opens, then Capture exchanges and removes the secret from visible browser history before showing the OTP step.
- **E2E-054** (US-013.AC-2): In the relevant public product UI, Given the valid OTP, when verification succeeds, then the participant sees only the assigned origin, inspection, or recapture responsibility.
- **E2E-055** (US-013.AC-3): In the relevant public product UI, Given required disclosure, when consent is accepted, then the guided responsibility becomes available; refusal prevents capture.
- **E2E-056** (US-013.AC-4): In the relevant public product UI, Given an expired, revoked, completed, or invalid invitation, when it opens, then no responsibility detail is disclosed and recovery guidance is shown.

### US-014: Register guided origin evidence

- **E2E-057** (US-014.AC-1): In the relevant public product UI, Given an origin responsibility, when Capture starts, then requirements, categories, instructions, minimum evidence, descriptions, and policy expectations are explicit.
- **E2E-058** (US-014.AC-2): In the relevant public product UI, Given a photo, when it is added, then source, time, description, required location context, progress, and quality or sensitive-content state are visible.
- **E2E-059** (US-014.AC-3): In the relevant public product UI, Given a requirement cannot be completed and impossibility is allowed, when a nonblank reason is submitted, then progress reflects the accepted exception.
- **E2E-060** (US-014.AC-4): In the relevant public product UI, Given all submission conditions are met or incompleteness is explicitly confirmed where allowed, when submitted, then a new immutable origin version results.

### US-015: Complete guided inspection evidence

- **E2E-061** (US-015.AC-1): In the relevant public product UI, Given an inspection responsibility, when Capture opens, then only applicable requirements, reference context, descriptions, checklists, policies, and progress are shown.
- **E2E-062** (US-015.AC-2): In the relevant public product UI, Given camera, gallery, location, and quality policies, when evidence is added, then allowed actions and advisory flags are explicit.
- **E2E-063** (US-015.AC-3): In the relevant public product UI, Given a required item cannot be produced and policy allows it, when a reason is accepted, then the submission can remain explicitly incomplete or attention-bearing as defined.
- **E2E-064** (US-015.AC-4): In the relevant public product UI, Given keyboard, screen reader, reduced viewport, or non-color perception, when the participant uses the journey, then every primary action and state remains understandable.

### US-016: Replace only requested evidence

- **E2E-065** (US-016.AC-1): In the relevant public product UI, Given a recapture invitation, when access succeeds, then only selected requirements, current reasons, deadline, and permitted reference context appear.
- **E2E-066** (US-016.AC-2): In the relevant public product UI, Given replacement evidence, when submitted, then prior evidence remains in lineage and the replacement is identified as current for the selected requirement.
- **E2E-067** (US-016.AC-3): In the relevant public product UI, Given multiple reasons for one requested item, when it opens, then all active reasons are understandable without exposing internal notes.
- **E2E-068** (US-016.AC-4): In the relevant public product UI, Given recapture completion or expiry, when analysis continues, then Capture closes and Dashboard reflects the resulting current state.

### US-017: Resume interrupted capture and uploads

- **E2E-069** (US-017.AC-1): In the relevant public product UI, Given pending drafts, when Capture refreshes or connectivity returns, then the participant sees saved progress and can resume unfinished uploads.
- **E2E-070** (US-017.AC-2): In the relevant public product UI, Given an offline period, when evidence is captured, then local-pending status is explicit and final submission remains unavailable.
- **E2E-071** (US-017.AC-3): In the relevant public product UI, Given expired upload authorization but an active responsibility, when resume starts, then Capture obtains a safe continuation path without duplicating completed work.
- **E2E-072** (US-017.AC-4): In the relevant public product UI, Given the server verifies all required media, when progress refreshes, then only verified items become ready for final submission.

### US-018: Submit immutable evidence and receive confirmation

- **E2E-073** (US-018.AC-1): In the relevant public product UI, Given ready evidence and connectivity, when the participant reviews final progress, then the product explains whether submission is complete or explicitly incomplete before confirmation.
- **E2E-074** (US-018.AC-2): In the relevant public product UI, Given final confirmation, when the server accepts submission, then the evidence set becomes immutable and the responsibility session closes.
- **E2E-075** (US-018.AC-3): In the relevant public product UI, Given successful submission, when the confirmation view appears, then it contains no submitted media, internal finding, classification, or report.
- **E2E-076** (US-018.AC-4): In the relevant public product UI, Given a later correction need, when an authorized user requests it, then a new recapture invitation is required and the prior submission remains unchanged.

## Non-Functional Gates

- Security: no response, trace, screenshot, or log contains bearer/refresh tokens, OTPs, invitation tokens, object keys, raw internal report JSON/HTML, or sensitive media URLs; cross-product, cross-tenant, revoked, and customer-to-internal probes fail closed.
- Accessibility: each journey has no serious/critical axe violation at its principal view; keyboard-only flows complete with visible focus.
- Performance: hierarchy authorization p95 <=50 ms, simple GraphQL p95 <=300 ms, mutations p95 <=500 ms excluding providers, useful frontend views p95 <=2 s, and notification projection lag <60 s.
- Capacity: 500 concurrent Capture sessions globally and 100 per tenant complete without data crossover, pool exhaustion, or lost accepted multipart parts; portfolio paging remains stable at 100x typical volume.
- Build isolation: each app clean-installs, generates, tests, builds, images, and starts without another frontend's node_modules; every lockfile pins an exact design-system version.
