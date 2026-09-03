# Test Specification: Autonomous Inspection Platform

Canonical test contract for the Autonomous Inspection Platform. Companion to `_techspec.md`; derived from `_user_stories.md` and the accepted ADRs.

## Strategy

- Go unit and slice tests use `testing`, `testify`, table-driven fixtures, deterministic clocks/IDs, and fakes only at I/O boundaries. Every feature is entered through its public `Setup` registration.
- Integration tests use Testcontainers for PostgreSQL, RabbitMQ, MinIO, Dragonfly, LiteLLM stubs, and Gotenberg. They run against migrated schemas and non-owner runtime database roles.
- Browser journeys use Playwright against the production-built Next.js app and real API boundaries, with Mailpit/fake Twilio, mobile viewports, service-worker control, geolocation permission fixtures, accessibility checks, and isolated tenants.
- IDs are permanent and sequential. `UT` covers pure components and contract logic, `IT` covers slice/infrastructure/API/message behavior, and `E2E` follows a user-visible journey.
- Test data uses synthetic contacts and generated non-personal images. No production credentials, faces, documents, or tenant data enter fixtures.

## Coverage Matrix

| Source | Behavior | Unit | Integration | E2E |
|---|---|---|---|---|
| US-001 | Configure tenant and business units | — | — | E2E-001 |
| US-001.EC-1 | Invalid timezone or blank tenant name → the change is rejected with the invalid field identified. | — | IT-001 | — |
| US-001.EC-2 | No business unit on initial setup → activation remains unavailable until one is added. | — | IT-002 | — |
| US-001.EC-3 | A configured organizational limit is reached → additional units are rejected without changing existing units. | — | IT-003 | — |
| US-001.EC-4 | A non-administrator attempts tenant configuration → access is denied without revealing settings outside their scope. | — | IT-004 | — |
| US-001.EC-5 | Two administrators edit the same unit → the stale save is rejected and the current values are shown. | — | IT-005 | — |
| US-001.EC-6 | Setup stops before completion → a draft remains resumable and the tenant is not presented as active. | — | IT-006 | — |
| US-001.EC-7 | The same create request is retried → only one tenant or unit is created. | — | IT-007 | — |
| US-001.EC-8 | A unit-dependent configuration is attempted before the unit exists → the user is directed to create the prerequisite. | — | IT-008 | — |
| US-001.EC-9 | An archived unit is edited → the edit is rejected until the unit is restored. | — | IT-009 | — |
| US-001.EC-10 | Hundreds of units exist → lists remain paginated/searchable and counts remain accurate. | — | IT-010 | — |
| US-002 | Assign hierarchical internal access | — | — | E2E-002 |
| US-002.EC-1 | Unknown role or resource identifier → assignment is rejected. | — | IT-011 | — |
| US-002.EC-2 | A scoped role has no assignments → the user sees an explanatory empty state and no operational data. | — | IT-012 | — |
| US-002.EC-3 | A bulk assignment exceeds the supported batch size → the user is asked to split it and no partial batch is applied. | — | IT-013 | — |
| US-002.EC-4 | A manager tries to grant tenant-administrator access → access is denied. | — | IT-014 | — |
| US-002.EC-5 | Scope changes while the user is active → the next protected action reflects the new scope. | — | IT-015 | — |
| US-002.EC-6 | Bulk scope assignment is interrupted → completed changes are listed and unprocessed changes remain unapplied. | — | IT-016 | — |
| US-002.EC-7 | The same role or assignment is applied twice → the result remains a single assignment. | — | IT-017 | — |
| US-002.EC-8 | Scope is assigned before the user accepts an internal invitation → it becomes effective only after account activation. | — | IT-018 | — |
| US-002.EC-9 | A disabled user attempts access with an existing session → access is denied immediately. | — | IT-019 | — |
| US-002.EC-10 | A user has thousands of assigned assets → authorization remains consistent across paginated lists, direct links, exports, and media. | — | IT-020 | — |
| US-003 | Manage external participants and delivery channels | — | — | E2E-003 |
| US-003.EC-1 | Malformed email or phone number → that channel cannot be saved or marked. | — | IT-021 | — |
| US-003.EC-2 | No verified channel is marked → invitation creation is blocked with a contact requirement. | — | IT-022 | — |
| US-003.EC-3 | More contacts are supplied than the participant limit → extras are rejected with the limit explained. | — | IT-023 | — |
| US-003.EC-4 | An employee without participant-management scope edits a participant → access is denied. | — | IT-024 | — |
| US-003.EC-5 | Two managers change marked channels → the stale update is rejected. | — | IT-025 | — |
| US-003.EC-6 | Verification is interrupted → the channel remains unverified and cannot receive invitations. | — | IT-026 | — |
| US-003.EC-7 | The same contact is added again → it is reused rather than duplicated. | — | IT-027 | — |
| US-003.EC-8 | A channel is marked before verification → marking is refused until verification succeeds. | — | IT-028 | — |
| US-003.EC-9 | An inactive participant is selected for a new inspection → selection is blocked. | — | IT-029 | — |
| US-003.EC-10 | A tenant has a large participant directory → search and filtering identify the intended participant without cross-tenant results. | — | IT-030 | — |
| US-004 | Review the audit trail | — | — | E2E-004 |
| US-004.EC-1 | Invalid date range or filter → the query is rejected with correction guidance. | — | IT-031 | — |
| US-004.EC-2 | No records match → an empty result is shown without implying audit is disabled. | — | IT-032 | — |
| US-004.EC-3 | A range contains more records than one page → pagination preserves stable ordering and filters. | — | IT-033 | — |
| US-004.EC-4 | A user without audit permission requests records → access is denied. | — | IT-034 | — |
| US-004.EC-5 | New events arrive during navigation → existing pages remain stable and a refresh exposes newer records. | — | IT-035 | — |
| US-004.EC-6 | Export or long query is interrupted → no corrupt artifact is presented and the user can retry. | — | IT-036 | — |
| US-004.EC-7 | The same action is safely retried → each attempted outcome is distinguishable without duplicating the business result. | — | IT-037 | — |
| US-004.EC-8 | Events arrive after related business data → correlation still groups them in causal order when available. | — | IT-038 | — |
| US-004.EC-9 | The target is later archived or deleted → its audit records remain readable for their retention period. | — | IT-039 | — |
| US-004.EC-10 | Audit volume reaches 100× typical activity → filters, pagination, and tenant isolation remain correct. | — | IT-040 | — |
| US-005 | Publish immutable template versions | — | — | E2E-005 |
| US-005.EC-1 | Definition violates its allowed schema or references an unknown component → publication is rejected with validation details. | — | IT-041 | — |
| US-005.EC-2 | Template has no capture requirement → publication is rejected. | — | IT-042 | — |
| US-005.EC-3 | Definition exceeds configured section, field, or requirement limits → publication is rejected without truncation. | — | IT-043 | — |
| US-005.EC-4 | Unauthorized user attempts activation → access is denied. | — | IT-044 | — |
| US-005.EC-5 | Two versions are activated at once → one active version is selected deterministically and the conflict is reported. | — | IT-045 | — |
| US-005.EC-6 | Publication stops before completion → the version remains unpublished and cannot be selected. | — | IT-046 | — |
| US-005.EC-7 | The same publication is retried → no duplicate version is created. | — | IT-047 | — |
| US-005.EC-8 | Activation is requested before validation → validation must succeed first. | — | IT-048 | — |
| US-005.EC-9 | An active version is retired → existing work remains accessible and new work requires another active version. | — | IT-049 | — |
| US-005.EC-10 | A tenant can choose among many versions → lists remain searchable and clearly distinguish status and effective version. | — | IT-050 | — |
| US-006 | Register a segment-specific asset | — | — | E2E-006 |
| US-006.EC-1 | Segment-specific data violates its schema or geofence radius is out of range → save is rejected with field details. | — | IT-051 | — |
| US-006.EC-2 | Required address, identifier, or segment field is absent → activation is unavailable. | — | IT-052 | — |
| US-006.EC-3 | Asset media, attribute, or text limits are exceeded → only the invalid change is rejected. | — | IT-053 | — |
| US-006.EC-4 | User lacks scope for the business unit → creation or direct access is denied. | — | IT-054 | — |
| US-006.EC-5 | Two users edit the asset → stale changes are rejected. | — | IT-055 | — |
| US-006.EC-6 | Creation stops mid-flow → a clearly marked draft may be resumed and cannot be scheduled. | — | IT-056 | — |
| US-006.EC-7 | Same create request is retried → one asset results. | — | IT-057 | — |
| US-006.EC-8 | Schedule creation is attempted before asset activation → the prerequisite is explained. | — | IT-058 | — |
| US-006.EC-9 | Archived asset receives a new occurrence → creation is blocked. | — | IT-059 | — |
| US-006.EC-10 | A business unit has thousands of assets → list, filter, and selection behavior remain usable and isolated. | — | IT-060 | — |
| US-007 | Configure comparison and capture policies | — | — | E2E-007 |
| US-007.EC-1 | Incompatible comparison or report settings are combined → save is rejected with the conflict identified. | — | IT-061 | — |
| US-007.EC-2 | A reference-required mode has no eligible reference → occurrence creation is blocked. | — | IT-062 | — |
| US-007.EC-3 | A policy value such as radius or stage count exceeds its range → save is rejected. | — | IT-063 | — |
| US-007.EC-4 | User outside asset scope changes an override → access is denied. | — | IT-064 | — |
| US-007.EC-5 | Template default and asset override change concurrently → the occurrence records the single resolved snapshot it actually used. | — | IT-065 | — |
| US-007.EC-6 | Configuration save is interrupted → prior active settings remain effective. | — | IT-066 | — |
| US-007.EC-7 | Same override is applied twice → one effective rule is shown. | — | IT-067 | — |
| US-007.EC-8 | Asset override is configured before template selection → the user must choose the template first. | — | IT-068 | — |
| US-007.EC-9 | Policy is changed after capture starts → current inspection remains unchanged. | — | IT-069 | — |
| US-007.EC-10 | Many assets inherit one template → changes apply to future occurrences without rewriting each historical asset occurrence. | — | IT-070 | — |
| US-008 | Invite an origin contributor | — | — | E2E-008 |
| US-008.EC-1 | Unknown asset or participant channel → invitation is rejected. | — | IT-071 | — |
| US-008.EC-2 | No marked verified channel → send is blocked. | — | IT-072 | — |
| US-008.EC-3 | Send or resend rate limit is reached → the user sees when another attempt is allowed. | — | IT-073 | — |
| US-008.EC-4 | User lacks asset or invitation permission → send is denied. | — | IT-074 | — |
| US-008.EC-5 | Two invitations are created simultaneously → each remains identifiable and only valid active invitations are presented. | — | IT-075 | — |
| US-008.EC-6 | Delivery fails on some channels → outcomes are recorded per channel and retry remains available. | — | IT-076 | — |
| US-008.EC-7 | A send action is retried after success → it does not create duplicate business invitations. | — | IT-077 | — |
| US-008.EC-8 | Link is opened before delivery status completes → valid access can still proceed once OTP is requested. | — | IT-078 | — |
| US-008.EC-9 | Asset is archived or invitation expires → the link no longer grants access. | — | IT-079 | — |
| US-008.EC-10 | Bulk origin invitations are sent → every recipient remains isolated and delivery results remain attributable. | — | IT-080 | — |
| US-009 | Capture a described origin | — | — | E2E-009 |
| US-009.EC-1 | Unsupported media, blank description, or hostile text → the item is rejected with a corrective message. | — | IT-081 | — |
| US-009.EC-2 | Finish is attempted with zero photos → completion is blocked. | — | IT-082 | — |
| US-009.EC-3 | A file or origin exceeds configured limits → the extra item is rejected without losing accepted items. | — | IT-083 | — |
| US-009.EC-4 | Expired or wrong-origin token is used → access is denied. | — | IT-084 | — |
| US-009.EC-5 | Same origin session opens on two devices → conflicts are surfaced and no item is silently overwritten. | — | IT-085 | — |
| US-009.EC-6 | Browser closes during capture → accepted local progress can resume while the invitation remains valid. | — | IT-086 | — |
| US-009.EC-7 | Same completed upload is confirmed twice → one origin item results. | — | IT-087 | — |
| US-009.EC-8 | Finish is attempted while uploads are pending → completion waits or identifies pending items. | — | IT-088 | — |
| US-009.EC-9 | Session is used after completion → mutation is blocked and confirmation is shown. | — | IT-089 | — |
| US-009.EC-10 | Origin contains many allowed items → progress, categorization, and upload status remain understandable. | — | IT-090 | — |
| US-010 | Activate and supersede an origin version | — | — | E2E-010 |
| US-010.EC-1 | Completed origin lacks a valid described item → activation is rejected. | — | IT-091 | — |
| US-010.EC-2 | No prior origin exists → the first valid version becomes active. | — | IT-092 | — |
| US-010.EC-3 | Version history exceeds one page → versions remain paginated in activation order. | — | IT-093 | — |
| US-010.EC-4 | Unauthorized user attempts manual version changes → access is denied. | — | IT-094 | — |
| US-010.EC-5 | Two origin versions complete simultaneously → one activation order is recorded without overwriting either version. | — | IT-095 | — |
| US-010.EC-6 | Activation processing is interrupted → it resumes without creating a second version. | — | IT-096 | — |
| US-010.EC-7 | Activation is retried → the same version remains active once. | — | IT-097 | — |
| US-010.EC-8 | An inspection is created during origin activation → it either receives the previous or new complete version, never a partial version. | — | IT-098 | — |
| US-010.EC-9 | Active origin is retired with dependent schedules → new occurrences are blocked until another valid origin is active. | — | IT-099 | — |
| US-010.EC-10 | Thousands of inspections reference older origins → activating a new version does not rewrite or slow historical access materially. | — | IT-100 | — |
| US-011 | Configure recurring inspections | — | — | E2E-011 |
| US-011.EC-1 | Invalid or impossible recurrence expression → save is rejected with an understandable schedule error. | — | IT-101 | — |
| US-011.EC-2 | Participant, template, timezone, or required reference is absent → activation is blocked. | — | IT-102 | — |
| US-011.EC-3 | Recurrence is more frequent than the allowed minimum → save is rejected with the minimum explained. | — | IT-103 | — |
| US-011.EC-4 | User lacks schedule permission for the asset → access is denied. | — | IT-104 | — |
| US-011.EC-5 | The same due occurrence is processed twice → one inspection is created. | — | IT-105 | — |
| US-011.EC-6 | Processing stops after occurrence creation but before notification → notification resumes without duplicating the inspection. | — | IT-106 | — |
| US-011.EC-7 | Manual technical retry runs after success → schedule and invitation remain single. | — | IT-107 | — |
| US-011.EC-8 | Next occurrence is calculated before current creation commits → no time is skipped or duplicated. | — | IT-108 | — |
| US-011.EC-9 | Schedule is canceled or asset archived → no later occurrence is created. | — | IT-109 | — |
| US-011.EC-10 | Many schedules become due together → every eligible occurrence is eventually created once and remains tenant-isolated. | — | IT-110 | — |
| US-012 | Create milestone and manual inspections | — | — | E2E-012 |
| US-012.EC-1 | Unknown milestone, invalid due date, or incompatible template → creation is rejected. | — | IT-111 | — |
| US-012.EC-2 | Manual reason or responsible participant is missing → creation is blocked. | — | IT-112 | — |
| US-012.EC-3 | Too many simultaneous open inspections for an asset → creation is rejected with the applicable limit. | — | IT-113 | — |
| US-012.EC-4 | User lacks asset or manual-create permission → access is denied. | — | IT-114 | — |
| US-012.EC-5 | Two users start the same milestone → only one milestone occurrence results. | — | IT-115 | — |
| US-012.EC-6 | Creation stops before the occurrence is complete → no participant receives a partial invitation. | — | IT-116 | — |
| US-012.EC-7 | The same request is retried → one occurrence results. | — | IT-117 | — |
| US-012.EC-8 | A later planned stage starts before a required prior stage decision → the template rule blocks or explicitly permits it. | — | IT-118 | — |
| US-012.EC-9 | Closed project or archived asset receives a request → creation is blocked until an authorized reopening where applicable. | — | IT-119 | — |
| US-012.EC-10 | A manager creates occurrences across many assets → bulk status remains attributable and partial failures are explicit. | — | IT-120 | — |
| US-013 | Configure deadlines and reminders | — | — | E2E-013 |
| US-013.EC-1 | Reminder occurs after expiry or dates are unparseable → save is rejected. | — | IT-121 | — |
| US-013.EC-2 | No tenant default and no occurrence value exists → activation is blocked with the missing rule identified. | — | IT-122 | — |
| US-013.EC-3 | Reminder count exceeds the tenant limit → extra reminders are rejected. | — | IT-123 | — |
| US-013.EC-4 | Unauthorized user changes deadlines → access is denied. | — | IT-124 | — |
| US-013.EC-5 | Deadline changes while delivery is being prepared → each notice uses one consistent saved schedule. | — | IT-125 | — |
| US-013.EC-6 | Delivery provider fails → per-channel failure is visible and bounded retry remains possible. | — | IT-126 | — |
| US-013.EC-7 | Reminder processing repeats → a due reminder is not intentionally sent more than once per channel except a recorded technical retry. | — | IT-127 | — |
| US-013.EC-8 | A reminder becomes due before the invitation → invitation is sent first or the invalid configuration is rejected. | — | IT-128 | — |
| US-013.EC-9 | Inspection is canceled, invalidated, submitted, or expired → inapplicable future notices stop. | — | IT-129 | — |
| US-013.EC-10 | Large reminder bursts occur → delivery status remains accurate per inspection and channel. | — | IT-130 | — |
| US-014 | Cancel or invalidate an inspection | — | — | E2E-014 |
| US-014.EC-1 | Blank invalidation reason → action is rejected. | — | IT-131 | — |
| US-014.EC-2 | Cancellation target does not exist → no data is changed and not-found is shown. | — | IT-132 | — |
| US-014.EC-3 | Reason exceeds its length limit → action is rejected without truncation. | — | IT-133 | — |
| US-014.EC-4 | Viewer or out-of-scope user attempts action → access is denied. | — | IT-134 | — |
| US-014.EC-5 | Submission and cancellation race → submitted evidence wins finality and only invalidation remains possible. | — | IT-135 | — |
| US-014.EC-6 | Action stops after state change → notifications and access eventually reflect the same final state. | — | IT-136 | — |
| US-014.EC-7 | Cancel or invalidate is repeated → state remains unchanged and no duplicate audit reason is manufactured. | — | IT-137 | — |
| US-014.EC-8 | Invalidation is attempted before evidence exists → the product offers cancellation instead. | — | IT-138 | — |
| US-014.EC-9 | Already canceled or invalidated inspection receives a mutation → invalid transition is rejected. | — | IT-139 | — |
| US-014.EC-10 | Many invalid records exist → dashboard exclusions and explicit history filters remain correct. | — | IT-140 | — |
| US-015 | Authenticate and accept required processing | — | — | E2E-015 |
| US-015.EC-1 | Wrong or malformed OTP → attempt is rejected without disclosing the expected code. | — | IT-141 | — |
| US-015.EC-2 | Code or required acceptance is missing → capture remains unavailable. | — | IT-142 | — |
| US-015.EC-3 | Attempt or resend rate limit is reached → further attempts wait until the displayed limit resets. | — | IT-143 | — |
| US-015.EC-4 | Token for another inspection is used → access is denied without revealing that inspection. | — | IT-144 | — |
| US-015.EC-5 | Same code is validated simultaneously → sessions remain bounded to the same invitation and policy. | — | IT-145 | — |
| US-015.EC-6 | Browser closes after validation → re-entry follows remaining session and invitation validity. | — | IT-146 | — |
| US-015.EC-7 | Used or expired code is replayed → it is rejected. | — | IT-147 | — |
| US-015.EC-8 | Capture route is opened before authentication or acceptance → the user is returned to the missing step. | — | IT-148 | — |
| US-015.EC-9 | Canceled, completed, or expired invitation is opened → access is denied with a safe status message. | — | IT-149 | — |
| US-015.EC-10 | Many participants request codes together → one participant's rate or delivery status never affects another's identity or data. | — | IT-150 | — |
| US-016 | Complete guided capture requirements | — | — | E2E-016 |
| US-016.EC-1 | Unsupported file or blank impossibility reason → requirement remains incomplete with guidance. | — | IT-151 | — |
| US-016.EC-2 | Template has no applicable requirements → participant sees a controlled empty state and cannot fabricate a submission. | — | IT-152 | — |
| US-016.EC-3 | Per-item or total media limit is reached → additional media is rejected without removing accepted evidence. | — | IT-153 | — |
| US-016.EC-4 | Session does not own the requirement → direct access is denied. | — | IT-154 | — |
| US-016.EC-5 | Same requirement is edited on two devices → conflicts are shown and no evidence is silently overwritten. | — | IT-155 | — |
| US-016.EC-6 | Page refresh occurs → saved and local resumable progress returns. | — | IT-156 | — |
| US-016.EC-7 | Same photo completion is retried → one evidence item is associated. | — | IT-157 | — |
| US-016.EC-8 | Later requirement is opened before earlier work → allowed navigation preserves incomplete status and submission rules. | — | IT-158 | — |
| US-016.EC-9 | Requirement is reopened for recapture → prior evidence is read-only and the requested replacement path is clear. | — | IT-159 | — |
| US-016.EC-10 | Template contains many requirements → grouped sections, progress, and navigation remain understandable. | — | IT-160 | — |
| US-017 | Record provenance, GPS, and geofence signals | — | — | E2E-017 |
| US-017.EC-1 | Impossible coordinates or precision values → evidence metadata is rejected or flagged as unverifiable. | — | IT-161 | — |
| US-017.EC-2 | Required GPS remains unavailable → the participant sees why capture is blocked and how to retry. | — | IT-162 | — |
| US-017.EC-3 | Location attempts exceed the bounded retry period → the effective required/optional policy is applied. | — | IT-163 | — |
| US-017.EC-4 | Another session requests evidence metadata → access is denied. | — | IT-164 | — |
| US-017.EC-5 | Location or asset policy changes during capture → the occurrence's fixed policy and location snapshot govern. | — | IT-165 | — |
| US-017.EC-6 | Permission prompt or acquisition is interrupted → the participant can retry without losing other progress. | — | IT-166 | — |
| US-017.EC-7 | Same metadata confirmation repeats → one provenance record remains associated with the evidence. | — | IT-167 | — |
| US-017.EC-8 | File is uploaded before metadata completion → submission remains pending until required metadata is resolved. | — | IT-168 | — |
| US-017.EC-9 | Asset coordinates change after submission → historical distance and flags do not change. | — | IT-169 | — |
| US-017.EC-10 | Many evidence items collect location → each item and session remain attributable without UI overload. | — | IT-170 | — |
| US-018 | Resume interrupted media uploads | — | — | E2E-018 |
| US-018.EC-1 | Declared size, MIME, or hash conflicts with the file → completion is rejected with retry guidance. | — | IT-171 | — |
| US-018.EC-2 | Local pending file is no longer available → item is marked for re-selection rather than falsely complete. | — | IT-172 | — |
| US-018.EC-3 | File exceeds size or session quota → upload is rejected before unnecessary transfer when possible. | — | IT-173 | — |
| US-018.EC-4 | Upload authority is expired or belongs to another tenant → transfer completion is denied. | — | IT-174 | — |
| US-018.EC-5 | Two resumptions upload the same file → one immutable original is accepted and duplicate completion is harmless. | — | IT-175 | — |
| US-018.EC-6 | Connection drops repeatedly → recoverable parts remain reusable until session expiry. | — | IT-176 | — |
| US-018.EC-7 | Completion request repeats after success → the same media item is returned. | — | IT-177 | — |
| US-018.EC-8 | Submission is attempted before all uploads settle → pending items are listed and submission waits or excludes only explicitly abandoned items. | — | IT-178 | — |
| US-018.EC-9 | Inspection closes while upload is pending → completion is refused and local state explains expiry. | — | IT-179 | — |
| US-018.EC-10 | Many large allowed files upload → individual progress remains visible and the interface remains responsive. | — | IT-180 | — |
| US-019 | Submit complete or incomplete evidence | — | — | E2E-019 |
| US-019.EC-1 | Extra media has a blank description or submission payload is malformed → submission is rejected with affected items listed. | — | IT-181 | — |
| US-019.EC-2 | Submission has no evidence and no valid reasons → explicit incomplete confirmation is required and result cannot be `NORMAL`. | — | IT-182 | — |
| US-019.EC-3 | Extra-photo or total-evidence limit is exceeded → extras beyond the limit are rejected before final submission. | — | IT-183 | — |
| US-019.EC-4 | Another invitation attempts submission → access is denied. | — | IT-184 | — |
| US-019.EC-5 | Submit is pressed twice or from two devices → one submission is created. | — | IT-185 | — |
| US-019.EC-6 | Connection drops during submit → retry returns the committed outcome or safely completes it once. | — | IT-186 | — |
| US-019.EC-7 | Completed submission is replayed → no duplicate analysis or evidence set is created. | — | IT-187 | — |
| US-019.EC-8 | Submit precedes required sensitive-content or upload checks → the unmet checks are shown. | — | IT-188 | — |
| US-019.EC-9 | Canceled, expired, or already submitted inspection is submitted → invalid transition is rejected. | — | IT-189 | — |
| US-019.EC-10 | Large allowed evidence set is submitted → a single observable state and stable progress are maintained. | — | IT-190 | — |
| US-020 | Prevent accidental sensitive content | — | — | E2E-020 |
| US-020.EC-1 | False-positive declaration is malformed or blank where a reason is required → override is rejected. | — | IT-191 | — |
| US-020.EC-2 | Detection cannot complete → the item remains pending rather than silently passing. | — | IT-192 | — |
| US-020.EC-3 | Repeated scans or declarations hit abuse limits → the participant is asked to wait or use impossibility handling. | — | IT-193 | — |
| US-020.EC-4 | A different session tries to override the item → access is denied. | — | IT-194 | — |
| US-020.EC-5 | Retake and false-positive declaration happen together → one final item decision is preserved. | — | IT-195 | — |
| US-020.EC-6 | Detection stops mid-check → the item resumes checking and cannot be submitted meanwhile. | — | IT-196 | — |
| US-020.EC-7 | Same declaration is submitted twice → one flag and audit action result. | — | IT-197 | — |
| US-020.EC-8 | Final submission is attempted before detection resolves → submission is blocked with the pending item identified. | — | IT-198 | — |
| US-020.EC-9 | Flagged item is later targeted for recapture → prior item and declaration remain historical. | — | IT-199 | — |
| US-020.EC-10 | Many photos require scanning → per-item status remains visible and the session does not mislabel unchecked items. | — | IT-200 | — |
| US-021 | Plan standard and exceptional stages | — | — | E2E-021 |
| US-021.EC-1 | Duplicate stage key, invalid date, or blank exceptional-stage reason → addition is rejected. | — | IT-201 | — |
| US-021.EC-2 | Multi-stage template has no planned stage → project activation is blocked. | — | IT-202 | — |
| US-021.EC-3 | Stage count reaches the configured maximum → new exceptional stages are rejected. | — | IT-203 | — |
| US-021.EC-4 | Unauthorized user adds or reorders a stage → access is denied. | — | IT-204 | — |
| US-021.EC-5 | Two users add a stage simultaneously → both receive stable distinct positions or one resolves a conflict explicitly. | — | IT-205 | — |
| US-021.EC-6 | Stage addition is interrupted → either the full stage exists or no partial stage appears. | — | IT-206 | — |
| US-021.EC-7 | Same exceptional-stage request is retried → one stage results. | — | IT-207 | — |
| US-021.EC-8 | Stage starts before its prerequisite → template ordering rule blocks it or records the allowed exception. | — | IT-208 | — |
| US-021.EC-9 | Closed project receives a new stage → addition is blocked until audited reopening. | — | IT-209 | — |
| US-021.EC-10 | Project has many allowed stages → timeline remains navigable and current stage is unambiguous. | — | IT-210 | — |
| US-022 | Skip, close, and reopen a staged project | — | — | E2E-022 |
| US-022.EC-1 | Skip or reopen reason is blank → action is rejected. | — | IT-211 | — |
| US-022.EC-2 | Closure has undecided planned stages → the user must complete or skip them first. | — | IT-212 | — |
| US-022.EC-3 | Reason exceeds the allowed length → action is rejected without truncation. | — | IT-213 | — |
| US-022.EC-4 | Viewer, participant, or out-of-scope user changes project state → access is denied. | — | IT-214 | — |
| US-022.EC-5 | Closure and stage submission occur together → one valid ordered outcome is applied and conflict is shown. | — | IT-215 | — |
| US-022.EC-6 | Close or reopen stops mid-action → project eventually reflects one durable state and matching access. | — | IT-216 | — |
| US-022.EC-7 | Skip, close, or reopen is repeated → state is idempotent and reasons are not duplicated. | — | IT-217 | — |
| US-022.EC-8 | Reopen is requested for an active project → action is rejected as unnecessary. | — | IT-218 | — |
| US-022.EC-9 | Invalidated project or archived asset is reopened → action is blocked. | — | IT-219 | — |
| US-022.EC-10 | Long project history exists → closure checks and timeline remain accurate across all stages. | — | IT-220 | — |
| US-023 | Request directed recapture | — | — | E2E-023 |
| US-023.EC-1 | Unknown item or blank reviewer reason → request is rejected. | — | IT-221 | — |
| US-023.EC-2 | No items are selected → request cannot be sent. | — | IT-222 | — |
| US-023.EC-3 | Request exceeds the allowed number of items or cycles → the user sees the applicable limit. | — | IT-223 | — |
| US-023.EC-4 | Viewer or out-of-scope user requests recapture → access is denied. | — | IT-224 | — |
| US-023.EC-5 | System and reviewer request the same item → one active requirement can carry all distinct reasons without duplicate work. | — | IT-225 | — |
| US-023.EC-6 | Notification delivery fails → the request remains active and delivery status is visible for retry. | — | IT-226 | — |
| US-023.EC-7 | Same request is retried → one active recapture request results. | — | IT-227 | — |
| US-023.EC-8 | Recapture is requested before initial submission → the user is directed to the active capture instead. | — | IT-228 | — |
| US-023.EC-9 | Canceled, invalidated, finalized-after-deadline, or closed item is targeted → invalid transition is rejected. | — | IT-229 | — |
| US-023.EC-10 | Many items are flagged → selection, reasons, and participant instructions remain item-specific and navigable. | — | IT-230 | — |
| US-024 | Complete or miss a recapture request | — | — | E2E-024 |
| US-024.EC-1 | Replacement fails media or description validation → affected requirement stays open. | — | IT-231 | — |
| US-024.EC-2 | Participant submits no correction → request remains pending until expiry. | — | IT-232 | — |
| US-024.EC-3 | Replacement exceeds item limits → it is rejected without removing prior evidence. | — | IT-233 | — |
| US-024.EC-4 | Initial or unrelated invitation token opens recapture → access is denied. | — | IT-234 | — |
| US-024.EC-5 | Deadline and resubmission occur together → one deterministic cutoff decides inclusion and is visible internally. | — | IT-235 | — |
| US-024.EC-6 | Upload or session is interrupted → resumable progress follows remaining recapture validity. | — | IT-236 | — |
| US-024.EC-7 | Corrected submission repeats → one correction set is accepted. | — | IT-237 | — |
| US-024.EC-8 | Final resubmit is attempted before replacement uploads complete → pending items are shown. | — | IT-238 | — |
| US-024.EC-9 | Link opens after completion or expiry → mutation is blocked and safe status is shown. | — | IT-239 | — |
| US-024.EC-10 | Many targeted items exist → the participant sees focused progress and no unrelated evidence. | — | IT-240 | — |
| US-025 | Receive structured evidence findings | — | — | E2E-025 |
| US-025.EC-1 | Severity, confidence, or evidence references are invalid → response is rejected rather than loosely parsed. | — | IT-241 | — |
| US-025.EC-2 | Model returns no findings → it is accepted only when the structure explicitly represents no relevant change. | — | IT-242 | — |
| US-025.EC-3 | Response or evidence count exceeds analysis limits → affected comparison becomes a visible bounded failure or is split without losing traceability. | — | IT-243 | — |
| US-025.EC-4 | Unauthorized user requests or views analysis → access is denied. | — | IT-244 | — |
| US-025.EC-5 | Duplicate analysis starts for the same evidence/profile → one accepted result version governs the report. | — | IT-245 | — |
| US-025.EC-6 | Provider timeout or worker restart occurs → bounded retry resumes and eventually reaches success or inconclusive. | — | IT-246 | — |
| US-025.EC-7 | Completed request is replayed → no duplicate finding set affects classification. | — | IT-247 | — |
| US-025.EC-8 | Analysis is requested before media verification or submission terminality → it waits for prerequisites. | — | IT-248 | — |
| US-025.EC-9 | New recapture evidence arrives → superseding analysis is created without editing prior results. | — | IT-249 | — |
| US-025.EC-10 | Many comparisons run for one inspection or tenant → each outcome remains attributable and reporting eventually reaches a terminal state. | — | IT-250 | — |
| US-026 | Apply deterministic inspection classification | — | — | E2E-026 |
| US-026.EC-1 | Unknown severity or confidence outside range → result cannot enter classification. | — | IT-251 | — |
| US-026.EC-2 | Inspection has no analyzable evidence → classification is at least `ATTENTION`, never `NORMAL`. | — | IT-252 | — |
| US-026.EC-3 | Very large finding set exists → every accepted critical and flag still affects the outcome. | — | IT-253 | — |
| US-026.EC-4 | Unauthorized user tries to change classification → no manual mutation is allowed. | — | IT-254 | — |
| US-026.EC-5 | Analysis results finish simultaneously → classification updates from the full committed terminal set. | — | IT-255 | — |
| US-026.EC-6 | Classification process stops → retry produces the same deterministic result. | — | IT-256 | — |
| US-026.EC-7 | Same findings are processed again → classification remains unchanged. | — | IT-257 | — |
| US-026.EC-8 | Partial results arrive before terminality → final classification is not published prematurely. | — | IT-258 | — |
| US-026.EC-9 | Recapture supersedes evidence → a new report classification is calculated without editing the old version. | — | IT-259 | — |
| US-026.EC-10 | Portfolio contains many reports → summary counts equal the classifications of valid latest report versions. | — | IT-260 | — |
| US-027 | Review an immutable inspection report | — | — | E2E-027 |
| US-027.EC-1 | Report ID or requested format is invalid → request is rejected without leaking another report. | — | IT-261 | — |
| US-027.EC-2 | No finding exists → report still explains evidence coverage and why classification is `NORMAL` or `ATTENTION`. | — | IT-262 | — |
| US-027.EC-3 | Evidence or text exceeds page layout limits → output paginates without dropping required content. | — | IT-263 | — |
| US-027.EC-4 | External, cross-tenant, or out-of-scope user opens report or media URL → access is denied. | — | IT-264 | — |
| US-027.EC-5 | Two generation attempts run → one logical report version is retained. | — | IT-265 | — |
| US-027.EC-6 | PDF generation fails → status remains visible and bounded retry leads to PDF or a recorded failure without changing analysis. | — | IT-266 | — |
| US-027.EC-7 | Download or generation is retried → content and report identity remain stable. | — | IT-267 | — |
| US-027.EC-8 | Report is requested before comparisons are terminal → pending status is shown. | — | IT-268 | — |
| US-027.EC-9 | Inspection is invalidated later → report remains historical and is labeled invalidated. | — | IT-269 | — |
| US-027.EC-10 | Report contains many evidence pairs → navigation and rendering remain usable and complete. | — | IT-270 | — |
| US-028 | Review consolidated or historical stage reports | — | — | E2E-028 |
| US-028.EC-1 | Unknown report mode → template cannot publish. | — | IT-271 | — |
| US-028.EC-2 | No stage has finalized → the project shows planned progress without pretending a report exists. | — | IT-272 | — |
| US-028.EC-3 | Version or stage history exceeds one page → stable chronological pagination is used. | — | IT-273 | — |
| US-028.EC-4 | User lacks project scope → no report or timeline detail is exposed. | — | IT-274 | — |
| US-028.EC-5 | Two stages finalize together → version order is deterministic and neither stage disappears. | — | IT-275 | — |
| US-028.EC-6 | Consolidation stops → prior latest version stays available until the new version completes. | — | IT-276 | — |
| US-028.EC-7 | Same stage-final event repeats → no duplicate visible report version results. | — | IT-277 | — |
| US-028.EC-8 | A late earlier-stage result arrives → history uses actual stage sequence and records result timing without rewriting prior versions. | — | IT-278 | — |
| US-028.EC-9 | Project reopens after closure → new versions append and closure history remains visible. | — | IT-279 | — |
| US-028.EC-10 | Long projects have many report versions → latest state is fast to find and full history stays navigable. | — | IT-280 | — |
| US-029 | Triage the inspection portfolio | — | — | E2E-029 |
| US-029.EC-1 | Invalid filter combination or date range → query is rejected with corrective guidance. | — | IT-281 | — |
| US-029.EC-2 | No authorized data or no match → an explanatory empty state is shown. | — | IT-282 | — |
| US-029.EC-3 | Results exceed one page → stable pagination and filter state are preserved. | — | IT-283 | — |
| US-029.EC-4 | Direct link points outside user scope → access is denied and counts never reveal it. | — | IT-284 | — |
| US-029.EC-5 | New report arrives while viewing → existing list is stable until refresh and updated totals are internally consistent. | — | IT-285 | — |
| US-029.EC-6 | Dashboard request fails → prior displayed data is labeled stale and retry is offered. | — | IT-286 | — |
| US-029.EC-7 | Refresh or repeated navigation → no duplicate rows or inflated counts appear. | — | IT-287 | — |
| US-029.EC-8 | Equal-priority reports exist → deterministic secondary ordering is used. | — | IT-288 | — |
| US-029.EC-9 | Inspection becomes invalidated or reclassified → it moves to the correct view and counts update. | — | IT-289 | — |
| US-029.EC-10 | Portfolio reaches 100× typical volume → filters, counts, and ordering remain usable and tenant-isolated. | — | IT-290 | — |
| US-030 | Receive critical-finding alerts | — | — | E2E-030 |
| US-030.EC-1 | Invalid recipient or channel → configuration is rejected. | — | IT-291 | — |
| US-030.EC-2 | No internal recipient is configured → dashboard highlight remains and configuration warning is visible. | — | IT-292 | — |
| US-030.EC-3 | Alert rate reaches configured protection limits → alerts are safely grouped or queued without losing critical inspection identity. | — | IT-293 | — |
| US-030.EC-4 | Recipient later loses scope → link access is denied even if message was delivered. | — | IT-294 | — |
| US-030.EC-5 | Multiple critical findings arrive together → notification grouping does not hide any affected inspection. | — | IT-295 | — |
| US-030.EC-6 | Provider delivery fails → bounded retry and final failure are recorded. | — | IT-296 | — |
| US-030.EC-7 | Same analysis event is replayed → no duplicate first-critical alert is sent. | — | IT-297 | — |
| US-030.EC-8 | Critical alert precedes report availability → link shows pending until report is ready rather than an error leak. | — | IT-298 | — |
| US-030.EC-9 | Later stage is no longer critical → old alert remains historical and current dashboard state updates. | — | IT-299 | — |
| US-030.EC-10 | Many tenants generate alerts → recipients and content remain isolated per tenant. | — | IT-300 | — |
| US-031 | Configure and enforce retention | — | — | E2E-031 |
| US-031.EC-1 | Negative, malformed, or disallowed retention period → save is rejected. | — | IT-301 | — |
| US-031.EC-2 | Tenant has no custom policy → all platform defaults are displayed and applied. | — | IT-302 | — |
| US-031.EC-3 | Deletion batch exceeds processing capacity → it continues in bounded batches with status, never partial silent completion. | — | IT-303 | — |
| US-031.EC-4 | Non-administrator changes policy or requests tenant-wide deletion → access is denied. | — | IT-304 | — |
| US-031.EC-5 | Policy changes while data reaches expiry → each record follows one determinable effective policy and audit explanation. | — | IT-305 | — |
| US-031.EC-6 | Deletion stops after partial derivative cleanup → retry completes the same eligible scope and status remains visible. | — | IT-306 | — |
| US-031.EC-7 | Deletion processing repeats → already deleted data remains absent and audit does not imply duplicate evidence. | — | IT-307 | — |
| US-031.EC-8 | Relationship closure is recorded late → retention start is based on the authoritative closure and recalculated transparently. | — | IT-308 | — |
| US-031.EC-9 | Closed project reopens → future retention eligibility is recalculated without restoring already lawfully deleted data. | — | IT-309 | — |
| US-031.EC-10 | Large tenant reaches mass expiry → deletion remains tenant-isolated, observable, and complete across data classes. | — | IT-310 | — |
| US-032 | Complete a periodic property inspection | — | — | E2E-032 |
| US-032.EC-1 | Property-specific required data or current evidence is invalid → affected step is blocked with guidance. | — | IT-311 | — |
| US-032.EC-2 | Property has no active origin → recurring or manual inspection creation is blocked. | — | IT-312 | — |
| US-032.EC-3 | Property origin exceeds capture session limits → template or origin activation must be corrected before invitation. | — | IT-313 | — |
| US-032.EC-4 | Participant link targets another property or internal history → access is denied. | — | IT-314 | — |
| US-032.EC-5 | Origin changes while capture is active → fixed origin remains the reference. | — | IT-315 | — |
| US-032.EC-6 | Tenant loses connectivity → resumable capture preserves progress within invitation validity. | — | IT-316 | — |
| US-032.EC-7 | Schedule or submission is replayed → one property occurrence and one submission result. | — | IT-317 | — |
| US-032.EC-8 | Participant attempts submission before every item has evidence or reason → incomplete choice is made explicit. | — | IT-318 | — |
| US-032.EC-9 | Occupancy closes before an unsubmitted occurrence → authorized staff decide cancellation; participant access reflects that state. | — | IT-319 | — |
| US-032.EC-10 | Large property has many rooms/items → grouped categories and progress remain usable. | — | IT-320 | — |
| US-033 | Report progress and nonconformities | — | — | E2E-033 |
| US-033.EC-1 | Progress value, checklist answer, or required evidence is invalid → affected requirement remains incomplete. | — | IT-321 | — |
| US-033.EC-2 | Planned stage lacks an expected reference or checklist → it cannot start until corrected or explicitly configured as reference-free. | — | IT-322 | — |
| US-033.EC-3 | Project or stage evidence limit is reached → additional items are rejected without losing accepted history. | — | IT-323 | — |
| US-033.EC-4 | Responsible party attempts internal project administration or another project → access is denied. | — | IT-324 | — |
| US-033.EC-5 | Exceptional stage and planned stage start together → timeline retains a deterministic order. | — | IT-325 | — |
| US-033.EC-6 | Field capture stops → resumable progress remains scoped to the stage. | — | IT-326 | — |
| US-033.EC-7 | Stage completion event repeats → one stage result and report version result. | — | IT-327 | — |
| US-033.EC-8 | Later construction stage starts before a required predecessor → configured ordering rule is enforced. | — | IT-328 | — |
| US-033.EC-9 | Closed project receives participant capture → access is denied until authorized reopening and a valid invitation. | — | IT-329 | — |
| US-033.EC-10 | Long construction project contains many stages and findings → latest status and full history remain navigable. | — | IT-330 | — |
| US-034 | Demonstrate cleaning execution and quality | — | — | E2E-034 |
| US-034.EC-1 | Checklist answer, origin description, or stage evidence is invalid → stage cannot complete until corrected or marked impossible where allowed. | — | IT-331 | — |
| US-034.EC-2 | No valid origin exists for an origin-based template → later comparison stage cannot start. | — | IT-332 | — |
| US-034.EC-3 | Cleaning scope exceeds template evidence or stage limits → additional content is rejected with the limit explained. | — | IT-333 | — |
| US-034.EC-4 | Cleaning participant accesses another service or internal report → access is denied. | — | IT-334 | — |
| US-034.EC-5 | Origin replacement and later stage creation race → stage fixes one complete origin version. | — | IT-335 | — |
| US-034.EC-6 | Before or after capture loses connectivity → progress resumes within the active session. | — | IT-336 | — |
| US-034.EC-7 | Same cleaning stage is submitted twice → one stage result is accepted. | — | IT-337 | — |
| US-034.EC-8 | After stage is attempted before required origin completion → start is blocked. | — | IT-338 | — |
| US-034.EC-9 | Cleaning project is closed → new stage is blocked until authorized audited reopening. | — | IT-339 | — |
| US-034.EC-10 | Cleaning operation spans many locations or checklist items → asset filters, grouped requirements, and report navigation remain usable. | — | IT-340 | — |

## Technical Coverage Matrix

| Source | Behavior | Unit | Integration |
|---|---|---|---|
| Slice registration / roots | `Setup` and dependency validation | UT-001–UT-002 | IT-351–IT-353 |
| Typed mediator | One handler, context, typed failures | UT-003–UT-004 | IT-351–IT-353 |
| TenantTx / PostgreSQL RLS | Tenant-local transaction and isolation | UT-005–UT-006 | IT-341–IT-350 |
| ObjectStore / MinIO | Private resumable multipart | UT-007–UT-008 | IT-363–IT-368 |
| SensitiveContentDetector | Pure-Go region screening | UT-009–UT-010 | IT-363, IT-366 |
| LLMGateway / LiteLLM | Structured provider-neutral analysis | UT-011–UT-012, UT-066–UT-067 | IT-371–IT-373 |
| Channel registry / senders | Independent multi-channel delivery | UT-013–UT-014, UT-046–UT-047 | IT-376–IT-379 |
| PDFRenderer / Gotenberg | PDF and HTML fallback | UT-015–UT-016 | IT-374–IT-375 |
| Authorization / OIDC | Membership, scope, PKCE | UT-017–UT-018 | IT-347, IT-349, IT-386 |
| OTP / session / CSRF | Fixed two-hour external access | UT-019–UT-021, UT-068–UT-069 | IT-369–IT-370, IT-383–IT-385 |
| Domain state machines | Valid and invalid transitions | UT-022–UT-023, UT-064–UT-065 | IT-350 |
| Template / policy compiler | Versions and snapshots | UT-024–UT-027 | IT-380 |
| Scheduler / RRULE | JIT daily occurrence and reminders | UT-028–UT-029, UT-050–UT-051 | IT-380–IT-382 |
| Completeness / GPS | Requirement and location decisions | UT-030–UT-031, UT-060–UT-061 | IT-387 |
| Media / quotas | Format, hash, count, capacity | UT-032–UT-033, UT-062–UT-063 | IT-363–IT-368, IT-388 |
| Classification | Deterministic terminal result | UT-034–UT-035 | IT-371–IT-373 |
| Report snapshots | Canonical immutable output | UT-036–UT-037, UT-064 | IT-374–IT-375 |
| Retry / outbox / inbox / RabbitMQ | At-least-once and fallback | UT-038–UT-041 | IT-354–IT-362 |
| GraphQL error mapper | Stable safe errors | UT-042–UT-043 | IT-353 |
| Retention | Clocks, hold, purge | UT-044–UT-045 | IT-390 |
| Dashboard projections | Ordered rebuildable reads | UT-048–UT-049 | IT-357, IT-360 |
| Next.js PWA state | Offline resumable isolation | UT-052–UT-053 | IT-367, IT-383–IT-387 |
| Observability | Correlation and redaction | UT-054–UT-055 | IT-389 |
| Migrator / config | Compatibility and secure startup | UT-056–UT-059 | IT-341–IT-346 |
| Audit | Append-only critical trace | UT-070–UT-071 | IT-354, IT-390 |
| Fixed validation and metering limits | Text/template caps and non-commercial byte metering | UT-072–UT-073 | IT-366, IT-388 |
| `inspection-api` | GraphQL/REST auth and tenant context | UT-001–UT-006, UT-017–UT-018, UT-042–UT-043 | IT-346–IT-353, IT-391–IT-548 |
| `inspection-worker` | Consumers, media, analysis, reports, notifications, purge | UT-007–UT-016, UT-034–UT-047 | IT-354–IT-379, IT-549–IT-586 |
| `inspection-scheduler` | Occurrences, reminders, expiry, retention work | UT-028–UT-029, UT-044–UT-045, UT-050–UT-051 | IT-380–IT-382 |
| `inspection-migrate` | Exclusive schema evolution | UT-056–UT-057 | IT-341–IT-346 |
| Next.js web | Internal SPA and external PWA trust zones | UT-052–UT-053, UT-068–UT-069 | IT-383–IT-387, IT-545–IT-548 |
| PostgreSQL | Source of truth, RLS, locks, migrations | UT-005–UT-006, UT-056–UT-057 | IT-341–IT-360 |
| RabbitMQ | Durable delivery, retry, DLQ | UT-038–UT-041 | IT-354–IT-362, IT-549–IT-586 |
| Dragonfly | Ephemeral rate limits | UT-019–UT-020 | IT-369–IT-370 |
| MinIO | Private originals, derivatives, reports | UT-007–UT-008, UT-032–UT-033 | IT-363–IT-368 |
| LiteLLM | Structured vision gateway | UT-011–UT-012, UT-066–UT-067 | IT-371–IT-373 |
| Gotenberg | Private Chromium renderer | UT-015–UT-016 | IT-374–IT-375 |
| Keycloak | Internal OIDC authentication | UT-017–UT-018 | IT-386, IT-543–IT-544 |
| SMTP / Twilio adapters | Channel sends and callbacks | UT-013–UT-014, UT-046–UT-047 | IT-376–IT-379, IT-541–IT-542 |
| Mailpit / fake Twilio | Safe test delivery sinks | UT-013–UT-014 | IT-376–IT-379 |
| OpenTelemetry / metrics | Safe operational signals | UT-054–UT-055 | IT-389, IT-539–IT-540 |


### API and Message Coverage

| Source | Behavior | Unit | Integration |
|---|---|---|---|
| GraphQL query `me` | Success and documented safe failure | — | IT-391–IT-392 |
| GraphQL query `tenant` | Success and documented safe failure | — | IT-393–IT-394 |
| GraphQL query `businessUnits` | Success and documented safe failure | — | IT-395–IT-396 |
| GraphQL query `participants` | Success and documented safe failure | — | IT-397–IT-398 |
| GraphQL query `participant` | Success and documented safe failure | — | IT-399–IT-400 |
| GraphQL query `segmentDefinitions` | Success and documented safe failure | — | IT-401–IT-402 |
| GraphQL query `templates` | Success and documented safe failure | — | IT-403–IT-404 |
| GraphQL query `templateVersion` | Success and documented safe failure | — | IT-405–IT-406 |
| GraphQL query `assets` | Success and documented safe failure | — | IT-407–IT-408 |
| GraphQL query `asset` | Success and documented safe failure | — | IT-409–IT-410 |
| GraphQL query `originVersions` | Success and documented safe failure | — | IT-411–IT-412 |
| GraphQL query `schedules` | Success and documented safe failure | — | IT-413–IT-414 |
| GraphQL query `projects` | Success and documented safe failure | — | IT-415–IT-416 |
| GraphQL query `project` | Success and documented safe failure | — | IT-417–IT-418 |
| GraphQL query `inspections` | Success and documented safe failure | — | IT-419–IT-420 |
| GraphQL query `inspection` | Success and documented safe failure | — | IT-421–IT-422 |
| GraphQL query `report` | Success and documented safe failure | — | IT-423–IT-424 |
| GraphQL query `reportDownload` | Success and documented safe failure | — | IT-425–IT-426 |
| GraphQL query `dashboardSummary` | Success and documented safe failure | — | IT-427–IT-428 |
| GraphQL query `triageInspections` | Success and documented safe failure | — | IT-429–IT-430 |
| GraphQL query `auditEvents` | Success and documented safe failure | — | IT-431–IT-432 |
| GraphQL query `notificationDeliveries` | Success and documented safe failure | — | IT-433–IT-434 |
| GraphQL query `retentionPolicies` | Success and documented safe failure | — | IT-435–IT-436 |
| GraphQL query `usageSummary` | Success and documented safe failure | — | IT-437–IT-438 |
| GraphQL mutation `createTenant` | Success and documented safe failure | — | IT-439–IT-440 |
| GraphQL mutation `updateTenant` | Success and documented safe failure | — | IT-441–IT-442 |
| GraphQL mutation `upsertBusinessUnit` | Success and documented safe failure | — | IT-443–IT-444 |
| GraphQL mutation `archiveBusinessUnit` | Success and documented safe failure | — | IT-445–IT-446 |
| GraphQL mutation `inviteInternalUser` | Success and documented safe failure | — | IT-447–IT-448 |
| GraphQL mutation `assignRoleScopes` | Success and documented safe failure | — | IT-449–IT-450 |
| GraphQL mutation `disableMembership` | Success and documented safe failure | — | IT-451–IT-452 |
| GraphQL mutation `upsertParticipant` | Success and documented safe failure | — | IT-453–IT-454 |
| GraphQL mutation `verifyContact` | Success and documented safe failure | — | IT-455–IT-456 |
| GraphQL mutation `setDeliveryChannels` | Success and documented safe failure | — | IT-457–IT-458 |
| GraphQL mutation `publishSegmentDefinition` | Success and documented safe failure | — | IT-459–IT-460 |
| GraphQL mutation `publishTemplateVersion` | Success and documented safe failure | — | IT-461–IT-462 |
| GraphQL mutation `activateTemplateVersion` | Success and documented safe failure | — | IT-463–IT-464 |
| GraphQL mutation `publishAnalysisProfile` | Success and documented safe failure | — | IT-465–IT-466 |
| GraphQL mutation `registerAsset` | Success and documented safe failure | — | IT-467–IT-468 |
| GraphQL mutation `updateAsset` | Success and documented safe failure | — | IT-469–IT-470 |
| GraphQL mutation `archiveAsset` | Success and documented safe failure | — | IT-471–IT-472 |
| GraphQL mutation `inviteOriginCapture` | Success and documented safe failure | — | IT-473–IT-474 |
| GraphQL mutation `activateOriginVersion` | Success and documented safe failure | — | IT-475–IT-476 |
| GraphQL mutation `invalidateOriginVersion` | Success and documented safe failure | — | IT-477–IT-478 |
| GraphQL mutation `createSchedule` | Success and documented safe failure | — | IT-479–IT-480 |
| GraphQL mutation `updateSchedule` | Success and documented safe failure | — | IT-481–IT-482 |
| GraphQL mutation `cancelSchedule` | Success and documented safe failure | — | IT-483–IT-484 |
| GraphQL mutation `createInspection` | Success and documented safe failure | — | IT-485–IT-486 |
| GraphQL mutation `cancelInspection` | Success and documented safe failure | — | IT-487–IT-488 |
| GraphQL mutation `invalidateInspection` | Success and documented safe failure | — | IT-489–IT-490 |
| GraphQL mutation `createProject` | Success and documented safe failure | — | IT-491–IT-492 |
| GraphQL mutation `addExceptionalStage` | Success and documented safe failure | — | IT-493–IT-494 |
| GraphQL mutation `startProjectStage` | Success and documented safe failure | — | IT-495–IT-496 |
| GraphQL mutation `skipProjectStage` | Success and documented safe failure | — | IT-497–IT-498 |
| GraphQL mutation `closeProject` | Success and documented safe failure | — | IT-499–IT-500 |
| GraphQL mutation `reopenProject` | Success and documented safe failure | — | IT-501–IT-502 |
| GraphQL mutation `requestInvitationOtp` | Success and documented safe failure | — | IT-503–IT-504 |
| GraphQL mutation `verifyInvitationOtp` | Success and documented safe failure | — | IT-505–IT-506 |
| GraphQL mutation `revokeInvitation` | Success and documented safe failure | — | IT-507–IT-508 |
| GraphQL mutation `createMediaUpload` | Success and documented safe failure | — | IT-509–IT-510 |
| GraphQL mutation `presignMediaParts` | Success and documented safe failure | — | IT-511–IT-512 |
| GraphQL mutation `completeMediaUpload` | Success and documented safe failure | — | IT-513–IT-514 |
| GraphQL mutation `saveCaptureMetadata` | Success and documented safe failure | — | IT-515–IT-516 |
| GraphQL mutation `declareCaptureImpossibility` | Success and documented safe failure | — | IT-517–IT-518 |
| GraphQL mutation `submitCapture` | Success and documented safe failure | — | IT-519–IT-520 |
| GraphQL mutation `requestRecapture` | Success and documented safe failure | — | IT-521–IT-522 |
| GraphQL mutation `submitRecapture` | Success and documented safe failure | — | IT-523–IT-524 |
| GraphQL mutation `declareSensitiveDetectionFalsePositive` | Success and documented safe failure | — | IT-525–IT-526 |
| GraphQL mutation `configureRetentionPolicy` | Success and documented safe failure | — | IT-527–IT-528 |
| GraphQL mutation `recordDeletionRequest` | Success and documented safe failure | — | IT-529–IT-530 |
| GraphQL mutation `applyLegalHold` | Success and documented safe failure | — | IT-531–IT-532 |
| GraphQL mutation `releaseLegalHold` | Success and documented safe failure | — | IT-533–IT-534 |
| REST `GET /healthz` | liveness success and failure | — | IT-535–IT-536 |
| REST `GET /readyz` | schema/dependency readiness success and failure | — | IT-537–IT-538 |
| REST `GET /metrics` | private metrics success and failure | — | IT-539–IT-540 |
| REST `POST /webhooks/twilio/status` | signed callback success and failure | — | IT-541–IT-542 |
| REST `GET /auth/callback` | PKCE callback success and failure | — | IT-543–IT-544 |
| REST `GET /capture/:linkToken` | external token exchange success and failure | — | IT-545–IT-546 |
| Event `participant.channel_verified.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-547–IT-548 |
| Event `origin.invitation_requested.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-549–IT-550 |
| Event `inspection.created.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-551–IT-552 |
| Event `inspection.state_changed.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-553–IT-554 |
| Event `media.upload_completed.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-555–IT-556 |
| Event `media.verified.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-557–IT-558 |
| Event `media.screened.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-559–IT-560 |
| Event `capture.submitted.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-561–IT-562 |
| Event `recapture.requested.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-563–IT-564 |
| Event `recapture.completed.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-565–IT-566 |
| Event `analysis.comparison_requested.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-567–IT-568 |
| Event `analysis.comparison_completed.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-569–IT-570 |
| Event `inspection.classified.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-571–IT-572 |
| Event `report.snapshot_created.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-573–IT-574 |
| Event `report.ready.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-575–IT-576 |
| Event `notification.delivery_requested.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-577–IT-578 |
| Event `notification.channel_status.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-579–IT-580 |
| Event `project.stage_changed.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-581–IT-582 |
| Event `retention.purge_due.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-583–IT-584 |
| Event `retention.purged.v1` | Valid envelope and invalid/duplicate fallback | UT-040–UT-041 | IT-585–IT-586 |
| GraphQL transport and stable errors | Local GET policy, pagination, idempotency, ten stable codes | UT-042–UT-043 | IT-587–IT-600 |
## Unit Tests — Components and Domain Contracts

- **UT-001** (happy): Feature registry accepts one typed entry point and exposes it to the selected adapter.
- **UT-002** (error): Feature registry rejects duplicate registration and a missing dependency with the slice name.
- **UT-003** (happy): Mediator routes one command/query while preserving principal, tenant, correlation, deadline, and cancellation.
- **UT-004** (error): Mediator rejects an unregistered or duplicate message type without invoking another handler.
- **UT-005** (happy): `TenantTx` begins, sets local `app.tenant_id`, calls `WithContext`, and commits.
- **UT-006** (error): `TenantTx` rolls back callback error and refuses an empty tenant or unsafe database role.
- **UT-007** (happy): `ObjectStore` maps the exact key, type, upload ID, part, and expiry to S3 multipart.
- **UT-008** (error): `ObjectStore` maps not-found, expiry, checksum, timeout, and denial to stable adapter errors.
- **UT-009** (happy): Sensitive detector returns bounded face/document regions and model digest without OCR.
- **UT-010** (error): Sensitive detector rejects corrupt/oversized decoded images and cancellation.
- **UT-011** (happy): LLM gateway builds logical model, prompt version, schema, digest, and authorized images.
- **UT-012** (error): LLM gateway rejects malformed output and safely maps provider failures.
- **UT-013** (happy): Channel registry selects a sender and aggregate succeeds after the first receipt.
- **UT-014** (error): Unknown/all-failed channels remain typed; successful channels are not retried.
- **UT-015** (happy): PDF renderer sends canonical sanitized HTML/assets with a timeout and returns bytes.
- **UT-016** (error): PDF renderer rejects external asset URLs and partial/timeout responses.
- **UT-017** (happy): Authorization requires a matching role plus tenant/resource scope.
- **UT-018** (error): Authorization denies disabled/viewer/scope/cross-tenant access without disclosure.
- **UT-019** (boundary): OTP/session accepts the final valid second/attempt and expires at the exact boundary.
- **UT-020** (error): OTP enforces resend, attempt, and hourly limits without resetting on failure.
- **UT-021** (state): Session revokes on terminal responsibility events; recapture requires a new session.
- **UT-022** (state): Every aggregate accepts each documented transition with actor, time, and required reason.
- **UT-023** (error): Aggregates reject undocumented transitions and non-idempotent terminal repeats.
- **UT-024** (happy): Template compiler validates schemas, references, safe AST, limits, and canonical digest.
- **UT-025** (error): Compiler rejects no requirements, unsafe expression, unknown component, incompatible mode, or limit.
- **UT-026** (happy): Policy resolver combines defaults and override into an immutable occurrence snapshot.
- **UT-027** (error): Policy resolver rejects an absent required reference or conflicting modes.
- **UT-028** (boundary): RRULE accepts minimum daily cadence and documented DST fixtures.
- **UT-029** (error): RRULE rejects sub-daily cadence, invalid timezone, or duplicate due instant.
- **UT-030** (happy): Completeness accepts required evidence or permitted impossibility and computes coverage.
- **UT-031** (error): Completeness names a missing/blocked requirement; failed media is never complete.
- **UT-032** (boundary): Media accepts every supported format at exactly 20 MiB and the 200th active photo.
- **UT-033** (error): Media rejects over-limit, decode/MIME/hash/parts/format errors and the 201st photo.
- **UT-034** (happy): Classifier maps terminal findings/profile to category, priority, and ordered reasons.
- **UT-035** (error): Missing/failed comparisons become inconclusive or stricter, never conforming.
- **UT-036** (happy): Report canonicalization yields a stable digest and records pinned versions.
- **UT-037** (error): Report rejects a mutable URL, missing version/digest, or published mutation.
- **UT-038** (ordering): Retry yields initial, 5-second, 30-second, and 5-minute attempts, then fallback.
- **UT-039** (error): Retry distinguishes permanent contract errors from retryable dependency failures.
- **UT-040** (idempotency): Envelope/outbox/inbox keys make redelivery yield one business outcome.
- **UT-041** (error): Event registry rejects unknown version, missing context, oversized payload, or secret/media fields.
- **UT-042** (happy): GraphQL mapper emits every stable code, optional field, and correlation ID.
- **UT-043** (error): GraphQL mapper redacts causes, SQL/provider data, keys, tokens, prompts, and contacts.
- **UT-044** (happy): Retention starts the correct class clock from closure and calculates its due instant.
- **UT-045** (state): Legal hold blocks purge and a repeated completed purge is idempotent.
- **UT-046** (happy): Notification tracks attempts and succeeds on the first delivered channel.
- **UT-047** (error): Notification preserves failures and fails only after every selected channel exhausts.
- **UT-048** (ordering): Projection reducer accepts sequence, produces deterministic counts, and rebuilds.
- **UT-049** (error): Projection ignores duplicates and defers a sequence gap without corruption.
- **UT-050** (idempotency): Scheduler materializes a due instant once and snapshots at most three reminders.
- **UT-051** (boundary): A changed RRULE affects only future unmaterialized occurrences.
- **UT-052** (happy): PWA serializes blob, upload ID, ETags, hash, and metadata for exact resume.
- **UT-053** (error): PWA isolates credential caches and reauthenticates after expiry.
- **UT-054** (happy): Telemetry sanitizer preserves correlation/code and hashes allowed tenant context.
- **UT-055** (error): Telemetry removes secrets/personal/media/provider data and unbounded labels.
- **UT-056** (happy): Migration planner orders additive models before advanced migrations.
- **UT-057** (error): Planner rejects checksum drift, incompatibility, duplicate, or unsafe destructive step.
- **UT-058** (happy): Config validates pinned endpoints, allowed origin, limits, timeouts, and private storage.
- **UT-059** (error): Config refuses missing secrets, wildcard CORS, public bucket, or privileged runtime DB.
- **UT-060** (boundary): GPS accepts accuracy exactly 50 m within 60 seconds and calculates distance deterministically.
- **UT-061** (error): GPS flags stale/inaccurate/missing data and an optional policy never blocks.
- **UT-062** (boundary): Quota accepts 100,000 assets, 1,000,000 inspections, and the active-photo boundary.
- **UT-063** (concurrency): Atomic reservation prevents tenant/global admission overshoot.
- **UT-064** (happy): Report policy uses the immutable consolidated/history flag and preserves the timeline.
- **UT-065** (state): Project requires a reason for insert/skip/reopen and blocks an invalid close.
- **UT-066** (happy): Analysis accepts evidence-bound observation/confidence/provenance with no blame or cost.
- **UT-067** (error): Analysis rejects missing evidence, confidence range, unknown requirement, blame, or cost.
- **UT-068** (happy): Helpers hash a link with SHA-256 and OTP with peppered HMAC using constant-time compare.
- **UT-069** (error): Token/CSRF rejects malformed, expired, mismatched, or replayed proof without logging it.
- **UT-070** (happy): Audit records actor, tenant, action, target, outcome, reason, time, and correlation.
- **UT-071** (error): Audit rejects a missing critical reason and deidentifies purge completion.
- **UT-072** (boundary): Validation accepts 200-code-point names, 2,000-code-point descriptions/reasons, and 1 MiB template JSON, then rejects one unit above each cap without truncation.
- **UT-073** (boundary): Storage policy meters tenant bytes without imposing a commercial byte quota and enforces only the confirmed count, file-size, capacity, and retention limits.

## Integration Tests — Technical Contracts

- **IT-341**: Migrator on an empty database creates schemas, GORM tables, constraints, RLS, roles, and expected version.
- **IT-342**: Migrator upgrades the prior supported version without deleting published evidence and records checksums once.
- **IT-343**: Versioned migration forces RLS and confirms runtime role is neither owner nor `BYPASSRLS`.
- **IT-344**: Changed applied checksum stops migration and leaves schema version unchanged.
- **IT-345**: Two migrators start; the lease permits one and prevents duplicate application.
- **IT-346**: `/readyz` is unavailable outside the compatible schema range and ready after migration.
- **IT-347**: Tenant A cannot read, update, delete, count, export, or resolve tenant B media.
- **IT-348**: Transaction without `app.tenant_id` reads zero tenant rows and cannot insert.
- **IT-349**: Tenant-admin application permission cannot bypass RLS with another tenant identifier.
- **IT-350**: Concurrent aggregate writes yield one commit and one `CONFLICT` with current version.
- **IT-351**: Every feature `Setup` registers its single framework entry point.
- **IT-352**: Duplicate registration or missing dependency fails startup with the slice identified.
- **IT-353**: Cancellation propagates through mediator, GORM `WithContext`, adapter, and error mapping.
- **IT-354**: Business write and outbox commit atomically; either forced failure leaves neither.
- **IT-355**: Outbox is published only after RabbitMQ confirm and retains correlation/causation.
- **IT-356**: Broker NACK leaves outbox claimable for one equivalent retry.
- **IT-357**: Consumer redelivery commits one inbox row and one business outcome.
- **IT-358**: Unknown event major version reaches its DLQ with a safe reason.
- **IT-359**: Failure runs initially, then after 5 seconds, 30 seconds, and 5 minutes before fallback.
- **IT-360**: Deliberate replay generation runs; replay of a committed generation is a no-op.
- **IT-361**: RabbitMQ declares durable exchanges, bounded-prefetch queues, retries, and DLQs idempotently.
- **IT-362**: Poison message reaches DLQ without blocking later valid messages.
- **IT-363**: JPEG multipart passes HEAD/part/MIME/decode/hash, creates private derivatives, and reaches `READY`.
- **IT-364**: Expired/wrong-part URL fails and refreshed authorized part URL succeeds.
- **IT-365**: Tenant B cannot create, presign, complete, read, or delete tenant A media.
- **IT-366**: Spoofed MIME, corrupt image, wrong hash, or more than 20 MiB is rejected before analysis.
- **IT-367**: Reload resumes missing parts from IndexedDB and preserves evidence lineage.
- **IT-368**: Multipart cleanup waits for reconciliation and never removes an active upload.
- **IT-369**: Dragonfly enforces OTP resend 60 seconds, five attempts, and five sends/hour.
- **IT-370**: Dragonfly outage returns `DEPENDENCY_UNAVAILABLE` and never bypasses limits.
- **IT-371**: Valid LiteLLM output persists model, prompt, digest, usage, cost, and evidence.
- **IT-372**: Malformed LiteLLM output retries without a partial finding.
- **IT-373**: Exhausted LiteLLM failure persists inconclusive comparison and terminal report.
- **IT-374**: Gotenberg renders immutable sanitized HTML and stores private hashed PDF.
- **IT-375**: Exhausted Gotenberg failure publishes HTML-only without reclassification.
- **IT-376**: One channel succeeds and another fails; aggregate delivers while failed channel retries.
- **IT-377**: All channels exhaust; aggregate fails with no false delivery.
- **IT-378**: Valid Twilio callback updates once; bad signature/stale timestamp makes no mutation.
- **IT-379**: Repeated callback is deduplicated without a second transition.
- **IT-380**: Scheduler materializes one due occurrence JIT with immutable snapshots.
- **IT-381**: Two schedulers claim one due row; uniqueness leaves one occurrence/event.
- **IT-382**: Daily DST gap/overlap fixtures produce documented instant once.
- **IT-383**: External session works until exactly two hours, then `SESSION_EXPIRED`.
- **IT-384**: Submit/finalize/cancel/invalidate/revoke immediately invalidates session.
- **IT-385**: Missing/mismatched CSRF fails; correct token permits only its responsibility.
- **IT-386**: SPA completes PKCE, persists no token, and rejects disallowed CORS.
- **IT-387**: PWA saves offline state across reload and blocks final submit until online.
- **IT-388**: At 500 global/100 tenant sessions targets hold; next tenant request gets retry guidance.
- **IT-389**: Telemetry correlates failure while redacting sensitive fields and unbounded labels.
- **IT-390**: Purge honors hold, then deletes personal associations/blobs/reports and keeps deidentified audit.

## Integration Tests — API, Routes, and Messages

- **IT-391**: `POST /graphql` executes `me` with authorized valid data and returns its schema-valid success payload.
- **IT-392**: `POST /graphql` executes `me` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-393**: `POST /graphql` executes `tenant` with authorized valid data and returns its schema-valid success payload.
- **IT-394**: `POST /graphql` executes `tenant` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-395**: `POST /graphql` executes `businessUnits` with authorized valid data and returns its schema-valid success payload.
- **IT-396**: `POST /graphql` executes `businessUnits` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-397**: `POST /graphql` executes `participants` with authorized valid data and returns its schema-valid success payload.
- **IT-398**: `POST /graphql` executes `participants` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-399**: `POST /graphql` executes `participant` with authorized valid data and returns its schema-valid success payload.
- **IT-400**: `POST /graphql` executes `participant` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-401**: `POST /graphql` executes `segmentDefinitions` with authorized valid data and returns its schema-valid success payload.
- **IT-402**: `POST /graphql` executes `segmentDefinitions` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-403**: `POST /graphql` executes `templates` with authorized valid data and returns its schema-valid success payload.
- **IT-404**: `POST /graphql` executes `templates` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-405**: `POST /graphql` executes `templateVersion` with authorized valid data and returns its schema-valid success payload.
- **IT-406**: `POST /graphql` executes `templateVersion` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-407**: `POST /graphql` executes `assets` with authorized valid data and returns its schema-valid success payload.
- **IT-408**: `POST /graphql` executes `assets` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-409**: `POST /graphql` executes `asset` with authorized valid data and returns its schema-valid success payload.
- **IT-410**: `POST /graphql` executes `asset` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-411**: `POST /graphql` executes `originVersions` with authorized valid data and returns its schema-valid success payload.
- **IT-412**: `POST /graphql` executes `originVersions` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-413**: `POST /graphql` executes `schedules` with authorized valid data and returns its schema-valid success payload.
- **IT-414**: `POST /graphql` executes `schedules` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-415**: `POST /graphql` executes `projects` with authorized valid data and returns its schema-valid success payload.
- **IT-416**: `POST /graphql` executes `projects` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-417**: `POST /graphql` executes `project` with authorized valid data and returns its schema-valid success payload.
- **IT-418**: `POST /graphql` executes `project` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-419**: `POST /graphql` executes `inspections` with authorized valid data and returns its schema-valid success payload.
- **IT-420**: `POST /graphql` executes `inspections` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-421**: `POST /graphql` executes `inspection` with authorized valid data and returns its schema-valid success payload.
- **IT-422**: `POST /graphql` executes `inspection` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-423**: `POST /graphql` executes `report` with authorized valid data and returns its schema-valid success payload.
- **IT-424**: `POST /graphql` executes `report` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-425**: `POST /graphql` executes `reportDownload` with authorized valid data and returns its schema-valid success payload.
- **IT-426**: `POST /graphql` executes `reportDownload` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-427**: `POST /graphql` executes `dashboardSummary` with authorized valid data and returns its schema-valid success payload.
- **IT-428**: `POST /graphql` executes `dashboardSummary` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-429**: `POST /graphql` executes `triageInspections` with authorized valid data and returns its schema-valid success payload.
- **IT-430**: `POST /graphql` executes `triageInspections` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-431**: `POST /graphql` executes `auditEvents` with authorized valid data and returns its schema-valid success payload.
- **IT-432**: `POST /graphql` executes `auditEvents` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-433**: `POST /graphql` executes `notificationDeliveries` with authorized valid data and returns its schema-valid success payload.
- **IT-434**: `POST /graphql` executes `notificationDeliveries` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-435**: `POST /graphql` executes `retentionPolicies` with authorized valid data and returns its schema-valid success payload.
- **IT-436**: `POST /graphql` executes `retentionPolicies` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-437**: `POST /graphql` executes `usageSummary` with authorized valid data and returns its schema-valid success payload.
- **IT-438**: `POST /graphql` executes `usageSummary` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-439**: `POST /graphql` executes `createTenant` with authorized valid data and returns its schema-valid success payload.
- **IT-440**: `POST /graphql` executes `createTenant` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-441**: `POST /graphql` executes `updateTenant` with authorized valid data and returns its schema-valid success payload.
- **IT-442**: `POST /graphql` executes `updateTenant` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-443**: `POST /graphql` executes `upsertBusinessUnit` with authorized valid data and returns its schema-valid success payload.
- **IT-444**: `POST /graphql` executes `upsertBusinessUnit` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-445**: `POST /graphql` executes `archiveBusinessUnit` with authorized valid data and returns its schema-valid success payload.
- **IT-446**: `POST /graphql` executes `archiveBusinessUnit` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-447**: `POST /graphql` executes `inviteInternalUser` with authorized valid data and returns its schema-valid success payload.
- **IT-448**: `POST /graphql` executes `inviteInternalUser` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-449**: `POST /graphql` executes `assignRoleScopes` with authorized valid data and returns its schema-valid success payload.
- **IT-450**: `POST /graphql` executes `assignRoleScopes` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-451**: `POST /graphql` executes `disableMembership` with authorized valid data and returns its schema-valid success payload.
- **IT-452**: `POST /graphql` executes `disableMembership` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-453**: `POST /graphql` executes `upsertParticipant` with authorized valid data and returns its schema-valid success payload.
- **IT-454**: `POST /graphql` executes `upsertParticipant` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-455**: `POST /graphql` executes `verifyContact` with authorized valid data and returns its schema-valid success payload.
- **IT-456**: `POST /graphql` executes `verifyContact` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-457**: `POST /graphql` executes `setDeliveryChannels` with authorized valid data and returns its schema-valid success payload.
- **IT-458**: `POST /graphql` executes `setDeliveryChannels` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-459**: `POST /graphql` executes `publishSegmentDefinition` with authorized valid data and returns its schema-valid success payload.
- **IT-460**: `POST /graphql` executes `publishSegmentDefinition` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-461**: `POST /graphql` executes `publishTemplateVersion` with authorized valid data and returns its schema-valid success payload.
- **IT-462**: `POST /graphql` executes `publishTemplateVersion` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-463**: `POST /graphql` executes `activateTemplateVersion` with authorized valid data and returns its schema-valid success payload.
- **IT-464**: `POST /graphql` executes `activateTemplateVersion` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-465**: `POST /graphql` executes `publishAnalysisProfile` with authorized valid data and returns its schema-valid success payload.
- **IT-466**: `POST /graphql` executes `publishAnalysisProfile` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-467**: `POST /graphql` executes `registerAsset` with authorized valid data and returns its schema-valid success payload.
- **IT-468**: `POST /graphql` executes `registerAsset` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-469**: `POST /graphql` executes `updateAsset` with authorized valid data and returns its schema-valid success payload.
- **IT-470**: `POST /graphql` executes `updateAsset` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-471**: `POST /graphql` executes `archiveAsset` with authorized valid data and returns its schema-valid success payload.
- **IT-472**: `POST /graphql` executes `archiveAsset` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-473**: `POST /graphql` executes `inviteOriginCapture` with authorized valid data and returns its schema-valid success payload.
- **IT-474**: `POST /graphql` executes `inviteOriginCapture` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-475**: `POST /graphql` executes `activateOriginVersion` with authorized valid data and returns its schema-valid success payload.
- **IT-476**: `POST /graphql` executes `activateOriginVersion` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-477**: `POST /graphql` executes `invalidateOriginVersion` with authorized valid data and returns its schema-valid success payload.
- **IT-478**: `POST /graphql` executes `invalidateOriginVersion` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-479**: `POST /graphql` executes `createSchedule` with authorized valid data and returns its schema-valid success payload.
- **IT-480**: `POST /graphql` executes `createSchedule` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-481**: `POST /graphql` executes `updateSchedule` with authorized valid data and returns its schema-valid success payload.
- **IT-482**: `POST /graphql` executes `updateSchedule` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-483**: `POST /graphql` executes `cancelSchedule` with authorized valid data and returns its schema-valid success payload.
- **IT-484**: `POST /graphql` executes `cancelSchedule` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-485**: `POST /graphql` executes `createInspection` with authorized valid data and returns its schema-valid success payload.
- **IT-486**: `POST /graphql` executes `createInspection` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-487**: `POST /graphql` executes `cancelInspection` with authorized valid data and returns its schema-valid success payload.
- **IT-488**: `POST /graphql` executes `cancelInspection` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-489**: `POST /graphql` executes `invalidateInspection` with authorized valid data and returns its schema-valid success payload.
- **IT-490**: `POST /graphql` executes `invalidateInspection` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-491**: `POST /graphql` executes `createProject` with authorized valid data and returns its schema-valid success payload.
- **IT-492**: `POST /graphql` executes `createProject` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-493**: `POST /graphql` executes `addExceptionalStage` with authorized valid data and returns its schema-valid success payload.
- **IT-494**: `POST /graphql` executes `addExceptionalStage` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-495**: `POST /graphql` executes `startProjectStage` with authorized valid data and returns its schema-valid success payload.
- **IT-496**: `POST /graphql` executes `startProjectStage` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-497**: `POST /graphql` executes `skipProjectStage` with authorized valid data and returns its schema-valid success payload.
- **IT-498**: `POST /graphql` executes `skipProjectStage` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-499**: `POST /graphql` executes `closeProject` with authorized valid data and returns its schema-valid success payload.
- **IT-500**: `POST /graphql` executes `closeProject` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-501**: `POST /graphql` executes `reopenProject` with authorized valid data and returns its schema-valid success payload.
- **IT-502**: `POST /graphql` executes `reopenProject` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-503**: `POST /graphql` executes `requestInvitationOtp` with authorized valid data and returns its schema-valid success payload.
- **IT-504**: `POST /graphql` executes `requestInvitationOtp` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-505**: `POST /graphql` executes `verifyInvitationOtp` with authorized valid data and returns its schema-valid success payload.
- **IT-506**: `POST /graphql` executes `verifyInvitationOtp` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-507**: `POST /graphql` executes `revokeInvitation` with authorized valid data and returns its schema-valid success payload.
- **IT-508**: `POST /graphql` executes `revokeInvitation` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-509**: `POST /graphql` executes `createMediaUpload` with authorized valid data and returns its schema-valid success payload.
- **IT-510**: `POST /graphql` executes `createMediaUpload` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-511**: `POST /graphql` executes `presignMediaParts` with authorized valid data and returns its schema-valid success payload.
- **IT-512**: `POST /graphql` executes `presignMediaParts` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-513**: `POST /graphql` executes `completeMediaUpload` with authorized valid data and returns its schema-valid success payload.
- **IT-514**: `POST /graphql` executes `completeMediaUpload` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-515**: `POST /graphql` executes `saveCaptureMetadata` with authorized valid data and returns its schema-valid success payload.
- **IT-516**: `POST /graphql` executes `saveCaptureMetadata` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-517**: `POST /graphql` executes `declareCaptureImpossibility` with authorized valid data and returns its schema-valid success payload.
- **IT-518**: `POST /graphql` executes `declareCaptureImpossibility` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-519**: `POST /graphql` executes `submitCapture` with authorized valid data and returns its schema-valid success payload.
- **IT-520**: `POST /graphql` executes `submitCapture` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-521**: `POST /graphql` executes `requestRecapture` with authorized valid data and returns its schema-valid success payload.
- **IT-522**: `POST /graphql` executes `requestRecapture` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-523**: `POST /graphql` executes `submitRecapture` with authorized valid data and returns its schema-valid success payload.
- **IT-524**: `POST /graphql` executes `submitRecapture` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-525**: `POST /graphql` executes `declareSensitiveDetectionFalsePositive` with authorized valid data and returns its schema-valid success payload.
- **IT-526**: `POST /graphql` executes `declareSensitiveDetectionFalsePositive` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-527**: `POST /graphql` executes `configureRetentionPolicy` with authorized valid data and returns its schema-valid success payload.
- **IT-528**: `POST /graphql` executes `configureRetentionPolicy` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-529**: `POST /graphql` executes `recordDeletionRequest` with authorized valid data and returns its schema-valid success payload.
- **IT-530**: `POST /graphql` executes `recordDeletionRequest` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-531**: `POST /graphql` executes `applyLegalHold` with authorized valid data and returns its schema-valid success payload.
- **IT-532**: `POST /graphql` executes `applyLegalHold` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.
- **IT-533**: `POST /graphql` executes `releaseLegalHold` with authorized valid data and returns its schema-valid success payload.
- **IT-534**: `POST /graphql` executes `releaseLegalHold` with invalid proof, scope, input, or state and returns the stable code without internal/cross-tenant data.

### REST and Browser Routes

- **IT-535**: `GET /healthz` with a valid fixture returns the documented success status, body, and headers for liveness.
- **IT-536**: `GET /healthz` with invalid proof/input or failed prerequisite returns the safe failure and makes no unauthorized mutation.
- **IT-537**: `GET /readyz` with a valid fixture returns the documented success status, body, and headers for schema/dependency readiness.
- **IT-538**: `GET /readyz` with invalid proof/input or failed prerequisite returns the safe failure and makes no unauthorized mutation.
- **IT-539**: `GET /metrics` with a valid fixture returns the documented success status, body, and headers for private metrics.
- **IT-540**: `GET /metrics` with invalid proof/input or failed prerequisite returns the safe failure and makes no unauthorized mutation.
- **IT-541**: `POST /webhooks/twilio/status` with a valid fixture returns the documented success status, body, and headers for signed callback.
- **IT-542**: `POST /webhooks/twilio/status` with invalid proof/input or failed prerequisite returns the safe failure and makes no unauthorized mutation.
- **IT-543**: `GET /auth/callback` with a valid fixture returns the documented success status, body, and headers for PKCE callback.
- **IT-544**: `GET /auth/callback` with invalid proof/input or failed prerequisite returns the safe failure and makes no unauthorized mutation.
- **IT-545**: `GET /capture/:linkToken` with a valid fixture returns the documented success status, body, and headers for external token exchange.
- **IT-546**: `GET /capture/:linkToken` with invalid proof/input or failed prerequisite returns the safe failure and makes no unauthorized mutation.

### Versioned Event Contracts

- **IT-547**: Valid `participant.channel_verified.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-548**: Duplicate or invalid-major `participant.channel_verified.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-549**: Valid `origin.invitation_requested.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-550**: Duplicate or invalid-major `origin.invitation_requested.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-551**: Valid `inspection.created.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-552**: Duplicate or invalid-major `inspection.created.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-553**: Valid `inspection.state_changed.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-554**: Duplicate or invalid-major `inspection.state_changed.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-555**: Valid `media.upload_completed.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-556**: Duplicate or invalid-major `media.upload_completed.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-557**: Valid `media.verified.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-558**: Duplicate or invalid-major `media.verified.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-559**: Valid `media.screened.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-560**: Duplicate or invalid-major `media.screened.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-561**: Valid `capture.submitted.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-562**: Duplicate or invalid-major `capture.submitted.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-563**: Valid `recapture.requested.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-564**: Duplicate or invalid-major `recapture.requested.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-565**: Valid `recapture.completed.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-566**: Duplicate or invalid-major `recapture.completed.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-567**: Valid `analysis.comparison_requested.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-568**: Duplicate or invalid-major `analysis.comparison_requested.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-569**: Valid `analysis.comparison_completed.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-570**: Duplicate or invalid-major `analysis.comparison_completed.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-571**: Valid `inspection.classified.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-572**: Duplicate or invalid-major `inspection.classified.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-573**: Valid `report.snapshot_created.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-574**: Duplicate or invalid-major `report.snapshot_created.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-575**: Valid `report.ready.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-576**: Duplicate or invalid-major `report.ready.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-577**: Valid `notification.delivery_requested.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-578**: Duplicate or invalid-major `notification.delivery_requested.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-579**: Valid `notification.channel_status.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-580**: Duplicate or invalid-major `notification.channel_status.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-581**: Valid `project.stage_changed.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-582**: Duplicate or invalid-major `project.stage_changed.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-583**: Valid `retention.purge_due.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-584**: Duplicate or invalid-major `retention.purge_due.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.
- **IT-585**: Valid `retention.purged.v1` serializes, publishes, consumes through inbox, and produces its documented local outcome once.
- **IT-586**: Duplicate or invalid-major `retention.purged.v1` does not duplicate state; invalid contract reaches its named DLQ/fallback with a safe reason.

### GraphQL Transport and Stable Errors

- **IT-587**: `GET /graphql` exposes development tooling only in local mode and is unavailable in production mode.
- **IT-588**: A malformed GraphQL document returns a protocol-valid parse/validation error and invokes no slice.
- **IT-589**: Missing or expired internal/external proof returns `UNAUTHENTICATED` with a correlation ID.
- **IT-590**: Authenticated caller outside required role or scope returns `FORBIDDEN` without resource disclosure.
- **IT-591**: Field/schema validation failure returns `INVALID_INPUT` and the exact public field name.
- **IT-592**: A state transition outside the documented state machine returns `INVALID_STATE` with no mutation.
- **IT-593**: Stale aggregate version or conflicting activation returns `CONFLICT` and safe current-version context.
- **IT-594**: OTP/session/admission limit returns `RATE_LIMITED` with retry guidance and no counter bypass.
- **IT-595**: A fixed two-hour or explicitly revoked external session returns `SESSION_EXPIRED` and clears its cookie.
- **IT-596**: A required database/broker/storage/provider prerequisite returns `DEPENDENCY_UNAVAILABLE` without internal endpoint details.
- **IT-597**: An unexpected wrapped failure returns `INTERNAL`, a correlation ID, and no stack, SQL, token, prompt, contact, or provider response.
- **IT-598**: A tenant-scoped unknown identifier returns the documented `NOT_FOUND` shape identically for absent and out-of-scope resources.
- **IT-599**: Cursor connections cap page size at 100, preserve stable order during concurrent inserts, and reject a tampered cursor.
- **IT-600**: Repeating an externally retryable mutation with the same `Idempotency-Key`/`clientMutationId` returns the original outcome; a changed payload conflicts.

## Integration Tests — Story Edge Cases

These cases execute the owning slice through its registered GraphQL, command, consumer, scheduler, or browser boundary with real authorization and persistence where the behavior crosses those boundaries.

### US-001: Configure tenant and business units

- **IT-001**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — Invalid timezone or blank tenant name → the change is rejected with the invalid field identified.
- **IT-002**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — No business unit on initial setup → activation remains unavailable until one is added.
- **IT-003**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — A configured organizational limit is reached → additional units are rejected without changing existing units.
- **IT-004**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — A non-administrator attempts tenant configuration → access is denied without revealing settings outside their scope.
- **IT-005**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — Two administrators edit the same unit → the stale save is rejected and the current values are shown.
- **IT-006**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — Setup stops before completion → a draft remains resumable and the tenant is not presented as active.
- **IT-007**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — The same create request is retried → only one tenant or unit is created.
- **IT-008**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — A unit-dependent configuration is attempted before the unit exists → the user is directed to create the prerequisite.
- **IT-009**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — An archived unit is edited → the edit is rejected until the unit is restored.
- **IT-010**: ``tenancy/create_tenant`, `tenancy/create_business_unit`` — Hundreds of units exist → lists remain paginated/searchable and counts remain accurate.

### US-002: Assign hierarchical internal access

- **IT-011**: ``access/assign_role_scope`` — Unknown role or resource identifier → assignment is rejected.
- **IT-012**: ``access/assign_role_scope`` — A scoped role has no assignments → the user sees an explanatory empty state and no operational data.
- **IT-013**: ``access/assign_role_scope`` — A bulk assignment exceeds the supported batch size → the user is asked to split it and no partial batch is applied.
- **IT-014**: ``access/assign_role_scope`` — A manager tries to grant tenant-administrator access → access is denied.
- **IT-015**: ``access/assign_role_scope`` — Scope changes while the user is active → the next protected action reflects the new scope.
- **IT-016**: ``access/assign_role_scope`` — Bulk scope assignment is interrupted → completed changes are listed and unprocessed changes remain unapplied.
- **IT-017**: ``access/assign_role_scope`` — The same role or assignment is applied twice → the result remains a single assignment.
- **IT-018**: ``access/assign_role_scope`` — Scope is assigned before the user accepts an internal invitation → it becomes effective only after account activation.
- **IT-019**: ``access/assign_role_scope`` — A disabled user attempts access with an existing session → access is denied immediately.
- **IT-020**: ``access/assign_role_scope`` — A user has thousands of assigned assets → authorization remains consistent across paginated lists, direct links, exports, and media.

### US-003: Manage external participants and delivery channels

- **IT-021**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — Malformed email or phone number → that channel cannot be saved or marked.
- **IT-022**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — No verified channel is marked → invitation creation is blocked with a contact requirement.
- **IT-023**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — More contacts are supplied than the participant limit → extras are rejected with the limit explained.
- **IT-024**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — An employee without participant-management scope edits a participant → access is denied.
- **IT-025**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — Two managers change marked channels → the stale update is rejected.
- **IT-026**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — Verification is interrupted → the channel remains unverified and cannot receive invitations.
- **IT-027**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — The same contact is added again → it is reused rather than duplicated.
- **IT-028**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — A channel is marked before verification → marking is refused until verification succeeds.
- **IT-029**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — An inactive participant is selected for a new inspection → selection is blocked.
- **IT-030**: ``participants/upsert_participant`, `participants/set_delivery_channels`` — A tenant has a large participant directory → search and filtering identify the intended participant without cross-tenant results.

### US-004: Review the audit trail

- **IT-031**: ``audit/list_events`` — Invalid date range or filter → the query is rejected with correction guidance.
- **IT-032**: ``audit/list_events`` — No records match → an empty result is shown without implying audit is disabled.
- **IT-033**: ``audit/list_events`` — A range contains more records than one page → pagination preserves stable ordering and filters.
- **IT-034**: ``audit/list_events`` — A user without audit permission requests records → access is denied.
- **IT-035**: ``audit/list_events`` — New events arrive during navigation → existing pages remain stable and a refresh exposes newer records.
- **IT-036**: ``audit/list_events`` — Export or long query is interrupted → no corrupt artifact is presented and the user can retry.
- **IT-037**: ``audit/list_events`` — The same action is safely retried → each attempted outcome is distinguishable without duplicating the business result.
- **IT-038**: ``audit/list_events`` — Events arrive after related business data → correlation still groups them in causal order when available.
- **IT-039**: ``audit/list_events`` — The target is later archived or deleted → its audit records remain readable for their retention period.
- **IT-040**: ``audit/list_events`` — Audit volume reaches 100× typical activity → filters, pagination, and tenant isolation remain correct.

### US-005: Publish immutable template versions

- **IT-041**: ``templates/publish_template`, `templates/activate_template`` — Definition violates its allowed schema or references an unknown component → publication is rejected with validation details.
- **IT-042**: ``templates/publish_template`, `templates/activate_template`` — Template has no capture requirement → publication is rejected.
- **IT-043**: ``templates/publish_template`, `templates/activate_template`` — Definition exceeds configured section, field, or requirement limits → publication is rejected without truncation.
- **IT-044**: ``templates/publish_template`, `templates/activate_template`` — Unauthorized user attempts activation → access is denied.
- **IT-045**: ``templates/publish_template`, `templates/activate_template`` — Two versions are activated at once → one active version is selected deterministically and the conflict is reported.
- **IT-046**: ``templates/publish_template`, `templates/activate_template`` — Publication stops before completion → the version remains unpublished and cannot be selected.
- **IT-047**: ``templates/publish_template`, `templates/activate_template`` — The same publication is retried → no duplicate version is created.
- **IT-048**: ``templates/publish_template`, `templates/activate_template`` — Activation is requested before validation → validation must succeed first.
- **IT-049**: ``templates/publish_template`, `templates/activate_template`` — An active version is retired → existing work remains accessible and new work requires another active version.
- **IT-050**: ``templates/publish_template`, `templates/activate_template`` — A tenant can choose among many versions → lists remain searchable and clearly distinguish status and effective version.

### US-006: Register a segment-specific asset

- **IT-051**: ``assets/register_asset`, `assets/update_asset`` — Segment-specific data violates its schema or geofence radius is out of range → save is rejected with field details.
- **IT-052**: ``assets/register_asset`, `assets/update_asset`` — Required address, identifier, or segment field is absent → activation is unavailable.
- **IT-053**: ``assets/register_asset`, `assets/update_asset`` — Asset media, attribute, or text limits are exceeded → only the invalid change is rejected.
- **IT-054**: ``assets/register_asset`, `assets/update_asset`` — User lacks scope for the business unit → creation or direct access is denied.
- **IT-055**: ``assets/register_asset`, `assets/update_asset`` — Two users edit the asset → stale changes are rejected.
- **IT-056**: ``assets/register_asset`, `assets/update_asset`` — Creation stops mid-flow → a clearly marked draft may be resumed and cannot be scheduled.
- **IT-057**: ``assets/register_asset`, `assets/update_asset`` — Same create request is retried → one asset results.
- **IT-058**: ``assets/register_asset`, `assets/update_asset`` — Schedule creation is attempted before asset activation → the prerequisite is explained.
- **IT-059**: ``assets/register_asset`, `assets/update_asset`` — Archived asset receives a new occurrence → creation is blocked.
- **IT-060**: ``assets/register_asset`, `assets/update_asset`` — A business unit has thousands of assets → list, filter, and selection behavior remain usable and isolated.

### US-007: Configure comparison and capture policies

- **IT-061**: ``templates/publish_template`, `assets/update_asset`` — Incompatible comparison or report settings are combined → save is rejected with the conflict identified.
- **IT-062**: ``templates/publish_template`, `assets/update_asset`` — A reference-required mode has no eligible reference → occurrence creation is blocked.
- **IT-063**: ``templates/publish_template`, `assets/update_asset`` — A policy value such as radius or stage count exceeds its range → save is rejected.
- **IT-064**: ``templates/publish_template`, `assets/update_asset`` — User outside asset scope changes an override → access is denied.
- **IT-065**: ``templates/publish_template`, `assets/update_asset`` — Template default and asset override change concurrently → the occurrence records the single resolved snapshot it actually used.
- **IT-066**: ``templates/publish_template`, `assets/update_asset`` — Configuration save is interrupted → prior active settings remain effective.
- **IT-067**: ``templates/publish_template`, `assets/update_asset`` — Same override is applied twice → one effective rule is shown.
- **IT-068**: ``templates/publish_template`, `assets/update_asset`` — Asset override is configured before template selection → the user must choose the template first.
- **IT-069**: ``templates/publish_template`, `assets/update_asset`` — Policy is changed after capture starts → current inspection remains unchanged.
- **IT-070**: ``templates/publish_template`, `assets/update_asset`` — Many assets inherit one template → changes apply to future occurrences without rewriting each historical asset occurrence.

### US-008: Invite an origin contributor

- **IT-071**: ``origins/invite_capture`` — Unknown asset or participant channel → invitation is rejected.
- **IT-072**: ``origins/invite_capture`` — No marked verified channel → send is blocked.
- **IT-073**: ``origins/invite_capture`` — Send or resend rate limit is reached → the user sees when another attempt is allowed.
- **IT-074**: ``origins/invite_capture`` — User lacks asset or invitation permission → send is denied.
- **IT-075**: ``origins/invite_capture`` — Two invitations are created simultaneously → each remains identifiable and only valid active invitations are presented.
- **IT-076**: ``origins/invite_capture`` — Delivery fails on some channels → outcomes are recorded per channel and retry remains available.
- **IT-077**: ``origins/invite_capture`` — A send action is retried after success → it does not create duplicate business invitations.
- **IT-078**: ``origins/invite_capture`` — Link is opened before delivery status completes → valid access can still proceed once OTP is requested.
- **IT-079**: ``origins/invite_capture`` — Asset is archived or invitation expires → the link no longer grants access.
- **IT-080**: ``origins/invite_capture`` — Bulk origin invitations are sent → every recipient remains isolated and delivery results remain attributable.

### US-009: Capture a described origin

- **IT-081**: ``origins/start_capture`, `origins/complete_capture`` — Unsupported media, blank description, or hostile text → the item is rejected with a corrective message.
- **IT-082**: ``origins/start_capture`, `origins/complete_capture`` — Finish is attempted with zero photos → completion is blocked.
- **IT-083**: ``origins/start_capture`, `origins/complete_capture`` — A file or origin exceeds configured limits → the extra item is rejected without losing accepted items.
- **IT-084**: ``origins/start_capture`, `origins/complete_capture`` — Expired or wrong-origin token is used → access is denied.
- **IT-085**: ``origins/start_capture`, `origins/complete_capture`` — Same origin session opens on two devices → conflicts are surfaced and no item is silently overwritten.
- **IT-086**: ``origins/start_capture`, `origins/complete_capture`` — Browser closes during capture → accepted local progress can resume while the invitation remains valid.
- **IT-087**: ``origins/start_capture`, `origins/complete_capture`` — Same completed upload is confirmed twice → one origin item results.
- **IT-088**: ``origins/start_capture`, `origins/complete_capture`` — Finish is attempted while uploads are pending → completion waits or identifies pending items.
- **IT-089**: ``origins/start_capture`, `origins/complete_capture`` — Session is used after completion → mutation is blocked and confirmation is shown.
- **IT-090**: ``origins/start_capture`, `origins/complete_capture`` — Origin contains many allowed items → progress, categorization, and upload status remain understandable.

### US-010: Activate and supersede an origin version

- **IT-091**: ``origins/activate_version`` — Completed origin lacks a valid described item → activation is rejected.
- **IT-092**: ``origins/activate_version`` — No prior origin exists → the first valid version becomes active.
- **IT-093**: ``origins/activate_version`` — Version history exceeds one page → versions remain paginated in activation order.
- **IT-094**: ``origins/activate_version`` — Unauthorized user attempts manual version changes → access is denied.
- **IT-095**: ``origins/activate_version`` — Two origin versions complete simultaneously → one activation order is recorded without overwriting either version.
- **IT-096**: ``origins/activate_version`` — Activation processing is interrupted → it resumes without creating a second version.
- **IT-097**: ``origins/activate_version`` — Activation is retried → the same version remains active once.
- **IT-098**: ``origins/activate_version`` — An inspection is created during origin activation → it either receives the previous or new complete version, never a partial version.
- **IT-099**: ``origins/activate_version`` — Active origin is retired with dependent schedules → new occurrences are blocked until another valid origin is active.
- **IT-100**: ``origins/activate_version`` — Thousands of inspections reference older origins → activating a new version does not rewrite or slow historical access materially.

### US-011: Configure recurring inspections

- **IT-101**: ``schedules/create_schedule`, `schedules/materialize_due`` — Invalid or impossible recurrence expression → save is rejected with an understandable schedule error.
- **IT-102**: ``schedules/create_schedule`, `schedules/materialize_due`` — Participant, template, timezone, or required reference is absent → activation is blocked.
- **IT-103**: ``schedules/create_schedule`, `schedules/materialize_due`` — Recurrence is more frequent than the allowed minimum → save is rejected with the minimum explained.
- **IT-104**: ``schedules/create_schedule`, `schedules/materialize_due`` — User lacks schedule permission for the asset → access is denied.
- **IT-105**: ``schedules/create_schedule`, `schedules/materialize_due`` — The same due occurrence is processed twice → one inspection is created.
- **IT-106**: ``schedules/create_schedule`, `schedules/materialize_due`` — Processing stops after occurrence creation but before notification → notification resumes without duplicating the inspection.
- **IT-107**: ``schedules/create_schedule`, `schedules/materialize_due`` — Manual technical retry runs after success → schedule and invitation remain single.
- **IT-108**: ``schedules/create_schedule`, `schedules/materialize_due`` — Next occurrence is calculated before current creation commits → no time is skipped or duplicated.
- **IT-109**: ``schedules/create_schedule`, `schedules/materialize_due`` — Schedule is canceled or asset archived → no later occurrence is created.
- **IT-110**: ``schedules/create_schedule`, `schedules/materialize_due`` — Many schedules become due together → every eligible occurrence is eventually created once and remains tenant-isolated.

### US-012: Create milestone and manual inspections

- **IT-111**: ``projects/start_stage`, `inspections/create_manual`` — Unknown milestone, invalid due date, or incompatible template → creation is rejected.
- **IT-112**: ``projects/start_stage`, `inspections/create_manual`` — Manual reason or responsible participant is missing → creation is blocked.
- **IT-113**: ``projects/start_stage`, `inspections/create_manual`` — Too many simultaneous open inspections for an asset → creation is rejected with the applicable limit.
- **IT-114**: ``projects/start_stage`, `inspections/create_manual`` — User lacks asset or manual-create permission → access is denied.
- **IT-115**: ``projects/start_stage`, `inspections/create_manual`` — Two users start the same milestone → only one milestone occurrence results.
- **IT-116**: ``projects/start_stage`, `inspections/create_manual`` — Creation stops before the occurrence is complete → no participant receives a partial invitation.
- **IT-117**: ``projects/start_stage`, `inspections/create_manual`` — The same request is retried → one occurrence results.
- **IT-118**: ``projects/start_stage`, `inspections/create_manual`` — A later planned stage starts before a required prior stage decision → the template rule blocks or explicitly permits it.
- **IT-119**: ``projects/start_stage`, `inspections/create_manual`` — Closed project or archived asset receives a request → creation is blocked until an authorized reopening where applicable.
- **IT-120**: ``projects/start_stage`, `inspections/create_manual`` — A manager creates occurrences across many assets → bulk status remains attributable and partial failures are explicit.

### US-013: Configure deadlines and reminders

- **IT-121**: ``schedules/schedule_reminders`` — Reminder occurs after expiry or dates are unparseable → save is rejected.
- **IT-122**: ``schedules/schedule_reminders`` — No tenant default and no occurrence value exists → activation is blocked with the missing rule identified.
- **IT-123**: ``schedules/schedule_reminders`` — Reminder count exceeds the tenant limit → extra reminders are rejected.
- **IT-124**: ``schedules/schedule_reminders`` — Unauthorized user changes deadlines → access is denied.
- **IT-125**: ``schedules/schedule_reminders`` — Deadline changes while delivery is being prepared → each notice uses one consistent saved schedule.
- **IT-126**: ``schedules/schedule_reminders`` — Delivery provider fails → per-channel failure is visible and bounded retry remains possible.
- **IT-127**: ``schedules/schedule_reminders`` — Reminder processing repeats → a due reminder is not intentionally sent more than once per channel except a recorded technical retry.
- **IT-128**: ``schedules/schedule_reminders`` — A reminder becomes due before the invitation → invitation is sent first or the invalid configuration is rejected.
- **IT-129**: ``schedules/schedule_reminders`` — Inspection is canceled, invalidated, submitted, or expired → inapplicable future notices stop.
- **IT-130**: ``schedules/schedule_reminders`` — Large reminder bursts occur → delivery status remains accurate per inspection and channel.

### US-014: Cancel or invalidate an inspection

- **IT-131**: ``inspections/cancel`, `inspections/invalidate`` — Blank invalidation reason → action is rejected.
- **IT-132**: ``inspections/cancel`, `inspections/invalidate`` — Cancellation target does not exist → no data is changed and not-found is shown.
- **IT-133**: ``inspections/cancel`, `inspections/invalidate`` — Reason exceeds its length limit → action is rejected without truncation.
- **IT-134**: ``inspections/cancel`, `inspections/invalidate`` — Viewer or out-of-scope user attempts action → access is denied.
- **IT-135**: ``inspections/cancel`, `inspections/invalidate`` — Submission and cancellation race → submitted evidence wins finality and only invalidation remains possible.
- **IT-136**: ``inspections/cancel`, `inspections/invalidate`` — Action stops after state change → notifications and access eventually reflect the same final state.
- **IT-137**: ``inspections/cancel`, `inspections/invalidate`` — Cancel or invalidate is repeated → state remains unchanged and no duplicate audit reason is manufactured.
- **IT-138**: ``inspections/cancel`, `inspections/invalidate`` — Invalidation is attempted before evidence exists → the product offers cancellation instead.
- **IT-139**: ``inspections/cancel`, `inspections/invalidate`` — Already canceled or invalidated inspection receives a mutation → invalid transition is rejected.
- **IT-140**: ``inspections/cancel`, `inspections/invalidate`` — Many invalid records exist → dashboard exclusions and explicit history filters remain correct.

### US-015: Authenticate and accept required processing

- **IT-141**: ``invitations/request_otp`, `invitations/verify_otp`` — Wrong or malformed OTP → attempt is rejected without disclosing the expected code.
- **IT-142**: ``invitations/request_otp`, `invitations/verify_otp`` — Code or required acceptance is missing → capture remains unavailable.
- **IT-143**: ``invitations/request_otp`, `invitations/verify_otp`` — Attempt or resend rate limit is reached → further attempts wait until the displayed limit resets.
- **IT-144**: ``invitations/request_otp`, `invitations/verify_otp`` — Token for another inspection is used → access is denied without revealing that inspection.
- **IT-145**: ``invitations/request_otp`, `invitations/verify_otp`` — Same code is validated simultaneously → sessions remain bounded to the same invitation and policy.
- **IT-146**: ``invitations/request_otp`, `invitations/verify_otp`` — Browser closes after validation → re-entry follows remaining session and invitation validity.
- **IT-147**: ``invitations/request_otp`, `invitations/verify_otp`` — Used or expired code is replayed → it is rejected.
- **IT-148**: ``invitations/request_otp`, `invitations/verify_otp`` — Capture route is opened before authentication or acceptance → the user is returned to the missing step.
- **IT-149**: ``invitations/request_otp`, `invitations/verify_otp`` — Canceled, completed, or expired invitation is opened → access is denied with a safe status message.
- **IT-150**: ``invitations/request_otp`, `invitations/verify_otp`` — Many participants request codes together → one participant's rate or delivery status never affects another's identity or data.

### US-016: Complete guided capture requirements

- **IT-151**: ``capture/bootstrap`, `capture/save_metadata`` — Unsupported file or blank impossibility reason → requirement remains incomplete with guidance.
- **IT-152**: ``capture/bootstrap`, `capture/save_metadata`` — Template has no applicable requirements → participant sees a controlled empty state and cannot fabricate a submission.
- **IT-153**: ``capture/bootstrap`, `capture/save_metadata`` — Per-item or total media limit is reached → additional media is rejected without removing accepted evidence.
- **IT-154**: ``capture/bootstrap`, `capture/save_metadata`` — Session does not own the requirement → direct access is denied.
- **IT-155**: ``capture/bootstrap`, `capture/save_metadata`` — Same requirement is edited on two devices → conflicts are shown and no evidence is silently overwritten.
- **IT-156**: ``capture/bootstrap`, `capture/save_metadata`` — Page refresh occurs → saved and local resumable progress returns.
- **IT-157**: ``capture/bootstrap`, `capture/save_metadata`` — Same photo completion is retried → one evidence item is associated.
- **IT-158**: ``capture/bootstrap`, `capture/save_metadata`` — Later requirement is opened before earlier work → allowed navigation preserves incomplete status and submission rules.
- **IT-159**: ``capture/bootstrap`, `capture/save_metadata`` — Requirement is reopened for recapture → prior evidence is read-only and the requested replacement path is clear.
- **IT-160**: ``capture/bootstrap`, `capture/save_metadata`` — Template contains many requirements → grouped sections, progress, and navigation remain understandable.

### US-017: Record provenance, GPS, and geofence signals

- **IT-161**: ``capture/save_metadata`` — Impossible coordinates or precision values → evidence metadata is rejected or flagged as unverifiable.
- **IT-162**: ``capture/save_metadata`` — Required GPS remains unavailable → the participant sees why capture is blocked and how to retry.
- **IT-163**: ``capture/save_metadata`` — Location attempts exceed the bounded retry period → the effective required/optional policy is applied.
- **IT-164**: ``capture/save_metadata`` — Another session requests evidence metadata → access is denied.
- **IT-165**: ``capture/save_metadata`` — Location or asset policy changes during capture → the occurrence's fixed policy and location snapshot govern.
- **IT-166**: ``capture/save_metadata`` — Permission prompt or acquisition is interrupted → the participant can retry without losing other progress.
- **IT-167**: ``capture/save_metadata`` — Same metadata confirmation repeats → one provenance record remains associated with the evidence.
- **IT-168**: ``capture/save_metadata`` — File is uploaded before metadata completion → submission remains pending until required metadata is resolved.
- **IT-169**: ``capture/save_metadata`` — Asset coordinates change after submission → historical distance and flags do not change.
- **IT-170**: ``capture/save_metadata`` — Many evidence items collect location → each item and session remain attributable without UI overload.

### US-018: Resume interrupted media uploads

- **IT-171**: ``media/create_upload`, `media/complete_upload`` — Declared size, MIME, or hash conflicts with the file → completion is rejected with retry guidance.
- **IT-172**: ``media/create_upload`, `media/complete_upload`` — Local pending file is no longer available → item is marked for re-selection rather than falsely complete.
- **IT-173**: ``media/create_upload`, `media/complete_upload`` — File exceeds size or session quota → upload is rejected before unnecessary transfer when possible.
- **IT-174**: ``media/create_upload`, `media/complete_upload`` — Upload authority is expired or belongs to another tenant → transfer completion is denied.
- **IT-175**: ``media/create_upload`, `media/complete_upload`` — Two resumptions upload the same file → one immutable original is accepted and duplicate completion is harmless.
- **IT-176**: ``media/create_upload`, `media/complete_upload`` — Connection drops repeatedly → recoverable parts remain reusable until session expiry.
- **IT-177**: ``media/create_upload`, `media/complete_upload`` — Completion request repeats after success → the same media item is returned.
- **IT-178**: ``media/create_upload`, `media/complete_upload`` — Submission is attempted before all uploads settle → pending items are listed and submission waits or excludes only explicitly abandoned items.
- **IT-179**: ``media/create_upload`, `media/complete_upload`` — Inspection closes while upload is pending → completion is refused and local state explains expiry.
- **IT-180**: ``media/create_upload`, `media/complete_upload`` — Many large allowed files upload → individual progress remains visible and the interface remains responsive.

### US-019: Submit complete or incomplete evidence

- **IT-181**: ``capture/submit`` — Extra media has a blank description or submission payload is malformed → submission is rejected with affected items listed.
- **IT-182**: ``capture/submit`` — Submission has no evidence and no valid reasons → explicit incomplete confirmation is required and result cannot be `NORMAL`.
- **IT-183**: ``capture/submit`` — Extra-photo or total-evidence limit is exceeded → extras beyond the limit are rejected before final submission.
- **IT-184**: ``capture/submit`` — Another invitation attempts submission → access is denied.
- **IT-185**: ``capture/submit`` — Submit is pressed twice or from two devices → one submission is created.
- **IT-186**: ``capture/submit`` — Connection drops during submit → retry returns the committed outcome or safely completes it once.
- **IT-187**: ``capture/submit`` — Completed submission is replayed → no duplicate analysis or evidence set is created.
- **IT-188**: ``capture/submit`` — Submit precedes required sensitive-content or upload checks → the unmet checks are shown.
- **IT-189**: ``capture/submit`` — Canceled, expired, or already submitted inspection is submitted → invalid transition is rejected.
- **IT-190**: ``capture/submit`` — Large allowed evidence set is submitted → a single observable state and stable progress are maintained.

### US-020: Prevent accidental sensitive content

- **IT-191**: ``media/screen_sensitive`` — False-positive declaration is malformed or blank where a reason is required → override is rejected.
- **IT-192**: ``media/screen_sensitive`` — Detection cannot complete → the item remains pending rather than silently passing.
- **IT-193**: ``media/screen_sensitive`` — Repeated scans or declarations hit abuse limits → the participant is asked to wait or use impossibility handling.
- **IT-194**: ``media/screen_sensitive`` — A different session tries to override the item → access is denied.
- **IT-195**: ``media/screen_sensitive`` — Retake and false-positive declaration happen together → one final item decision is preserved.
- **IT-196**: ``media/screen_sensitive`` — Detection stops mid-check → the item resumes checking and cannot be submitted meanwhile.
- **IT-197**: ``media/screen_sensitive`` — Same declaration is submitted twice → one flag and audit action result.
- **IT-198**: ``media/screen_sensitive`` — Final submission is attempted before detection resolves → submission is blocked with the pending item identified.
- **IT-199**: ``media/screen_sensitive`` — Flagged item is later targeted for recapture → prior item and declaration remain historical.
- **IT-200**: ``media/screen_sensitive`` — Many photos require scanning → per-item status remains visible and the session does not mislabel unchecked items.

### US-021: Plan standard and exceptional stages

- **IT-201**: ``projects/create_project`, `projects/add_exceptional_stage`` — Duplicate stage key, invalid date, or blank exceptional-stage reason → addition is rejected.
- **IT-202**: ``projects/create_project`, `projects/add_exceptional_stage`` — Multi-stage template has no planned stage → project activation is blocked.
- **IT-203**: ``projects/create_project`, `projects/add_exceptional_stage`` — Stage count reaches the configured maximum → new exceptional stages are rejected.
- **IT-204**: ``projects/create_project`, `projects/add_exceptional_stage`` — Unauthorized user adds or reorders a stage → access is denied.
- **IT-205**: ``projects/create_project`, `projects/add_exceptional_stage`` — Two users add a stage simultaneously → both receive stable distinct positions or one resolves a conflict explicitly.
- **IT-206**: ``projects/create_project`, `projects/add_exceptional_stage`` — Stage addition is interrupted → either the full stage exists or no partial stage appears.
- **IT-207**: ``projects/create_project`, `projects/add_exceptional_stage`` — Same exceptional-stage request is retried → one stage results.
- **IT-208**: ``projects/create_project`, `projects/add_exceptional_stage`` — Stage starts before its prerequisite → template ordering rule blocks it or records the allowed exception.
- **IT-209**: ``projects/create_project`, `projects/add_exceptional_stage`` — Closed project receives a new stage → addition is blocked until audited reopening.
- **IT-210**: ``projects/create_project`, `projects/add_exceptional_stage`` — Project has many allowed stages → timeline remains navigable and current stage is unambiguous.

### US-022: Skip, close, and reopen a staged project

- **IT-211**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Skip or reopen reason is blank → action is rejected.
- **IT-212**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Closure has undecided planned stages → the user must complete or skip them first.
- **IT-213**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Reason exceeds the allowed length → action is rejected without truncation.
- **IT-214**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Viewer, participant, or out-of-scope user changes project state → access is denied.
- **IT-215**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Closure and stage submission occur together → one valid ordered outcome is applied and conflict is shown.
- **IT-216**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Close or reopen stops mid-action → project eventually reflects one durable state and matching access.
- **IT-217**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Skip, close, or reopen is repeated → state is idempotent and reasons are not duplicated.
- **IT-218**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Reopen is requested for an active project → action is rejected as unnecessary.
- **IT-219**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Invalidated project or archived asset is reopened → action is blocked.
- **IT-220**: ``projects/skip_stage`, `projects/close_project`, `projects/reopen_project`` — Long project history exists → closure checks and timeline remain accurate across all stages.

### US-023: Request directed recapture

- **IT-221**: ``recapture/request`, `recapture/request_automatically`` — Unknown item or blank reviewer reason → request is rejected.
- **IT-222**: ``recapture/request`, `recapture/request_automatically`` — No items are selected → request cannot be sent.
- **IT-223**: ``recapture/request`, `recapture/request_automatically`` — Request exceeds the allowed number of items or cycles → the user sees the applicable limit.
- **IT-224**: ``recapture/request`, `recapture/request_automatically`` — Viewer or out-of-scope user requests recapture → access is denied.
- **IT-225**: ``recapture/request`, `recapture/request_automatically`` — System and reviewer request the same item → one active requirement can carry all distinct reasons without duplicate work.
- **IT-226**: ``recapture/request`, `recapture/request_automatically`` — Notification delivery fails → the request remains active and delivery status is visible for retry.
- **IT-227**: ``recapture/request`, `recapture/request_automatically`` — Same request is retried → one active recapture request results.
- **IT-228**: ``recapture/request`, `recapture/request_automatically`` — Recapture is requested before initial submission → the user is directed to the active capture instead.
- **IT-229**: ``recapture/request`, `recapture/request_automatically`` — Canceled, invalidated, finalized-after-deadline, or closed item is targeted → invalid transition is rejected.
- **IT-230**: ``recapture/request`, `recapture/request_automatically`` — Many items are flagged → selection, reasons, and participant instructions remain item-specific and navigable.

### US-024: Complete or miss a recapture request

- **IT-231**: ``capture/submit_recapture`, `recapture/expire_request`` — Replacement fails media or description validation → affected requirement stays open.
- **IT-232**: ``capture/submit_recapture`, `recapture/expire_request`` — Participant submits no correction → request remains pending until expiry.
- **IT-233**: ``capture/submit_recapture`, `recapture/expire_request`` — Replacement exceeds item limits → it is rejected without removing prior evidence.
- **IT-234**: ``capture/submit_recapture`, `recapture/expire_request`` — Initial or unrelated invitation token opens recapture → access is denied.
- **IT-235**: ``capture/submit_recapture`, `recapture/expire_request`` — Deadline and resubmission occur together → one deterministic cutoff decides inclusion and is visible internally.
- **IT-236**: ``capture/submit_recapture`, `recapture/expire_request`` — Upload or session is interrupted → resumable progress follows remaining recapture validity.
- **IT-237**: ``capture/submit_recapture`, `recapture/expire_request`` — Corrected submission repeats → one correction set is accepted.
- **IT-238**: ``capture/submit_recapture`, `recapture/expire_request`` — Final resubmit is attempted before replacement uploads complete → pending items are shown.
- **IT-239**: ``capture/submit_recapture`, `recapture/expire_request`` — Link opens after completion or expiry → mutation is blocked and safe status is shown.
- **IT-240**: ``capture/submit_recapture`, `recapture/expire_request`` — Many targeted items exist → the participant sees focused progress and no unrelated evidence.

### US-025: Receive structured evidence findings

- **IT-241**: ``analysis/process_comparison`` — Severity, confidence, or evidence references are invalid → response is rejected rather than loosely parsed.
- **IT-242**: ``analysis/process_comparison`` — Model returns no findings → it is accepted only when the structure explicitly represents no relevant change.
- **IT-243**: ``analysis/process_comparison`` — Response or evidence count exceeds analysis limits → affected comparison becomes a visible bounded failure or is split without losing traceability.
- **IT-244**: ``analysis/process_comparison`` — Unauthorized user requests or views analysis → access is denied.
- **IT-245**: ``analysis/process_comparison`` — Duplicate analysis starts for the same evidence/profile → one accepted result version governs the report.
- **IT-246**: ``analysis/process_comparison`` — Provider timeout or worker restart occurs → bounded retry resumes and eventually reaches success or inconclusive.
- **IT-247**: ``analysis/process_comparison`` — Completed request is replayed → no duplicate finding set affects classification.
- **IT-248**: ``analysis/process_comparison`` — Analysis is requested before media verification or submission terminality → it waits for prerequisites.
- **IT-249**: ``analysis/process_comparison`` — New recapture evidence arrives → superseding analysis is created without editing prior results.
- **IT-250**: ``analysis/process_comparison`` — Many comparisons run for one inspection or tenant → each outcome remains attributable and reporting eventually reaches a terminal state.

### US-026: Apply deterministic inspection classification

- **IT-251**: ``analysis/classify_inspection`` — Unknown severity or confidence outside range → result cannot enter classification.
- **IT-252**: ``analysis/classify_inspection`` — Inspection has no analyzable evidence → classification is at least `ATTENTION`, never `NORMAL`.
- **IT-253**: ``analysis/classify_inspection`` — Very large finding set exists → every accepted critical and flag still affects the outcome.
- **IT-254**: ``analysis/classify_inspection`` — Unauthorized user tries to change classification → no manual mutation is allowed.
- **IT-255**: ``analysis/classify_inspection`` — Analysis results finish simultaneously → classification updates from the full committed terminal set.
- **IT-256**: ``analysis/classify_inspection`` — Classification process stops → retry produces the same deterministic result.
- **IT-257**: ``analysis/classify_inspection`` — Same findings are processed again → classification remains unchanged.
- **IT-258**: ``analysis/classify_inspection`` — Partial results arrive before terminality → final classification is not published prematurely.
- **IT-259**: ``analysis/classify_inspection`` — Recapture supersedes evidence → a new report classification is calculated without editing the old version.
- **IT-260**: ``analysis/classify_inspection`` — Portfolio contains many reports → summary counts equal the classifications of valid latest report versions.

### US-027: Review an immutable inspection report

- **IT-261**: ``reports/generate_snapshot`, `reports/get_report`` — Report ID or requested format is invalid → request is rejected without leaking another report.
- **IT-262**: ``reports/generate_snapshot`, `reports/get_report`` — No finding exists → report still explains evidence coverage and why classification is `NORMAL` or `ATTENTION`.
- **IT-263**: ``reports/generate_snapshot`, `reports/get_report`` — Evidence or text exceeds page layout limits → output paginates without dropping required content.
- **IT-264**: ``reports/generate_snapshot`, `reports/get_report`` — External, cross-tenant, or out-of-scope user opens report or media URL → access is denied.
- **IT-265**: ``reports/generate_snapshot`, `reports/get_report`` — Two generation attempts run → one logical report version is retained.
- **IT-266**: ``reports/generate_snapshot`, `reports/get_report`` — PDF generation fails → status remains visible and bounded retry leads to PDF or a recorded failure without changing analysis.
- **IT-267**: ``reports/generate_snapshot`, `reports/get_report`` — Download or generation is retried → content and report identity remain stable.
- **IT-268**: ``reports/generate_snapshot`, `reports/get_report`` — Report is requested before comparisons are terminal → pending status is shown.
- **IT-269**: ``reports/generate_snapshot`, `reports/get_report`` — Inspection is invalidated later → report remains historical and is labeled invalidated.
- **IT-270**: ``reports/generate_snapshot`, `reports/get_report`` — Report contains many evidence pairs → navigation and rendering remain usable and complete.

### US-028: Review consolidated or historical stage reports

- **IT-271**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — Unknown report mode → template cannot publish.
- **IT-272**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — No stage has finalized → the project shows planned progress without pretending a report exists.
- **IT-273**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — Version or stage history exceeds one page → stable chronological pagination is used.
- **IT-274**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — User lacks project scope → no report or timeline detail is exposed.
- **IT-275**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — Two stages finalize together → version order is deterministic and neither stage disappears.
- **IT-276**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — Consolidation stops → prior latest version stays available until the new version completes.
- **IT-277**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — Same stage-final event repeats → no duplicate visible report version results.
- **IT-278**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — A late earlier-stage result arrives → history uses actual stage sequence and records result timing without rewriting prior versions.
- **IT-279**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — Project reopens after closure → new versions append and closure history remains visible.
- **IT-280**: ``reports/generate_snapshot`, `dashboard/project_timeline`` — Long projects have many report versions → latest state is fast to find and full history stays navigable.

### US-029: Triage the inspection portfolio

- **IT-281**: ``dashboard/summary`, `dashboard/list_triage`` — Invalid filter combination or date range → query is rejected with corrective guidance.
- **IT-282**: ``dashboard/summary`, `dashboard/list_triage`` — No authorized data or no match → an explanatory empty state is shown.
- **IT-283**: ``dashboard/summary`, `dashboard/list_triage`` — Results exceed one page → stable pagination and filter state are preserved.
- **IT-284**: ``dashboard/summary`, `dashboard/list_triage`` — Direct link points outside user scope → access is denied and counts never reveal it.
- **IT-285**: ``dashboard/summary`, `dashboard/list_triage`` — New report arrives while viewing → existing list is stable until refresh and updated totals are internally consistent.
- **IT-286**: ``dashboard/summary`, `dashboard/list_triage`` — Dashboard request fails → prior displayed data is labeled stale and retry is offered.
- **IT-287**: ``dashboard/summary`, `dashboard/list_triage`` — Refresh or repeated navigation → no duplicate rows or inflated counts appear.
- **IT-288**: ``dashboard/summary`, `dashboard/list_triage`` — Equal-priority reports exist → deterministic secondary ordering is used.
- **IT-289**: ``dashboard/summary`, `dashboard/list_triage`` — Inspection becomes invalidated or reclassified → it moves to the correct view and counts update.
- **IT-290**: ``dashboard/summary`, `dashboard/list_triage`` — Portfolio reaches 100× typical volume → filters, counts, and ordering remain usable and tenant-isolated.

### US-030: Receive critical-finding alerts

- **IT-291**: ``notifications/request_delivery`` — Invalid recipient or channel → configuration is rejected.
- **IT-292**: ``notifications/request_delivery`` — No internal recipient is configured → dashboard highlight remains and configuration warning is visible.
- **IT-293**: ``notifications/request_delivery`` — Alert rate reaches configured protection limits → alerts are safely grouped or queued without losing critical inspection identity.
- **IT-294**: ``notifications/request_delivery`` — Recipient later loses scope → link access is denied even if message was delivered.
- **IT-295**: ``notifications/request_delivery`` — Multiple critical findings arrive together → notification grouping does not hide any affected inspection.
- **IT-296**: ``notifications/request_delivery`` — Provider delivery fails → bounded retry and final failure are recorded.
- **IT-297**: ``notifications/request_delivery`` — Same analysis event is replayed → no duplicate first-critical alert is sent.
- **IT-298**: ``notifications/request_delivery`` — Critical alert precedes report availability → link shows pending until report is ready rather than an error leak.
- **IT-299**: ``notifications/request_delivery`` — Later stage is no longer critical → old alert remains historical and current dashboard state updates.
- **IT-300**: ``notifications/request_delivery`` — Many tenants generate alerts → recipients and content remain isolated per tenant.

### US-031: Configure and enforce retention

- **IT-301**: ``retention/configure_policy`, `retention/purge_data`` — Negative, malformed, or disallowed retention period → save is rejected.
- **IT-302**: ``retention/configure_policy`, `retention/purge_data`` — Tenant has no custom policy → all platform defaults are displayed and applied.
- **IT-303**: ``retention/configure_policy`, `retention/purge_data`` — Deletion batch exceeds processing capacity → it continues in bounded batches with status, never partial silent completion.
- **IT-304**: ``retention/configure_policy`, `retention/purge_data`` — Non-administrator changes policy or requests tenant-wide deletion → access is denied.
- **IT-305**: ``retention/configure_policy`, `retention/purge_data`` — Policy changes while data reaches expiry → each record follows one determinable effective policy and audit explanation.
- **IT-306**: ``retention/configure_policy`, `retention/purge_data`` — Deletion stops after partial derivative cleanup → retry completes the same eligible scope and status remains visible.
- **IT-307**: ``retention/configure_policy`, `retention/purge_data`` — Deletion processing repeats → already deleted data remains absent and audit does not imply duplicate evidence.
- **IT-308**: ``retention/configure_policy`, `retention/purge_data`` — Relationship closure is recorded late → retention start is based on the authoritative closure and recalculated transparently.
- **IT-309**: ``retention/configure_policy`, `retention/purge_data`` — Closed project reopens → future retention eligibility is recalculated without restoring already lawfully deleted data.
- **IT-310**: ``retention/configure_policy`, `retention/purge_data`` — Large tenant reaches mass expiry → deletion remains tenant-isolated, observable, and complete across data classes.

### US-032: Complete a periodic property inspection

- **IT-311**: `Property template seeds` — Property-specific required data or current evidence is invalid → affected step is blocked with guidance.
- **IT-312**: `Property template seeds` — Property has no active origin → recurring or manual inspection creation is blocked.
- **IT-313**: `Property template seeds` — Property origin exceeds capture session limits → template or origin activation must be corrected before invitation.
- **IT-314**: `Property template seeds` — Participant link targets another property or internal history → access is denied.
- **IT-315**: `Property template seeds` — Origin changes while capture is active → fixed origin remains the reference.
- **IT-316**: `Property template seeds` — Tenant loses connectivity → resumable capture preserves progress within invitation validity.
- **IT-317**: `Property template seeds` — Schedule or submission is replayed → one property occurrence and one submission result.
- **IT-318**: `Property template seeds` — Participant attempts submission before every item has evidence or reason → incomplete choice is made explicit.
- **IT-319**: `Property template seeds` — Occupancy closes before an unsubmitted occurrence → authorized staff decide cancellation; participant access reflects that state.
- **IT-320**: `Property template seeds` — Large property has many rooms/items → grouped categories and progress remain usable.

### US-033: Report progress and nonconformities

- **IT-321**: `Construction template seeds` — Progress value, checklist answer, or required evidence is invalid → affected requirement remains incomplete.
- **IT-322**: `Construction template seeds` — Planned stage lacks an expected reference or checklist → it cannot start until corrected or explicitly configured as reference-free.
- **IT-323**: `Construction template seeds` — Project or stage evidence limit is reached → additional items are rejected without losing accepted history.
- **IT-324**: `Construction template seeds` — Responsible party attempts internal project administration or another project → access is denied.
- **IT-325**: `Construction template seeds` — Exceptional stage and planned stage start together → timeline retains a deterministic order.
- **IT-326**: `Construction template seeds` — Field capture stops → resumable progress remains scoped to the stage.
- **IT-327**: `Construction template seeds` — Stage completion event repeats → one stage result and report version result.
- **IT-328**: `Construction template seeds` — Later construction stage starts before a required predecessor → configured ordering rule is enforced.
- **IT-329**: `Construction template seeds` — Closed project receives participant capture → access is denied until authorized reopening and a valid invitation.
- **IT-330**: `Construction template seeds` — Long construction project contains many stages and findings → latest status and full history remain navigable.

### US-034: Demonstrate cleaning execution and quality

- **IT-331**: `Cleaning template seeds` — Checklist answer, origin description, or stage evidence is invalid → stage cannot complete until corrected or marked impossible where allowed.
- **IT-332**: `Cleaning template seeds` — No valid origin exists for an origin-based template → later comparison stage cannot start.
- **IT-333**: `Cleaning template seeds` — Cleaning scope exceeds template evidence or stage limits → additional content is rejected with the limit explained.
- **IT-334**: `Cleaning template seeds` — Cleaning participant accesses another service or internal report → access is denied.
- **IT-335**: `Cleaning template seeds` — Origin replacement and later stage creation race → stage fixes one complete origin version.
- **IT-336**: `Cleaning template seeds` — Before or after capture loses connectivity → progress resumes within the active session.
- **IT-337**: `Cleaning template seeds` — Same cleaning stage is submitted twice → one stage result is accepted.
- **IT-338**: `Cleaning template seeds` — After stage is attempted before required origin completion → start is blocked.
- **IT-339**: `Cleaning template seeds` — Cleaning project is closed → new stage is blocked until authorized audited reopening.
- **IT-340**: `Cleaning template seeds` — Cleaning operation spans many locations or checklist items → asset filters, grouped requirements, and report navigation remain usable.

## End-to-End Tests — User Journeys

- **E2E-001** (US-001): Configure tenant and business units — Given a new tenant, when the administrator supplies its name, language, default timezone, and at least one business unit, then the tenant becomes available for configuration. Given an active tenant, when the administrator adds or updates a business unit, then authorized users see the current structure without changing historical inspection ownership. Given the initial defaults, when no override exists, then the tenant uses Portuguese (Brazil), `America/Sao_Paulo`, and platform notification and retention defaults.
- **E2E-002** (US-002): Assign hierarchical internal access — Given an internal user, when the administrator assigns `TENANT_ADMIN`, `MANAGER`, `EMPLOYEE`, or `VIEWER`, then the product explains the resulting capabilities. Given a manager or viewer, when business units are assigned, then the user can access only those units. Given an employee, when assets or inspections are assigned, then the employee can operate only those assignments. Given a viewer, when they open an allowed resource, then every mutating action is unavailable.
- **E2E-003** (US-003): Manage external participants and delivery channels — Given a participant, when the manager records their segment role and contact information, then the participant can be linked to an asset, occupancy, service, or project. Given verified email, WhatsApp, or SMS contacts, when the manager marks multiple channels, then future invitations use all marked channels simultaneously. Given an updated contact, when it is verified, then it may replace or supplement existing marked channels without changing historical deliveries.
- **E2E-004** (US-004): Review the audit trail — Given an audited action, when it completes, then the trail records actor, tenant, time, action, target, outcome, and available correlation context. Given the audit view, when the administrator filters by actor, asset, inspection, action, or period, then matching records are returned in stable time order. Given a recapture, false-positive declaration, invalidation, project reopening, stage insertion, or retention action, then its reason is visible in the audit trail.
- **E2E-005** (US-005): Publish immutable template versions — Given a valid administrative template definition, when a version is published, then its segment, roles, sections, requirements, policies, and report settings become immutable. Given a newly activated version, when a new occurrence starts, then it uses that version. Given an inspection or staged project already started, when another version activates, then the existing work retains its original version. Given the MVP, then tenant users can select and configure published templates but cannot use a visual template editor.
- **E2E-006** (US-006): Register a segment-specific asset — Given a selected segment, when the manager enters universal and segment-specific required fields, then the asset is created in an assigned business unit. Given location-enabled policies, when coordinates and geofence radius are set, then future capture can evaluate distance against that asset. Given a segment-specific field definition, when the value changes, then the new value is validated without rewriting historical inspection snapshots.
- **E2E-007** (US-007): Configure comparison and capture policies — Given a template, when comparison mode is configured, then it uses exactly one of fixed origin, planned stage, previous inspection, before/after, or checklist-only. Given a template, when multi-stage and report-mode flags are configured, then any segment can enable or disable those behaviors. Given GPS required by default, when an asset override makes it optional, then missing valid GPS produces a flag instead of blocking. Given a policy snapshot at occurrence creation, then later configuration changes do not alter that occurrence.
- **E2E-008** (US-008): Invite an origin contributor — Given an eligible asset and marked verified channels, when the manager creates an origin invitation, then one expiring link is sent to all marked channels. Given the link, when any recipient opens it, then OTP validation through the invitation's marked channels is still required. Given a valid session, then its access is limited to that origin capture and cannot open tenant data or other assets.
- **E2E-009** (US-009): Capture a described origin — Given an authenticated origin session, when the contributor adds a photo, then they must select or confirm a category and provide a nonblank description. Given each accepted photo, then capture source, time, GPS, precision, device context, hash, and quality signals are associated with it. Given at least one described photo and every mandatory validation satisfied, when the contributor finishes, then the origin version becomes eligible for activation. Given gallery media, when it is accepted, then it is visibly marked as gallery-originated.
- **E2E-010** (US-010): Activate and supersede an origin version — Given a completed origin with at least one described item, when validation finishes, then it activates automatically even when nonblocking quality, gallery, or geofence flags exist. Given an active origin, when a replacement is captured, then a new immutable version becomes active and the prior version remains readable. Given an already created inspection, when a new origin activates, then that inspection keeps the origin version fixed at creation.
- **E2E-011** (US-011): Configure recurring inspections — Given an active asset, participant, template, and reference when required, when the manager saves a valid recurrence and timezone, then the next occurrence is displayed. Given a due recurrence, when it is processed, then at most one inspection is created for that occurrence with the then-active template and origin versions fixed. Given an occurrence created successfully, then the schedule advances to the next valid time without duplicating the invitation.
- **E2E-012** (US-012): Create milestone and manual inspections — Given a planned milestone becomes due, when an authorized user starts it, then an inspection is created for the milestone and expected stage. Given a valid asset and participant, when an authorized user creates a manual inspection, then the user supplies a reason and applicable due-date settings. Given either trigger, then the occurrence fixes its template, reference, capture policy, report policy, and responsible external participant.
- **E2E-013** (US-013): Configure deadlines and reminders — Given tenant defaults, when a schedule, milestone, or manual occurrence omits its own settings, then those defaults apply. Given valid overrides, when the occurrence is created, then its invitation, reminder times, and expiry are shown before activation. Given multiple marked participant channels, when an invitation, reminder, or recapture notice is due, then the same notice is attempted on all of them and each outcome is recorded.
- **E2E-014** (US-014): Cancel or invalidate an inspection — Given no evidence has been submitted, when an authorized user cancels the inspection, then participant access and future notifications stop. Given any evidence has been submitted, when an authorized user supplies a reason, then the inspection becomes invalidated rather than deleted. Given an invalidated inspection, then it remains visible in history and audit but is excluded from valid-inspection dashboard indicators.
- **E2E-015** (US-015): Authenticate and accept required processing — Given an active invitation, when the participant requests a code, then the same OTP is sent to every marked verified channel. Given a valid code within expiry and attempt limits, when it is submitted, then a short session grants access only to that inspection or origin. Before capture, the participant must explicitly accept disclosed processing of photos, AI analysis, and GPS; refusal blocks the flow. The disclosure identifies purpose, recipients, retention, tenant controller, and the absence of automatic participant consequences.
- **E2E-016** (US-016): Complete guided capture requirements — Given a visual reference, when a requirement opens, then the participant sees the reference, description, category, and optional alignment overlay. For each requirement, the participant must either capture corresponding media or provide a nonblank impossibility reason. Camera capture is the default; gallery selection remains available and produces a visible risk flag. The participant sees progress, pending uploads, missing requirements, and blocking validations before submission.
- **E2E-017** (US-017): Record provenance, GPS, and geofence signals — Given GPS is required by the effective policy, when no sufficiently valid location is obtained after guided retries, then capture cannot proceed. Given GPS is optional, when location is absent or imprecise, then capture proceeds with a visible flag. Given valid location outside the asset radius, then capture proceeds and records distance, precision, and an out-of-geofence flag. Given any evidence, then source, time, device context, original hash, and applicable quality or provenance flags are preserved and described as signals rather than proof.
- **E2E-018** (US-018): Resume interrupted media uploads — Given an accepted file, when upload begins, then progress and local pending state are visible. Given connection loss or page refresh, when the participant returns with a valid session, then incomplete uploads resume from recoverable progress. Given completion, then the product verifies the stored object and metadata before marking the evidence ready. Original media is never silently overwritten by retry or replacement.
- **E2E-019** (US-019): Submit complete or incomplete evidence — Given all requirements have evidence or an impossibility reason and uploads are ready, when the participant submits, then the submission becomes immutable. Given unresolved required items, when the participant confirms incomplete submission, then it is accepted and will classify at least `ATTENTION`. Given extra photos, then each requires a nonblank description and is included in analysis and report context. After submission, the participant sees confirmation only and cannot browse submitted evidence or internal results.
- **E2E-020** (US-020): Prevent accidental sensitive content — Before and during capture, the participant is instructed not to photograph faces, documents, or other identifiable personal information. Given detected prohibited content, then submission of that item is blocked and the participant can retake it or record impossibility. Given a false detection, when the participant declares a false positive, then the item may proceed with a flag and audited declaration for internal review.
- **E2E-021** (US-021): Plan standard and exceptional stages — Given multi-stage enabled, when a project starts, then planned stage names, order, expected requirements, and optional dates are copied from the template version. Given an active project, when an authorized user adds an exceptional stage with a reason, then it appears in the plan and audit trail. Given a stage starts, then its effective reference and policies are fixed for that stage.
- **E2E-022** (US-022): Skip, close, and reopen a staged project — Given a planned stage will not occur, when an authorized user supplies a reason, then it is marked skipped and the consolidated result is at least `ATTENTION`. Given all stage decisions are terminal, when an authorized user closes the project, then new stages and captures are blocked. Given a closed project, when an authorized user reopens it with a reason, then new permitted work resumes and the reopening is audited.
- **E2E-023** (US-023): Request directed recapture — Given objective quality or policy failure, when the system identifies an eligible item, then it can create a recapture request with the detected reason. Given a submitted inspection, when an authorized internal user selects items and supplies reasons, then only those requirements reopen. Given a recapture request, then prior evidence remains immutable and visible internally, a new controlled invitation is sent to all marked channels, and the final report waits for correction or deadline.
- **E2E-024** (US-024): Complete or miss a recapture request — Given a valid recapture link and OTP, when the participant enters, then only requested requirements and their reasons are available. Given corrected media, when the participant resubmits, then the new evidence is added without overwriting the original. Given all requested corrections complete, then analysis and final reporting continue. Given deadline expiry with correction pending, then the inspection finalizes with available evidence, identifies uncorrected items, and classifies at least `ATTENTION`.
- **E2E-025** (US-025): Receive structured evidence findings — Given analyzable reference and current evidence, when analysis completes, then each finding includes category, title, description, severity, confidence, referenced media, evidence quality, observations, and recommended action. Given an AI response outside the required structure, then it is rejected and retried only within the configured attempt policy. Given permanent failure or insufficient evidence, then the affected comparison becomes inconclusive and remains visible in the report. Findings use neutral observed-change language and do not assign blame, cost, legal responsibility, or automatic action.
- **E2E-026** (US-026): Apply deterministic inspection classification — If any accepted finding is `CRITICAL`, then the inspection classification is `CRITICAL`. If no critical finding exists but any noncritical change, missing evidence, gallery/geofence/quality flag, skipped stage, uncorrected request, or inconclusive analysis exists, then classification is `ATTENTION`. Classification is `NORMAL` only when all required evidence is analyzed, no relevant change exists, and no flag or inconclusive outcome exists. Thresholds and classification rules are fixed by the inspection's analysis-profile version and are not inferred from prompt prose.
- **E2E-027** (US-027): Review an immutable inspection report — Given all comparisons reach terminal states, then one immutable HTML/PDF report version is generated even when some are inconclusive. The report shows classification, reference/current evidence side by side, descriptions, provenance flags, findings, confidence, and recommended actions. The report identifies itself as internal advisory triage and is not available through the external participant session. An authorized user can download the PDF and relate it to its inspection, template, reference, analysis profile, and version.
- **E2E-028** (US-028): Review consolidated or historical stage reports — Given consolidated mode, when each stage finalizes, then a new immutable consolidated version becomes available and the latest is the default view. Given historical mode, when each stage finalizes, then it receives its own immutable report in a chronological project history. In consolidated mode, the headline classification reflects the newest stage and pendencies still observable, while prior classifications remain visible on the timeline. Skipped stages, exceptional stages, closure, and reopening remain visible in either mode.
- **E2E-029** (US-029): Triage the inspection portfolio — The dashboard shows totals and trends by `CRITICAL`, `ATTENTION`, and `NORMAL` for the user's authorized scope. The user can filter by asset, business unit, period, status, segment, classification, and flags. Lists prioritize critical, attention, then normal latest valid reports and open the corresponding evidence comparison. Invalidated inspections remain available through explicit history filters but do not count in valid indicators.
- **E2E-030** (US-030): Receive critical-finding alerts — Given selected internal recipients and channels, when a new report version first contains a `CRITICAL` finding, then an alert is sent and the dashboard highlights it. The alert identifies tenant-safe inspection context and links only authorized users to internal details. External participants are never automatic recipients of finding or classification alerts. Delivery outcomes and retries are visible to authorized internal users.
- **E2E-031** (US-031): Configure and enforce retention — The administrator can configure retention separately for evidence/reports, operational records/location, and invitation/OTP secrets within allowed policy bounds. Without overrides, evidence, inspections, and reports remain for five years after occupancy, service relationship, or project closure; operational records and separately usable location remain for one year; secrets remain only until expiry. A deletion request before configured expiry is recorded and refused with the active retention restriction explained. At expiry, eligible originals, derivatives, reports, and linked personal data are deleted or irreversibly de-identified as applicable, and the outcome is audited.
- **E2E-032** (US-032): Complete a periodic property inspection — A property inspection fixes the active origin version at occurrence creation and shows each origin image and description as a capture requirement. The participant captures a corresponding image or explains impossibility for every origin item and may add described extra images. The resulting report compares origin and current evidence, exposes condition changes and flags internally, and remains unavailable to the participant. Activating a new origin affects future occurrences only.
- **E2E-033** (US-033): Report progress and nonconformities — Each construction stage displays its planned stage reference, requirements, quality checklist, and applicable prior evidence. The participant records observed progress and evidence for nonconformities without assigning contractual fault or cost. Authorized users can add exceptional stages, skip planned stages with reason, and close or reopen the project under the shared staged lifecycle. The selected consolidated or historical report mode shows progress, current pendencies, flags, and prior stage classifications.
- **E2E-034** (US-034): Demonstrate cleaning execution and quality — The first configured stage establishes immutable origin photos and descriptions for the cleaning scope. Each later stage captures comparable after evidence and completes the template quality checklist. The template-selected report mode produces either a latest consolidated view with immutable versions or a chronological report per stage. The product reports observed execution, quality gaps, skipped items, and flags without scoring employee productivity, hours, or individual performance.
