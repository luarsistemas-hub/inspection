# Task 04 Memory

## Current state

- Scheduling, inspection, and project lifecycle delivery is implemented and verified.
- Task tracking can be marked completed; auto-commit remains disabled.

## Decisions

- Preserve the existing optional `aggregateId` event-envelope field while satisfying all mandatory lifecycle event fields.
- Treat `_prd.md`, `_techspec.md`, `_user_stories.md`, `_tests.md`, the applicable ADRs, and `task_04.md` as the contract because `_spec.md` is absent.
- Use deterministic event IDs and unique business keys for schedule materialization, reminder dispatch, inspection creation, and invitation outcomes.
- Keep lifecycle commands atomic to their owned schema and propagate invitation, reminder-cancel, and session-revocation outcomes through versioned events.
- Auto-commit is disabled because the dispatch did not enable it.

## Learnings

- Partial Task 04 foundations already existed at kickoff: lifecycle database models, recurrence code, and inspection core. Projects, public operation slices, GraphQL lifecycle fields, scheduler registration, async outcomes, and task coverage were incomplete.
- `rrule-go` orders frequencies from yearly through secondly; sub-daily rules are values greater than `DAILY`.
- Civil-time recurrence must retain configured wall-clock components independently of an absolute-time candidate to handle DST gaps and overlaps deterministically.
- GORM adds a non-zero destination primary key to `First`; idempotency lookups must query into an empty value rather than a preinitialized create row.
- Grouped `identity.ID` declarations without explicit GORM UUID tags become PostgreSQL `text`, causing UUID RLS policies to fail on clean migration.

## Touched surfaces

- `services/inspection/internal/features/{schedules,inspections,projects}/**`: lifecycle cores, operation setup slices, and tests.
- `services/inspection/internal/platform/graphql/**`: lifecycle schema, generated bindings, resolver wiring, mappers, and contract tests.
- `services/inspection/internal/features/messaging/consume_events/**` and `internal/contracts/events/**`: invitation chaining, reminder cancellation, session revocation, registry behavior, and idempotent consumer coverage.
- `services/inspection/internal/platform/database/**`: lifecycle fields/models plus UUID typing and current schema-version integration expectation needed by clean migrations.
- `services/inspection/cmd/{inspection-api,inspection-scheduler}/main.go`: lifecycle registration and scheduler loop.
- `services/inspection/internal/features/lifecycle_{contract,integration}_test/**`: setup, PostgreSQL/RLS, idempotency, lifecycle, and snapshot coverage.
- `services/inspection/internal/features/recapture/core/core.go`: removed a behavior-neutral self-assignment that blocked repository-wide `go vet`.

## Verification

- Focused lifecycle packages: passed.
- PostgreSQL migration/RLS test against isolated database `inspection_task4_verify_host2`: passed.
- PostgreSQL lifecycle integration with runtime RLS role: passed.
- `go test -count=1 ./...` with both PostgreSQL integration DSNs: passed.
- `go vet ./...`: passed.
- `go build ./...`: passed.
- `git diff --check`: passed.

## Follow-up

- The isolated local verification databases and the started Compose PostgreSQL container were intentionally left available for subsequent task runs.
